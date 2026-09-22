package conformance

import (
	"os"
	"strings"
	"testing"
)

// LineContaining finds a line by what is on it, so a fixture edit that shifts
// line numbers does not silently point the suite at the wrong place.
func LineContaining(t *testing.T, file, needle string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("%s no longer contains %q", file, needle)
	return 0
}
