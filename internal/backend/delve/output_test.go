package delve

import (
	"testing"

	"github.com/didenkolab/dbgmcp/internal/model"
)

func TestBufferPagesFromACursorWithoutRepeating(t *testing.T) {
	// The cursor is what makes tailing cheap: an agent polling output must not
	// pay for the whole history every time.
	var b outputBuffer
	for _, line := range []string{"one", "two", "three"} {
		b.append(model.StreamStdout, line)
	}

	first := b.page(0, 2)
	if len(first.Chunks) != 2 || first.Chunks[0].Text != "one" {
		t.Fatalf("first page: %+v", first.Chunks)
	}
	second := b.page(first.NextSince, 10)
	if len(second.Chunks) != 1 || second.Chunks[0].Text != "three" {
		t.Fatalf("second page repeated or skipped: %+v", second.Chunks)
	}
	if third := b.page(second.NextSince, 10); len(third.Chunks) != 0 {
		t.Errorf("a cursor at the end should yield nothing, got %+v", third.Chunks)
	}
}

func TestBufferReportsWhatItDropped(t *testing.T) {
	// A silent drop would let an agent conclude a program printed nothing when
	// in fact it printed too much.
	var b outputBuffer
	for i := 0; i < maxBufferedChunks+50; i++ {
		b.append(model.StreamStdout, "x")
	}
	page := b.page(0, 10)
	if page.Dropped != 50 {
		t.Errorf("expected 50 dropped, got %d", page.Dropped)
	}
	if len(b.chunks) != maxBufferedChunks {
		t.Errorf("buffer grew past its bound: %d", len(b.chunks))
	}
}

func TestBufferKeepsBothStreamsInOneOrder(t *testing.T) {
	var b outputBuffer
	b.append(model.StreamStdout, "out1")
	b.append(model.StreamStderr, "err1")
	b.append(model.StreamStdout, "out2")

	page := b.page(0, 10)
	if len(page.Chunks) != 3 {
		t.Fatalf("got %d chunks", len(page.Chunks))
	}
	// Interleaving order is the information: it says which came first.
	if page.Chunks[1].Stream != model.StreamStderr || page.Chunks[1].Text != "err1" {
		t.Errorf("streams were not merged in arrival order: %+v", page.Chunks)
	}
}

func TestTailReturnsTheLastLines(t *testing.T) {
	var b outputBuffer
	for _, l := range []string{"a", "b", "c", "d"} {
		b.append(model.StreamStdout, l)
	}
	got := b.tail(2)
	if len(got) != 2 || got[0].Text != "c" || got[1].Text != "d" {
		t.Errorf("tail(2) = %+v", got)
	}
	if b.tail(0) != nil {
		t.Error("tail(0) should be empty")
	}
}
