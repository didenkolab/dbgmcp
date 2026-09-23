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
	// parent is the session that launched, when the adapter answers through a
	// tree. It owns nothing the agent asks about -- breakpoints and stops live
	// on the child -- but it has to stay connected, because closing it takes
	// the adapter down.
	parent *client
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
	// boundLine is where the adapter actually put it, which is not always the
	// line asked for: an adapter may move a breakpoint to the next executable
	// statement, and a stop then lands on a line nobody requested.
	boundLine int
	verified  bool
	record    []string
}

func New(language string) (*Backend, error) {
	a, err := Lookup(language)
	if err != nil {
		return nil, err
	}
	return &Backend{adapter: a, breakpoints: map[string][]trackedBreakpoint{}}, nil
}

// NewUnderConstruction builds a backend on a profile that is not offered yet.
// Only that profile's own tests call it.
func NewUnderConstruction(language string) (*Backend, error) {
	a, err := NewDevelopmentAdapter(language)
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
	// The adapter starts itself, because how to reach it differs per runtime.
	// Its own working directory is not set here: the directory that matters
	// travels inside the launch request, and setting Dir after Start would do
	// nothing while looking like configuration.
	cmd, connection, again, err := b.adapter.start()
	if err != nil {
		return err
	}

	cl := newClient(connection)
	b.mu.Lock()
	b.cmd, b.client = cmd, cl
	b.mu.Unlock()

	caps, err := b.handshake(ctx, cl, launchArgs, "launch")
	if err != nil {
		b.kill()
		return err
	}
	b.mu.Lock()
	b.caps = caps
	b.mu.Unlock()

	// An adapter may answer through a tree rather than a single session: it
	// launches the process and then asks the client to debug it through a
	// second session. Everything the agent asks about lives on that child, so
	// it becomes the active session and the launcher is demoted to lifecycle.
	if again != nil {
		if err := b.adoptChildSession(ctx, cl, again); err != nil {
			b.kill()
			return err
		}
	}

	// stopOnEntry means the first stop is the entry point. Consuming it here is
	// what makes "the target is stopped, set your breakpoints" true on return.
	//
	// Read from the ACTIVE session, which after a child adoption is no longer
	// the one that launched. Waiting on the launcher's channel is a stop that
	// never arrives, and it surfaces as a minute of silence followed by the
	// entry stop being mistaken for the first breakpoint.
	b.mu.Lock()
	active := b.client
	b.mu.Unlock()
	select {
	case ev := <-active.stopped:
		b.mu.Lock()
		b.currentThread = ev.ThreadID
		b.mu.Unlock()
	case <-active.terminated:
		b.mu.Lock()
		b.exited = true
		b.mu.Unlock()
	case <-time.After(15 * time.Second):
		// Some adapters do not honour stopOnEntry; the session is still usable,
		// and waiting a full minute to find that out helps nobody.
	}

	// Drain any further stops that arrive before the agent has resumed
	// anything. js-debug announces the entry stop twice, and the duplicate is
	// otherwise collected by the next wait and read as the first breakpoint --
	// which lands the agent in a frame it never asked for, evaluating names that
	// do not exist there. Nothing can legitimately stop again before a resume,
	// so anything here is the adapter repeating itself.
	for draining := true; draining; {
		select {
		case <-active.stopped:
		case <-time.After(300 * time.Millisecond):
			draining = false
		}
	}
	return nil
}

// handshake runs the initialize/launch/configurationDone dance on one session.
//
// The ordering is the part every DAP client gets wrong once: the response to
// launch may not arrive until after configurationDone, which cannot be sent
// before the initialized event, which does not arrive until launch has been
// sent. Waiting for the launch response first is a deadlock in which both sides
// are following the specification.
func (b *Backend) handshake(ctx context.Context, cl *client, args map[string]any, verb string) (initializeResponse, error) {
	var caps initializeResponse

	body, err := cl.send(ctx, "initialize", map[string]any{
		"clientID": "dbgmcp", "adapterID": b.adapter.Language,
		"linesStartAt1": true, "columnsStartAt1": true,
		"pathFormat": "path", "supportsVariableType": true,
		// Declared because the adapter may ask us to open another session, and
		// an adapter that does not know we can will not offer.
		"supportsStartDebuggingRequest": true,
	}, requestTimeout)
	if err != nil {
		return caps, fmt.Errorf("the %s adapter refused to initialise: %w", b.adapter.Language, err)
	}
	_ = json.Unmarshal(body, &caps)

	reply, err := cl.sendAsync(verb, args)
	if err != nil {
		return caps, fmt.Errorf("could not %s under the %s adapter: %w", verb, b.adapter.Language, err)
	}

	select {
	case <-cl.initEvent:
	case <-cl.terminated:
		return caps, fmt.Errorf("the %s adapter exited before it was ready to configure", b.adapter.Language)
	case <-time.After(requestTimeout):
		return caps, fmt.Errorf("the %s adapter never reported that it was ready to configure", b.adapter.Language)
	case <-ctx.Done():
		return caps, ctx.Err()
	}

	if caps.SupportsConfigurationDoneRequest {
		if _, err := cl.send(ctx, "configurationDone", map[string]any{}, requestTimeout); err != nil {
			return caps, err
		}
	}
	if _, err := awaitReply(ctx, verb, reply, 3*time.Minute); err != nil {
		return caps, fmt.Errorf("could not %s under the %s adapter: %w", verb, b.adapter.Language, err)
	}
	return caps, nil
}

// adoptChildSession answers the adapter's request for a second session and makes
// it the one everything else uses.
func (b *Backend) adoptChildSession(ctx context.Context, parent *client, again dialer) error {
	var request json.RawMessage
	select {
	case request = <-parent.childSession:
	case <-time.After(30 * time.Second):
		// Not every launch produces one -- a target that exits immediately may
		// not -- so this is not fatal on its own.
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

	var ask struct {
		Request       string         `json:"request"`
		Configuration map[string]any `json:"configuration"`
	}
	if err := json.Unmarshal(request, &ask); err != nil {
		return fmt.Errorf("the adapter asked for a child session in a shape this server cannot read: %w", err)
	}
	if ask.Request == "" {
		ask.Request = "attach"
	}

	connection, err := again()
	if err != nil {
		return err
	}
	child := newClient(connection)
	if _, err := b.handshake(ctx, child, ask.Configuration, ask.Request); err != nil {
		child.close()
		return fmt.Errorf("the child session refused to start: %w", err)
	}

	b.mu.Lock()
	b.parent, b.client = parent, child
	b.mu.Unlock()
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
	cmd, cl, parent := b.cmd, b.client, b.parent
	b.cmd, b.client, b.parent = nil, nil, nil
	b.mu.Unlock()
	if cl != nil {
		cl.close()
	}
	// The launcher goes too: it is what holds the adapter open.
	if parent != nil && parent != cl {
		parent.close()
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
			b.breakpoints[file][i].boundLine = resp.Breakpoints[i].Line
		}
	}
	b.mu.Unlock()
	return resp.Breakpoints, nil
}

var _ backend.Backend = (*Backend)(nil)
