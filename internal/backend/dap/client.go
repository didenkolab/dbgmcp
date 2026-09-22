package dap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// StoppedEvent is what the adapter reports when the debuggee halts.
type StoppedEvent struct {
	Reason            string `json:"reason"`
	ThreadID          int    `json:"threadId"`
	Description       string `json:"description"`
	Text              string `json:"text"`
	HitBreakpointIDs  []int  `json:"hitBreakpointIds"`
	AllThreadsStopped bool   `json:"allThreadsStopped"`
}

// OutputEvent carries what the debuggee printed. DAP delivers it as events
// rather than a stream, which is why an attached adapter can report output that
// a raw pipe would not see.
type OutputEvent struct {
	Category string `json:"category"`
	Output   string `json:"output"`
}

// client correlates requests with responses and routes events.
//
// DAP multiplexes both directions over one pipe, and responses may arrive out
// of order or interleaved with events, so every request waits on its own
// channel keyed by sequence number rather than on "the next frame".
type client struct {
	conn *conn

	mu      sync.Mutex
	seq     int
	pending map[int]chan Message
	closed  bool

	stopped    chan StoppedEvent
	terminated chan struct{}
	initEvent  chan struct{}

	outputMu sync.Mutex
	output   []OutputEvent

	readErr error
	done    chan struct{}
}

func newClient(c *conn) *client {
	cl := &client{
		conn:       c,
		pending:    map[int]chan Message{},
		stopped:    make(chan StoppedEvent, 64),
		terminated: make(chan struct{}),
		initEvent:  make(chan struct{}),
		done:       make(chan struct{}),
	}
	go cl.readLoop()
	return cl
}

func (c *client) readLoop() {
	defer close(c.done)
	terminatedOnce, initOnce := sync.Once{}, sync.Once{}
	for {
		m, err := c.conn.read()
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			waiters := c.pending
			c.pending = map[int]chan Message{}
			c.closed = true
			c.mu.Unlock()
			// Wake everything still waiting; a silent hang is the worst failure
			// mode for an agent, which has no way to tell it apart from slow.
			for _, ch := range waiters {
				close(ch)
			}
			terminatedOnce.Do(func() { close(c.terminated) })
			return
		}

		switch m.Type {
		case "response":
			c.mu.Lock()
			ch, waiting := c.pending[m.RequestSeq]
			delete(c.pending, m.RequestSeq)
			c.mu.Unlock()
			if waiting {
				ch <- m
				close(ch)
			}
		case "event":
			switch m.Event {
			case "initialized":
				initOnce.Do(func() { close(c.initEvent) })
			case "stopped":
				var ev StoppedEvent
				if err := json.Unmarshal(m.Body, &ev); err == nil {
					select {
					case c.stopped <- ev:
					default: // a full buffer means nobody is waiting; dropping is correct
					}
				}
			case "terminated", "exited":
				terminatedOnce.Do(func() { close(c.terminated) })
			case "output":
				var ev OutputEvent
				if err := json.Unmarshal(m.Body, &ev); err == nil {
					c.outputMu.Lock()
					c.output = append(c.output, ev)
					c.outputMu.Unlock()
				}
			}
		}
	}
}

// sendAsync issues a request and returns a channel carrying its response.
//
// This exists for exactly one reason, and it is the sequencing rule that catches
// every DAP client out: an adapter may defer the response to "launch" until
// after "configurationDone", and "configurationDone" may not be sent until the
// "initialized" event arrives. Waiting for the launch response before sending
// configurationDone is a deadlock with each side correctly following the spec.
func (c *client) sendAsync(command string, args any) (<-chan Message, error) {
	var raw json.RawMessage
	if args != nil {
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("the debug adapter has exited")
	}
	c.seq++
	seq := c.seq
	reply := make(chan Message, 1)
	c.pending[seq] = reply
	c.mu.Unlock()

	if err := c.conn.write(Message{Seq: seq, Type: "request", Command: command, Arguments: raw}); err != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, fmt.Errorf("could not send %s: %w", command, err)
	}
	return reply, nil
}

// awaitReply blocks on a channel from sendAsync.
func awaitReply(ctx context.Context, command string, reply <-chan Message, timeout time.Duration) (json.RawMessage, error) {
	select {
	case m, open := <-reply:
		if !open {
			return nil, fmt.Errorf("the debug adapter exited while handling %s", command)
		}
		if m.Success == nil || !*m.Success {
			return nil, &RequestError{Command: command, Reason: m.Message}
		}
		return m.Body, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("the debug adapter did not answer %s within %s", command, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// send issues a request and waits for its response.
func (c *client) send(ctx context.Context, command string, args any, timeout time.Duration) (json.RawMessage, error) {
	var raw json.RawMessage
	if args != nil {
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}

	c.mu.Lock()
	if c.closed {
		err := c.readErr
		c.mu.Unlock()
		if err == nil || err == io.EOF {
			return nil, fmt.Errorf("the debug adapter has exited")
		}
		return nil, fmt.Errorf("the debug adapter is gone: %w", err)
	}
	c.seq++
	seq := c.seq
	reply := make(chan Message, 1)
	c.pending[seq] = reply
	c.mu.Unlock()

	if err := c.conn.write(Message{Seq: seq, Type: "request", Command: command, Arguments: raw}); err != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, fmt.Errorf("could not send %s: %w", command, err)
	}

	select {
	case m, open := <-reply:
		if !open {
			return nil, fmt.Errorf("the debug adapter exited while handling %s", command)
		}
		if m.Success == nil || !*m.Success {
			return nil, &RequestError{Command: command, Reason: m.Message}
		}
		return m.Body, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, fmt.Errorf("the debug adapter did not answer %s within %s", command, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// drainOutput returns what the debuggee has printed and clears the buffer.
func (c *client) drainOutput() []OutputEvent {
	c.outputMu.Lock()
	defer c.outputMu.Unlock()
	out := c.output
	c.output = nil
	return out
}

func (c *client) close() { _ = c.conn.close() }
