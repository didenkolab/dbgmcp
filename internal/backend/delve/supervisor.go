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
}

// launchArgs turns a neutral LaunchRequest into a dlv command line.
//
// dlv debug and dlv test compile with the optimiser and inliner off by default,
// which is why the agent can see local variables at all; dlv exec takes the
// binary as given, so the caller is told the difference.
func launchArgs(mode model.LaunchMode, target, socket, buildOutput string, args []string, testRun string) ([]string, bool, error) {
	var sub string
	switch mode {
	case model.LaunchTest:
		sub = "test"
	case model.LaunchDebug:
		sub = "debug"
	case model.LaunchExec:
		sub = "exec"
	default:
		return nil, false, fmt.Errorf("unknown launch mode %q (expected test, debug or exec)", mode)
	}

	out := []string{sub, "--headless", "--api-version=2", "--accept-multiclient",
		"--log-dest=2", "--listen=unix:" + socket}
	// Send the compiled binary into our own temporary directory. By default
	// Delve builds a __debug_bin<random> file in the working directory and
	// deletes it on exit -- but this server kills the process group, so Delve
	// never gets to. Owning the path means the litter disappears with the
	// directory we already remove, instead of accumulating in the user's repo.
	if buildOutput != "" && mode != model.LaunchExec {
		out = append(out, "--output="+buildOutput)
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
	return out, mode != model.LaunchExec, nil
}

func (s *supervisor) start(ctx context.Context, dlvPath string, req model.LaunchRequest) (bool, error) {
	tmpDir, err := os.MkdirTemp("", "dbgmcp-")
	if err != nil {
		return false, fmt.Errorf("could not create a socket directory: %w", err)
	}
	socket := filepath.Join(tmpDir, "dlv.sock")
	buildOutput := filepath.Join(tmpDir, "debug.bin")

	args, optimisationsDisabled, err := launchArgs(req.Mode, req.Target, socket, buildOutput, req.Args, req.TestRun)
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

	s.mu.Lock()
	s.cmd, s.tmpDir, s.stderr = cmd, tmpDir, &strings.Builder{}
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
	s.client = rpc2.NewClientFromConn(conn)
	s.mu.Unlock()

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
	s.client, s.conn = nil, nil
	s.mu.Unlock()

	// Ask politely first: Detach(true) lets Delve tear the debuggee down the way
	// it knows how. Its failure is not interesting -- the group kill follows.
	if client != nil {
		done := make(chan struct{})
		go func() { defer func() { recover(); close(done) }(); _ = client.Detach(true) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	if conn != nil {
		_ = conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		killProcessGroup(cmd.Process.Pid)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
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
