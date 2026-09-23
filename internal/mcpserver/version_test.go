package mcpserver_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/mcpserver"
)

// The version is written by hand in two places: the constant the server
// announces to every client, and the changelog heading a reader trusts. Nothing
// connected them, so the classic release defect -- tag a new version, ship a
// binary that still reports the old one -- was available and silent.
func TestTheAnnouncedVersionIsTheNewestReleasedOne(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	changelog := filepath.Join(filepath.Dir(thisFile), "..", "..", "CHANGELOG.md")
	data, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatalf("read the changelog: %v", err)
	}

	// The first heading that names a version, skipping an [Unreleased] section:
	// during development the server keeps reporting the last release, which is
	// what it actually is.
	heading := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
	match := heading.FindSubmatch(data)
	if match == nil {
		t.Fatal("the changelog has no released version heading of the form '## [x.y.z]'")
	}
	newest := string(match[1])

	if mcpserver.ServerVersion != newest {
		t.Errorf("the server announces %q but the newest released changelog entry is %q -- "+
			"one of the two was not updated, and clients believe the constant",
			mcpserver.ServerVersion, newest)
	}
}
