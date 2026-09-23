package dap

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// JsDebugVersion is pinned: the adapter and this server agree on a protocol, and
// a surprise upgrade is a silent change to that agreement.
const JsDebugVersion = "v1.117.0"

// jsDebugAnnouncement is what the adapter prints once it is listening. Parsing
// it rather than assuming a port is what makes port zero usable, and port zero
// is what stops two sessions colliding.
var jsDebugAnnouncement = regexp.MustCompile(`listening at\s+(\S+:\d+)`)

// jsDebugHome finds the adapter. Unlike Delve there is nothing to `go install`:
// Microsoft ships the standalone DAP server as a release asset, so this is a
// directory rather than a binary on PATH.
func jsDebugHome() (string, error) {
	var searched []string
	if dir := os.Getenv("DBGMCP_JS_DEBUG"); dir != "" {
		if ok, _ := isJsDebugDir(dir); ok {
			return dir, nil
		}
		searched = append(searched, dir+" (from DBGMCP_JS_DEBUG)")
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".cache", "dbgmcp", "js-debug")
		if ok, _ := isJsDebugDir(candidate); ok {
			return candidate, nil
		}
		searched = append(searched, candidate)
	}
	return "", fmt.Errorf(
		"the JavaScript debug adapter was not found in %v. It is a GitHub release asset rather than an npm package, so install it with:\n"+
			"  mkdir -p ~/.cache/dbgmcp && cd ~/.cache/dbgmcp \\\n"+
			"    && gh release download %s -R microsoft/vscode-js-debug -p 'js-debug-dap-*.tar.gz' -O js-debug.tar.gz \\\n"+
			"    && tar xzf js-debug.tar.gz && rm js-debug.tar.gz\n"+
			"Or point DBGMCP_JS_DEBUG at an existing copy.",
		searched, JsDebugVersion)
}

func isJsDebugDir(dir string) (bool, string) {
	entry := filepath.Join(dir, "src", "dapDebugServer.js")
	if st, err := os.Stat(entry); err == nil && !st.IsDir() {
		return true, entry
	}
	return false, ""
}

var node = &Adapter{
	Language: "node",
	Install:  "download js-debug-dap from microsoft/vscode-js-debug releases into ~/.cache/dbgmcp/js-debug",

	// js-debug reports where a function breakpoint bound, unlike debugpy.
	symbolBreakpointsUsable: false,

	// stopOnEntry is required here -- without it the program runs to completion
	// before a breakpoint can be set, which is the race the stopped-at-start
	// contract exists to prevent. The price is that js-debug then pauses
	// repeatedly with reason "entry" at places nobody asked about, attributed to
	// no breakpoint, before execution settles. Passing those to the agent hands
	// it a frame it did not ask for, where the names it wants genuinely do not
	// exist.
	spuriousEntryStops: true,

	start: func() (*exec.Cmd, *conn, dialer, error) {
		home, err := jsDebugHome()
		if err != nil {
			return nil, nil, nil, err
		}
		_, entry := isJsDebugDir(home)
		if _, err := exec.LookPath("node"); err != nil {
			return nil, nil, nil, fmt.Errorf("node is not on PATH, and the JavaScript debug adapter runs on it: %w", err)
		}

		// Port zero, then read back the one it chose. A fixed port is a
		// collision between two sessions waiting to happen.
		cmd := exec.Command("node", entry, "0", "127.0.0.1")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			return nil, nil, nil, fmt.Errorf("could not start the JavaScript debug adapter: %w", err)
		}

		address, err := readAnnouncedAddress(stdout, 30*time.Second)
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, nil, nil, err
		}
		socket, err := net.DialTimeout("tcp", address, 10*time.Second)
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, nil, nil, fmt.Errorf("the adapter announced %s but it could not be reached: %w", address, err)
		}

		// One adapter, many sessions: the child that actually owns the debuggee
		// connects to the same port the parent announced.
		again := func() (*conn, error) {
			child, err := net.DialTimeout("tcp", address, 10*time.Second)
			if err != nil {
				return nil, fmt.Errorf("could not open a child session at %s: %w", address, err)
			}
			return newConn(child, child), nil
		}
		return cmd, newConn(socket, socket), again, nil
	},

	launchArgs: func(req model.LaunchRequest) (map[string]any, error) {
		args := map[string]any{
			// pwa-node is js-debug's own name for the Node launcher; the older
			// "node" type routes to the retired adapter.
			"type":    "pwa-node",
			"request": "launch",
			"cwd":     req.WorkDir,
			// Stop before the first statement, the same contract as the other
			// backends, so breakpoints can be set while nothing has run.
			"stopOnEntry": true,
			"console":     "internalConsole",
			// TypeScript is compiled before it runs, so without this every
			// breakpoint lands in generated JavaScript the agent never wrote.
			"sourceMaps": true,
			// Node's own internals are never the answer, and they bury the
			// frames that are.
			"skipFiles": []string{"<node_internals>/**"},
		}
		if len(req.Env) > 0 {
			args["env"] = req.Env
		}

		switch req.Mode {
		case model.LaunchDebug, model.LaunchExec:
			if req.Target == "" {
				return nil, fmt.Errorf("mode %q needs a target: the script to run", req.Mode)
			}
			args["program"] = absolute(req.WorkDir, req.Target)
			if len(req.Args) > 0 {
				args["args"] = req.Args
			}
		case model.LaunchTest:
			// A JavaScript project's test command lives in its package.json, so
			// the runner is whatever the project says it is rather than
			// something this server picks.
			runner, runnerArgs, err := nodeTestRunner(req)
			if err != nil {
				return nil, err
			}
			args["runtimeExecutable"] = runner
			args["runtimeArgs"] = runnerArgs
			if len(req.Args) > 0 {
				args["args"] = req.Args
			}
		case model.LaunchAttach:
			return nil, fmt.Errorf("attaching is not implemented for node yet; it needs the process to have been started with --inspect")
		default:
			return nil, fmt.Errorf("unknown launch mode %q", req.Mode)
		}
		return args, nil
	},
}

