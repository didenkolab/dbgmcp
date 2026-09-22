package discover

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "buggy")
}

func TestGoFindsBothTheMainPackageAndItsTests(t *testing.T) {
	targets, err := Go(context.Background(), fixtureDir(t))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	var debug, test *Target
	for i := range targets {
		switch targets[i].Mode {
		case "debug":
			debug = &targets[i]
		case "test":
			test = &targets[i]
		}
	}
	if debug == nil {
		t.Error("the fixture's main package was not offered as a debug target")
	}
	if test == nil {
		t.Fatal("the fixture's tests were not offered as a test target")
	}
	// The test names matter more than the package: an agent should be able to
	// run the one failing test, not the whole package.
	var found bool
	for _, name := range test.Tests {
		if name == "TestSubtotal" {
			found = true
		}
	}
	if !found {
		t.Errorf("TestSubtotal was not listed; got %v", test.Tests)
	}
}

func TestGoReportsAFailureVerbatim(t *testing.T) {
	// A directory with no module at all: the agent needs to hear what go list
	// actually said, not a paraphrase.
	_, err := Go(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a directory that is not a Go module")
	}
	if len(err.Error()) < 20 {
		t.Errorf("the failure was flattened into %q", err)
	}
}
