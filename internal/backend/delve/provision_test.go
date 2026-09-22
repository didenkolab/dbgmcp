package delve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDlv writes a script that answers `version` however the test wants, so the
// version guard can be exercised without installing several Delves.
func fakeDlv(t *testing.T, versionLine string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dlv")
	script := "#!/bin/sh\necho 'Delve Debugger'\necho '" + versionLine + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func resetProbeCache() {
	probeMu.Lock()
	probeCache = map[string]Info{}
	probeMu.Unlock()
}

func TestResolveRejectsADelveFromADifferentApiSeries(t *testing.T) {
	// The alternative to catching this here is discovering it as a malformed
	// RPC reply somewhere in the middle of a debugging session.
	resetProbeCache()
	t.Setenv("DBGMCP_DLV", fakeDlv(t, "Version: 1.21.0"))

	_, err := Resolve()
	if err == nil {
		t.Fatal("a Delve from another API series must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"1.21.0", "1.27", "go install"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
}

func TestResolveAcceptsAPatchReleaseOfTheSameSeries(t *testing.T) {
	// Patch releases keep the RPC API, and refusing them would make the pin a
	// nuisance rather than a safeguard.
	resetProbeCache()
	t.Setenv("DBGMCP_DLV", fakeDlv(t, "Version: 1.27.9"))

	info, err := Resolve()
	if err != nil {
		t.Fatalf("a patch release of the required series must be accepted: %v", err)
	}
	if info.Version != "1.27.9" {
		t.Errorf("version not read back: %+v", info)
	}
	if info.SupportedGo == "" {
		t.Error("the supported Go range should be reported, so a refusal later is not a surprise")
	}
}

func TestResolveExplainsAnUnreadableVersion(t *testing.T) {
	resetProbeCache()
	dir := t.TempDir()
	path := filepath.Join(dir, "dlv")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho nonsense\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBGMCP_DLV", path)

	if _, err := Resolve(); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected a clear complaint about the version, got %v", err)
	}
}

func TestNotInstalledErrorCarriesTheInstallCommand(t *testing.T) {
	// An agent that reads this can fix the problem itself; one that gets
	// "exec: dlv: not found" cannot.
	err := &NotInstalledError{Searched: []string{"PATH"}}
	if !strings.Contains(err.Error(), "go install github.com/go-delve/delve/cmd/dlv@"+RequiredVersion) {
		t.Errorf("no install command in: %s", err)
	}
}
