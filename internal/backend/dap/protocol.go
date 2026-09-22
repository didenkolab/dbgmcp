// Package dap speaks the Debug Adapter Protocol, the protocol VS Code uses to
// talk to debuggers.
//
// One implementation here covers Python, JavaScript/TypeScript and Ruby,
// because each ships an adapter that speaks it. That breadth is the reason this
// backend exists; the price is that DAP cannot express several things Delve's
// native API can, which the capability model reports rather than hides.
package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Message is one DAP frame. The protocol multiplexes three kinds down one pipe,
// distinguished by Type, so they are decoded into a single shape and sorted
// afterwards.
type Message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Event      string          `json:"event,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    *bool           `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// conn is the wire: length-prefixed JSON, the same framing as LSP.
type conn struct {
	w      io.WriteCloser
	r      *bufio.Reader
	writeM sync.Mutex
}

func newConn(w io.WriteCloser, r io.Reader) *conn {
	return &conn{w: w, r: bufio.NewReaderSize(r, 64*1024)}
}

func (c *conn) write(m Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeM.Lock()
	defer c.writeM.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = c.w.Write(payload)
	return err
}

// read returns the next frame. The header block is terminated by a blank line;
// only Content-Length matters, but the rest must still be consumed.
func (c *conn) read() (Message, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return Message{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if value, found := strings.CutPrefix(line, "Content-Length:"); found {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return Message{}, fmt.Errorf("unreadable Content-Length %q: %w", value, err)
			}
		}
	}
	if length < 0 {
		return Message{}, fmt.Errorf("a DAP frame arrived without a Content-Length header")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(payload, &m); err != nil {
		return Message{}, fmt.Errorf("unreadable DAP frame: %w", err)
	}
	return m, nil
}

func (c *conn) close() error { return c.w.Close() }

// RequestError is an adapter's refusal, carrying its own words. Adapters differ
// enough that paraphrasing them loses the only specific information available.
type RequestError struct {
	Command string
	Reason  string
}

func (e *RequestError) Error() string {
	if e.Reason == "" {
		return e.Command + " was refused by the debug adapter"
	}
	return e.Command + ": " + e.Reason
}
