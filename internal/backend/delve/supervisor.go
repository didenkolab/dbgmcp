package delve

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/go-delve/delve/service/rpc2"
)

// startupTimeout covers compiling the target, which for a cold cache is the
// slowest thing this server ever waits on.
const startupTimeout = 120 * time.Second

// listeningPrefix is what dlv prints -- on stderr, not stdout -- once its API
// is up. Waiting for it is what makes the first RPC call race-free.
const listeningPrefix = "API server listening at:"

// supervisor owns exactly one dlv process and the connection to it.
//
// Ownership is the point. The server, not the operating system and not the
// user, is responsible for making sure no dlv and no debuggee outlives the
// session that asked for it.
type supervisor struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	client  *rpc2.RPCClient
	conn    net.Conn
	tmpDir  string
	stderr  *strings.Builder
	stopped bool

	// output holds what the debuggee printed. It outlives the process on
	// purpose: the most useful moment to read a program's last words is after
	// it has died.
	output outputBuffer

	// attached records that the debuggee belongs to somebody else. Every
	// teardown decision reads it, because getting this wrong means killing a
	// service the agent was only supposed to look at.
	attached bool
}

// launchArgs turns a neutral LaunchRequest into a dlv command line.
//
// dlv debug and dlv test compile with the optimiser and inliner off by default,
// which is why the agent can see local variables at all; dlv exec takes the
// binary as given, so the caller is told the difference.
// redirectPaths names the two files Delve is told to send the debuggee's
// streams into. They are separate files rather than Delve's own stdout because
// the debuggee's stderr would otherwise be interleaved with Delve's log lines,
// and an agent cannot unpick that afterwards.
type redirectPaths struct{ stdout, stderr string }

func launchArgs(mode model.LaunchMode, target, socket, buildOutput string, redirects redirectPaths, args []string, testRun string) ([]string, bool, error) {
	var sub string
	switch mode {
	case model.LaunchTest:
		sub = "test"
	case model.LaunchDebug:
		sub = "debug"
	case model.LaunchExec:
		sub = "exec"
	case model.LaunchAttach:
		sub = "attach"
	default:
		return nil, false, fmt.Errorf("unknown launch mode %q (expected test, debug, exec or attach)", mode)
	}

	out := []string{sub, "--headless", "--api-version=2", "--accept-multiclient",
		"--log-dest=2", "--listen=unix:" + socket}
	// Send the compiled binary into our own temporary directory. By default
	// Delve builds a __debug_bin<random> file in the working directory and
	// deletes it on exit -- but this server kills the process group, so Delve
	// never gets to. Owning the path means the litter disappears with the
	// directory we already remove, instead of accumulating in the user's repo.
	// Only the modes that actually compile something. exec takes the binary as
	// given, and attach does not have one to build.
	if buildOutput != "" && (mode == model.LaunchTest || mode == model.LaunchDebug) {
		out = append(out, "--output="+buildOutput)
	}
	// An attached process already owns its streams; they go wherever they were
	// going before the debugger arrived, and redirecting them is not ours to do.
	if !mode.IsAttach() {
		if redirects.stdout != "" {
			out = append(out, "-r", "stdout:"+redirects.stdout)
		}
		if redirects.stderr != "" {
			out = append(out, "-r", "stderr:"+redirects.stderr)
		}
	}
	if target != "" {
		out = append(out, target)
	}

	passthrough := args
	if mode == model.LaunchTest && testRun != "" {
		passthrough = append([]string{"-test.run", testRun}, passthrough...)
	}
	if len(passthrough) > 0 {
		out = append(out, "--")
		out = append(out, passthrough...)
	}
	// Delve rebuilds with the optimiser and inliner off for the modes where it
	// does the building. exec and attach get whatever the binary already is.
	compiledByDelve := mode == model.LaunchTest || mode == model.LaunchDebug
	return out, compiledByDelve, nil
}

