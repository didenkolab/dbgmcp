package backend

import (
	"errors"
	"strings"
	"testing"
)

func TestUnsupportedNamesTheCapabilityAndTheAlternative(t *testing.T) {
	// An agent that is told the alternative retries usefully; one that gets
	// "operation failed" retries blindly. The message is the whole value here.
	err := Unsupported("dap", "watchpoints", "Set a line breakpoint at each write site instead.")

	msg := err.Error()
	for _, want := range []string{"dap", "watchpoints", "line breakpoint"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q omits %q", msg, want)
		}
	}

	var ue *UnsupportedError
	if !errors.As(err, &ue) || ue.Capability != "watchpoints" {
		t.Fatalf("callers cannot branch on the capability: %#v", err)
	}
}