// nodeTestRunner works out how to run the project's tests.
//
// It asks the project rather than guessing a framework: a target naming a runner
// wins, otherwise the package's own test script does. Guessing "jest" in a
// vitest project fails in a way that reads like the debugger being broken.
func nodeTestRunner(req model.LaunchRequest) (string, []string, error) {
	if req.Target != "" && req.Target != "." {
		local := filepath.Join(req.WorkDir, "node_modules", ".bin", req.Target)
		if st, err := os.Stat(local); err == nil && !st.IsDir() {
			return local, nil, nil
		}
		if abs := absolute(req.WorkDir, req.Target); fileExists(abs) {
			return "node", []string{abs}, nil
		}
		return "", nil, fmt.Errorf(
			"could not find %q as a test runner: it is neither node_modules/.bin/%s nor a file in %s",
			req.Target, req.Target, req.WorkDir)
	}

	manifest := filepath.Join(req.WorkDir, "package.json")
	if !fileExists(manifest) {
		return "", nil, fmt.Errorf("no package.json in %s, so there is no test script to run; name a runner as the target", req.WorkDir)
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", nil, fmt.Errorf("npm is not on PATH and the project's test script needs it: %w", err)
	}
	testArgs := []string{"test"}
	if req.TestRun != "" {
		// Everything after -- reaches the runner rather than npm.
		testArgs = append(testArgs, "--", "-t", req.TestRun)
	}
	return npm, testArgs, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// readAnnouncedAddress waits for the adapter to say where it is listening.
//
// Waiting for the line rather than sleeping is what makes the first connection
// race-free; the alternative is a timeout that passes on a fast machine.
func readAnnouncedAddress(out interface{ Read([]byte) (int, error) }, timeout time.Duration) (string, error) {
	type result struct {
		address string
		err     error
	}
	found := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		var seen strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			seen.WriteString(line + "\n")
			if m := jsDebugAnnouncement.FindStringSubmatch(line); m != nil {
				found <- result{address: m[1]}
				return
			}
		}
		found <- result{err: fmt.Errorf("the adapter exited without saying where it was listening. Output:\n%s", seen.String())}
	}()

	select {
	case r := <-found:
		return r.address, r.err
	case <-time.After(timeout):
		return "", fmt.Errorf("the adapter did not report a listening address within %s", timeout)
	}
}