func (s *supervisor) start(ctx context.Context, dlvPath string, req model.LaunchRequest) (bool, error) {
	tmpDir, err := os.MkdirTemp("", "dbgmcp-")
	if err != nil {
		return false, fmt.Errorf("could not create a socket directory: %w", err)
	}
	socket := filepath.Join(tmpDir, "dlv.sock")
	buildOutput := filepath.Join(tmpDir, "debug.bin")
	redirects := redirectPaths{
		stdout: filepath.Join(tmpDir, "stdout"),
		stderr: filepath.Join(tmpDir, "stderr"),
	}
	// Create them up front so the tailers have something to open immediately
	// rather than racing the debuggee's first write.
	for _, path := range []string{redirects.stdout, redirects.stderr} {
		if f, createErr := os.Create(path); createErr == nil {
			_ = f.Close()
		}
	}

	target := req.Target
	if req.Mode.IsAttach() {
		target = strconv.Itoa(req.PID)
	}
	args, optimisationsDisabled, err := launchArgs(req.Mode, target, socket, buildOutput, redirects, req.Args, req.TestRun)
	if err != nil {
		os.RemoveAll(tmpDir)
		return false, err
	}

	cmd := exec.Command(dlvPath, args...)
	cmd.Dir = req.WorkDir
	cmd.Env = buildEnv(req.Env)
	isolateProcessGroup(cmd)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		os.RemoveAll(tmpDir)
		return false, fmt.Errorf("could not capture Delve's output: %w", err)
	}

	if err := cmd.Start(); err != nil {
		os.RemoveAll(tmpDir)
		return false, fmt.Errorf("could not start Delve (%s): %w", dlvPath, err)
	}

	s.output.follow(redirects.stdout, model.StreamStdout)
	s.output.follow(redirects.stderr, model.StreamStderr)

	s.mu.Lock()
	s.cmd, s.tmpDir, s.stderr = cmd, tmpDir, &strings.Builder{}
	s.attached = req.Mode.IsAttach()
	s.mu.Unlock()

	ready := s.scanForReady(stderrPipe)

	select {
	case err := <-ready:
		if err != nil {
			s.kill()
			return false, err
		}
	case <-time.After(startupTimeout):
		s.kill()
		return false, fmt.Errorf("Delve did not report a listening API within %s. Captured output:\n%s",
			startupTimeout, s.capturedStderr())
	case <-ctx.Done():
		s.kill()
		return false, ctx.Err()
	}

	conn, err := net.DialTimeout("unix", socket, 10*time.Second)
	if err != nil {
		s.kill()
		return false, fmt.Errorf("Delve announced its API but the socket could not be reached: %w", err)
	}

	s.mu.Lock()
	s.conn = conn
	// NewClientFromConn rather than NewClient: rpc2.NewClient dials TCP only and
	// calls log.Fatal on failure, which would take this whole server down.
	client := rpc2.NewClientFromConn(conn)
	s.client = client
	s.mu.Unlock()

	// Delve announces its API before it has finished taking hold of the target,
	// so "listening" is not "working": attaching to a pid that does not exist
	// gets this far and only then fails. Without this check the agent is handed
	// a session it can set breakpoints into and never hear from again.
	if _, err := client.GetState(); err != nil {
		captured := s.capturedStderr()
		s.kill()
		return false, fmt.Errorf("Delve started but could not take control of the target: %w\n%s", err, captured)
	}

	return optimisationsDisabled, nil
}

// scanForReady consumes Delve's stderr, signalling as soon as the API is up and
// retaining the rest so a failure can be explained instead of merely reported.
func (s *supervisor) scanForReady(pipe io.ReadCloser) <-chan error {
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		announced := false
		for scanner.Scan() {
			line := scanner.Text()
			s.mu.Lock()
			if s.stderr != nil {
				s.stderr.WriteString(line + "\n")
			}
			s.mu.Unlock()
			if !announced && strings.Contains(line, listeningPrefix) {
				announced = true
				ready <- nil
			}
		}
		if !announced {
			ready <- fmt.Errorf("Delve exited before its API came up. Output:\n%s", s.capturedStderr())
		}
	}()
	return ready
}

func (s *supervisor) capturedStderr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stderr == nil {
		return ""
	}
	return s.stderr.String()
}

func (s *supervisor) rpc() (*rpc2.RPCClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, fmt.Errorf("no debug session is running")
	}
	return s.client, nil
}

// kill is idempotent, because Stop can legitimately be called after the
// debuggee has already exited on its own.
func (s *supervisor) kill() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	cmd, conn, client, tmpDir := s.cmd, s.conn, s.client, s.tmpDir
	attached := s.attached
	s.client, s.conn = nil, nil
	s.mu.Unlock()

	// Detach(kill) is the whole safety question. For a process we started, true
	// tears it down the way Delve knows how. For one we attached to, killing it
	// would take down somebody else's running service because an agent finished
	// looking at it, so the flag is the inverse of "attached" and never a
	// constant.
	if client != nil {
		killDebuggee := !attached
		done := make(chan struct{})
		go func() { defer func() { recover(); close(done) }(); _ = client.Detach(killDebuggee) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	if conn != nil {
		_ = conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		// Only the process group we created. When attached, the debuggee is not
		// in it -- and a group kill here would be the bug this guards against.
		if !attached {
			killProcessGroup(cmd.Process.Pid)
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	// Take a last reading before the files go with the directory.
	s.output.close()
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
	}
}

// buildEnv starts from the server's environment because a Go build needs it
// (GOPATH, GOCACHE, HOME), then applies the caller's overrides.
func buildEnv(overrides map[string]string) []string {
	env := os.Environ()
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}
