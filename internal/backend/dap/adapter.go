package dap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// Adapter is one runtime's debug adapter: how to start it, and how to phrase a
// launch for it.
//
// The differences between adapters are entirely in these two functions. Keeping
// them declarative is what makes "add a runtime" a profile rather than a
// project, which is the whole reason for choosing DAP over three native APIs.
// dialer opens another connection to an adapter already running. Nil for
// adapters that serve one session per process; js-debug needs it because its
// child sessions share the port the parent announced.
type dialer func() (*conn, error)

type Adapter struct {
	// Language is the name an agent uses to ask for this runtime.
	Language string
	// Install is the command a user runs when the toolchain is missing.
	Install string

	// start launches the adapter and hands back a connection to it.
	//
	// Each profile knows how to reach itself, rather than the backend switching
	// on a transport enum: debugpy speaks over the pipes of the process we
	// start, js-debug listens on a TCP port and announces it. Those are not two
	// settings of one mechanism, they are two mechanisms.
	start func() (*exec.Cmd, *conn, dialer, error)
	// launchArgs builds the DAP launch request body, which is adapter-specific
	// by design: the protocol deliberately leaves it open.
	launchArgs func(req model.LaunchRequest) (map[string]any, error)

	// symbolBreakpointsUsable overrides the adapter's own claim.
	//
	// debugpy declares supportsFunctionBreakpoints and does bind them, but
	// reports no file or line back, so an agent cannot know where it will stop.
	// A capability that technically works and cannot be planned against is not
	// one worth declaring, and the honest place to say so is here, per runtime,
	// rather than in the protocol-generic code.
	symbolBreakpointsUsable bool
}

// Adapters is the registry. Ruby is present although it is being retired,
// because rdbg speaks DAP and supporting it costs one entry here.
// Adapters is the registry of runtimes this server will actually accept.
//
// node is deliberately absent. Its profile is written and most of it works --
// launch, stepping, hit counts, breakpoints that bind -- but a stop does not
// reliably land in the frame the breakpoint named, so evaluation and
// set_variable fail against names that are genuinely not in the frame reached.
// Registering it would mean declaring set_variable and eval_calls_functions as
// supported while they do not work, which is the exact dishonesty the
// conformance suite exists to catch. It goes in when the suite is green, not
// before.
var Adapters = map[string]*Adapter{
	"python": python,
}

// developmentAdapters are profiles under construction. They are reachable only
// from their own tests, so the work stays visible and runnable without being
// offered to an agent as if it were finished.
var developmentAdapters = map[string]*Adapter{
	"node":       node,
	"javascript": node,
	"typescript": node,
}

func Lookup(language string) (*Adapter, error) {
	a, found := Adapters[strings.ToLower(language)]
	if !found {
		if _, underway := developmentAdapters[strings.ToLower(language)]; underway {
			return nil, fmt.Errorf(
				"%s is not supported yet: the adapter profile exists but a stop does not reliably land in the frame the breakpoint named, so evaluation in that frame fails. It will be offered when its conformance suite is green",
				language)
		}
		known := make([]string, 0, len(Adapters))
		for name := range Adapters {
			known = append(known, name)
		}
		return nil, fmt.Errorf("no debug adapter for %q; this server has: %s", language, strings.Join(known, ", "))
	}
	return a, nil
}

// pythonExe resolves the interpreter to debug with. DBGMCP_PYTHON exists because
// the interpreter that matters is the project's, not whichever one happens to be
// first on PATH -- a virtualenv is the normal case, not the exception.
func pythonExe() string {
	if p := os.Getenv("DBGMCP_PYTHON"); p != "" {
		return p
	}
	if p, err := exec.LookPath("python3"); err == nil {
		return p
	}
	return "python"
}

var python = &Adapter{
	Language: "python",
	Install:  "python3 -m pip install debugpy",

	// debugpy binds function breakpoints but does not report their location.
	symbolBreakpointsUsable: false,

	start: func() (*exec.Cmd, *conn, dialer, error) {
		exe := pythonExe()
		// debugpy ships its own DAP adapter; running it as a module keeps it
		// bound to the interpreter that will do the debugging, which is the
		// pairing that actually has to match.
		check := exec.Command(exe, "-c", "import debugpy")
		if out, err := check.CombinedOutput(); err != nil {
			return nil, nil, nil, fmt.Errorf(
				"debugpy is not installed for %s. Install it with: %s -m pip install debugpy\n%s",
				exe, exe, strings.TrimSpace(string(out)))
		}
		cmd := exec.Command(exe, "-m", "debugpy.adapter")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, nil, fmt.Errorf("could not start the python debug adapter: %w", err)
		}
		// debugpy serves one session per adapter process, so there is nothing
		// to dial a second time.
		return cmd, newConn(stdin, stdout), nil, nil
	},

	launchArgs: func(req model.LaunchRequest) (map[string]any, error) {
		args := map[string]any{
			"request": "launch",
			"type":    "python",
			"python":  pythonExe(),
			"cwd":     req.WorkDir,
			// Stop before the first statement so breakpoints can be set while
			// nothing has run -- the same contract as the Delve backend.
			"stopOnEntry": true,
			// Without this, debugpy hides the user's own frames behind library
			// ones and an agent sees a stack it cannot act on.
			"justMyCode": false,
			"console":    "internalConsole",
		}
		if len(req.Env) > 0 {
			args["env"] = req.Env
		}

		switch req.Mode {
		case model.LaunchDebug, model.LaunchExec:
			if req.Target == "" {
				return nil, fmt.Errorf("mode %q needs a target: the path of the script to run", req.Mode)
			}
			args["program"] = absolute(req.WorkDir, req.Target)
			// Only when there are some: an empty Go slice marshals to null,
			// and debugpy rejects the whole launch over it.
			if len(req.Args) > 0 {
				args["args"] = req.Args
			}
		case model.LaunchTest:
			// pytest is a module, not a script, so the target names the tests
			// rather than a file to execute.
			args["module"] = "pytest"
			testArgs := []string{}
			if req.Target != "" && req.Target != "." {
				testArgs = append(testArgs, req.Target)
			}
			if req.TestRun != "" {
				testArgs = append(testArgs, "-k", req.TestRun)
			}
			testArgs = append(testArgs, req.Args...)
			if len(testArgs) > 0 {
				args["args"] = testArgs
			}
		case model.LaunchAttach:
			return nil, fmt.Errorf("attaching is not implemented for python yet; it needs the debuggee to have started debugpy itself")
		default:
			return nil, fmt.Errorf("unknown launch mode %q", req.Mode)
		}
		return args, nil
	},
}

func absolute(workDir, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(workDir, target)
}

// NewDevelopmentAdapter reaches a profile that is still being built. It exists
// for that profile's own tests; nothing an agent can call goes through it.
func NewDevelopmentAdapter(language string) (*Adapter, error) {
	a, found := developmentAdapters[strings.ToLower(language)]
	if !found {
		return nil, fmt.Errorf("no adapter under construction for %q", language)
	}
	return a, nil
}
