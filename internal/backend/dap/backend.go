package dap

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
)

const requestTimeout = 60 * time.Second

// Backend drives one debug adapter. One of these covers Python, and the same
// code covers JavaScript and Ruby once their adapter profiles are added -- which
// is the point of speaking DAP rather than three native protocols.
type Backend struct {
	adapter *Adapter

	mu     sync.Mutex
	cmd    *exec.Cmd
	client *client
	caps   initializeResponse

	// breakpoints is kept per file because DAP's setBreakpoints *replaces* every
	// breakpoint in a source, rather than adding one. Without our own copy,
	// setting a second breakpoint in a file would silently delete the first.
	breakpoints map[string][]trackedBreakpoint
	nextLocalID int

	currentThread int
	currentFrame  int
	// frameIDs maps our frame index to the adapter's opaque ids, which are
	// only valid until the next resume.
	frameIDs  []int
	exited    bool
	pathMap   model.PathMapping
	outputSeq int
	outputBuf []model.OutputChunk
}

type trackedBreakpoint struct {
	localID  int
	remoteID int
	spec     sourceBreakpoint
	verified bool
	record   []string
}

func New(language string) (*Backend, error) {
	a, err := Lookup(language)
	if err != nil {
		return nil, err
	}
	return &Backend{adapter: a, breakpoints: map[string][]trackedBreakpoint{}}, nil
}

func (b *Backend) Name() string { return "dap/" + b.adapter.Language }

// Capabilities are read from the adapter's own initialize response rather than
// hardcoded per language.
//
// Adapters disagree about what they support, and the same adapter changes
// between versions. Declaring from a table would make this model wrong exactly
// where it is most trusted, so the answer comes from the adapter that will have
// to honour it.
func (b *Backend) Capabilities() backend.Capabilities {
	b.mu.Lock()
	caps := b.caps
	b.mu.Unlock()

	c := backend.Capabilities{
		Watchpoints: backend.SupportNone,
		HitCounts:   backend.HitCountsNone,
		SetVariable: backend.SupportNone,
		// Both halves must hold: the adapter has to support it, and it has to
		// report where the breakpoint landed.
		BreakpointBySymbol: caps.SupportsFunctionBreakpoints && b.adapter.symbolBreakpointsUsable,
		// DAP has no notion of who created a thread.
		Ancestry:        false,
		BreakpointKinds: []string{string(model.BreakLine)},
		// A logpoint reports and carries on, so the agent is not in the loop --
		// but the debuggee is still interrupted at each hit, which is the same
		// deal Delve gives without eBPF.
		TraceMode: backend.TraceSuspendOnly,
		// Python and JavaScript both evaluate arbitrary expressions, function
		// calls included. That is not a limitation to report but a hazard.
		EvalCallsFunctions: backend.SupportFull,
	}
	if caps.SupportsLogPoints {
		c.TraceMode = backend.TraceAutoContinue
	}
	if caps.SupportsSetVariable {
		c.SetVariable = backend.SupportFull
	}
	if caps.SupportsDataBreakpoints {
		c.Watchpoints = backend.SupportFull
		c.BreakpointKinds = append(c.BreakpointKinds, string(model.BreakWatch))
	}
	return c
}

