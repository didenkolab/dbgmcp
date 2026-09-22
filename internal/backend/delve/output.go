package delve

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// maxBufferedChunks bounds what a chatty program can cost in memory. A server a
// debuggee can exhaust is a server a debuggee can take down.
const maxBufferedChunks = 4000

// outputBuffer holds what the debuggee printed, read from the files Delve
// redirects its streams into.
//
// Reading happens on demand rather than in a polling goroutine. A poll loop
// meant that a program which printed and exited within one poll interval looked
// silent to an agent that asked immediately afterwards -- a race that is
// invisible in a slow program and reliable in a fast one. Draining at the
// moment of the question removes it rather than narrowing it.
type outputBuffer struct {
	mu      sync.Mutex
	streams []*streamReader
	chunks  []model.OutputChunk
	nextSeq int
	dropped int
}

type streamReader struct {
	path    string
	stream  string
	file    *os.File
	partial string
	closed  bool
}

func (b *outputBuffer) follow(path, stream string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streams = append(b.streams, &streamReader{path: path, stream: stream})
}

// drain reads whatever has appeared since last time. final flushes a trailing
// line that never got its newline, which is exactly how a panic message or a
// prompt tends to arrive.
func (b *outputBuffer) drain(final bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.streams {
		if s.closed {
			continue
		}
		if s.file == nil {
			f, err := os.Open(s.path)
			if err != nil {
				continue
			}
			s.file = f
		}
		data, err := io.ReadAll(s.file)
		if err == nil && len(data) > 0 {
			s.partial += string(data)
			for {
				idx := strings.IndexByte(s.partial, '\n')
				if idx < 0 {
					break
				}
				b.appendLocked(s.stream, strings.TrimRight(s.partial[:idx], "\r"))
				s.partial = s.partial[idx+1:]
			}
		}
		if final {
			if s.partial != "" {
				b.appendLocked(s.stream, s.partial)
				s.partial = ""
			}
			_ = s.file.Close()
			s.file, s.closed = nil, true
		}
	}
}

func (b *outputBuffer) appendLocked(stream, text string) {
	b.nextSeq++
	b.chunks = append(b.chunks, model.OutputChunk{
		Seq: b.nextSeq, Stream: stream, Text: text, At: time.Now(),
	})
	if over := len(b.chunks) - maxBufferedChunks; over > 0 {
		b.chunks = b.chunks[over:]
		b.dropped += over
	}
}

// append exists for tests that exercise the ring without a real process.
func (b *outputBuffer) append(stream, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.appendLocked(stream, text)
}

// page returns chunks newer than since. Limit zero means the rest.
func (b *outputBuffer) page(since, limit int) model.OutputPage {
	b.drain(false)
	b.mu.Lock()
	defer b.mu.Unlock()

	out := model.OutputPage{NextSince: since, Dropped: b.dropped, Chunks: []model.OutputChunk{}}
	for _, c := range b.chunks {
		if c.Seq <= since {
			continue
		}
		out.Chunks = append(out.Chunks, c)
		out.NextSince = c.Seq
		if limit > 0 && len(out.Chunks) >= limit {
			break
		}
	}
	return out
}

// tail returns the last n chunks: what a pause report wants, the last thing the
// program said before it stopped.
func (b *outputBuffer) tail(n int) []model.OutputChunk {
	b.drain(false)
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || len(b.chunks) == 0 {
		return nil
	}
	start := len(b.chunks) - n
	if start < 0 {
		start = 0
	}
	out := make([]model.OutputChunk, len(b.chunks)-start)
	copy(out, b.chunks[start:])
	return out
}

// close takes a last reading before the files disappear with the session.
func (b *outputBuffer) close() { b.drain(true) }