func (b *Backend) Launch(ctx context.Context, req model.LaunchRequest) error {
	launchArgs, err := b.adapter.launchArgs(req)
	if err != nil {
		return err
	}
	cmd, err := b.adapter.command()
	if err != nil {
		return err
	}
	cmd.Dir = req.WorkDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start the %s debug adapter: %w", b.adapter.Language, err)
	}

	cl := newClient(newConn(stdin, stdout))
	b.mu.Lock()
	b.cmd, b.client = cmd, cl
	b.mu.Unlock()

	body, err := cl.send(ctx, "initialize", map[string]any{
		"clientID": "dbgmcp", "adapterID": b.adapter.Language,
		"linesStartAt1": true, "columnsStartAt1": true,
		"pathFormat": "path", "supportsVariableType": true,
	}, requestTimeout)
	if err != nil {
		b.kill()
		return fmt.Errorf("the %s adapter refused to initialise: %w", b.adapter.Language, err)
	}
	var caps initializeResponse
	_ = json.Unmarshal(body, &caps)
	b.mu.Lock()
	b.caps = caps
	b.mu.Unlock()

	// Sent, not awaited. See sendAsync: the response to launch may not arrive
	// until after configurationDone, which cannot be sent until the initialized
	// event, which does not arrive until launch has been sent.
	launchReply, err := cl.sendAsync("launch", launchArgs)
	if err != nil {
		b.kill()
		return fmt.Errorf("could not launch under the %s adapter: %w", b.adapter.Language, err)
	}

	// The adapter signals readiness for configuration with an event, not a
	// response, and configurationDone before it is a protocol error.
	select {
	case <-cl.initEvent:
	case <-cl.terminated:
		b.kill()
		return fmt.Errorf("the %s adapter exited before it was ready to configure", b.adapter.Language)
	case <-time.After(requestTimeout):
		b.kill()
		return fmt.Errorf("the %s adapter never reported that it was ready to configure", b.adapter.Language)
	case <-ctx.Done():
		b.kill()
		return ctx.Err()
	}

	if caps.SupportsConfigurationDoneRequest {
		if _, err := cl.send(ctx, "configurationDone", map[string]any{}, requestTimeout); err != nil {
			b.kill()
			return err
		}
	}

	// Only now can the launch have completed.
	if _, err := awaitReply(ctx, "launch", launchReply, 3*time.Minute); err != nil {
		b.kill()
		return fmt.Errorf("could not launch under the %s adapter: %w", b.adapter.Language, err)
	}

	// stopOnEntry means the first stop is the entry point. Consuming it here is
	// what makes "the target is stopped, set your breakpoints" true on return.
	select {
	case ev := <-cl.stopped:
		b.mu.Lock()
		b.currentThread = ev.ThreadID
		b.mu.Unlock()
	case <-cl.terminated:
		b.mu.Lock()
		b.exited = true
		b.mu.Unlock()
	case <-time.After(requestTimeout):
		// Some adapters do not honour stopOnEntry; the session is still usable.
	}
	return nil
}

func (b *Backend) Stop(ctx context.Context) error {
	b.mu.Lock()
	cl := b.client
	b.mu.Unlock()
	if cl != nil {
		// terminateDebuggee is true because this backend only ever launches;
		// when attach arrives it must become the inverse of "attached", exactly
		// as it is in the Delve backend.
		ctxShort, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = cl.send(ctxShort, "disconnect", map[string]any{"terminateDebuggee": true}, 5*time.Second)
		cancel()
	}
	b.kill()
	return nil
}

func (b *Backend) kill() {
	b.mu.Lock()
	cmd, cl := b.cmd, b.client
	b.cmd, b.client = nil, nil
	b.mu.Unlock()
	if cl != nil {
		cl.close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

func (b *Backend) rpc() (*client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		return nil, fmt.Errorf("No active debug session")
	}
	return b.client, nil
}

// syncBreakpoints resends every breakpoint for one file, because DAP's
// setBreakpoints replaces the file's whole set.
func (b *Backend) syncBreakpoints(ctx context.Context, file string) ([]breakpointResult, error) {
	cl, err := b.rpc()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	tracked := append([]trackedBreakpoint(nil), b.breakpoints[file]...)
	b.mu.Unlock()

	specs := make([]sourceBreakpoint, 0, len(tracked))
	for _, t := range tracked {
		specs = append(specs, t.spec)
	}
	body, err := cl.send(ctx, "setBreakpoints", map[string]any{
		"source":      source{Path: file},
		"breakpoints": specs,
	}, requestTimeout)
	if err != nil {
		return nil, err
	}
	var resp setBreakpointsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	b.mu.Lock()
	for i := range b.breakpoints[file] {
		if i < len(resp.Breakpoints) {
			b.breakpoints[file][i].remoteID = resp.Breakpoints[i].ID
			b.breakpoints[file][i].verified = resp.Breakpoints[i].Verified
		}
	}
	b.mu.Unlock()
	return resp.Breakpoints, nil
}

var _ backend.Backend = (*Backend)(nil)
