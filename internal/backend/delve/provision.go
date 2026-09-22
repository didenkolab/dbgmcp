// Package delve drives a headless Delve process over its native RPC API.
//
// Delve's own API is used rather than its DAP mode because DAP cannot express
// the things that make debugging worth automating: expressions evaluated on
// every hit without a round trip, tracing that does not suspend, per-goroutine
// hit counts, watchpoints and goroutine ancestry.
package delve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// RequiredVersion is pinned so a surprise upgrade cannot silently change
// behaviour underneath a running agent.
const RequiredVersion = "v1.27.2"

// requiredSeries is the major.minor this server compiles its RPC client
// against. Patch releases within it keep the API; a different series is an
// API-mismatch risk, and finding that out through a malformed RPC reply at the
// first breakpoint is the worst available moment.
const requiredSeries = "1.27"

// SupportedGoRange is what this Delve accepts in a target binary. Delve refuses
// a Go version outside it; the refusal is surfaced rather than suppressed with
// --check-go-version=false, because "the debugger does not understand this
// binary" is something the agent has to know.
const SupportedGoRange = "Go 1.25 - 1.27"

// InstallCommand is what a user (or the server) runs to get a usable dlv.
var InstallCommand = []string{"go", "install", "github.com/go-delve/delve/cmd/dlv@" + RequiredVersion}

// NotInstalledError explains, in one message, both what is missing and exactly
// how to fix it. An agent that reads this can resolve the problem itself; one
// that gets "exec: dlv: not found" cannot.
type NotInstalledError struct{ Searched []string }

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("Delve (dlv) was not found in PATH or %v. Install it with: %s",
		e.Searched, "go install github.com/go-delve/delve/cmd/dlv@"+RequiredVersion)
}

// FindDelve locates a dlv binary. GOPATH/bin is searched explicitly because
// `go install` puts it there and that directory is frequently absent from the
// PATH of a GUI-launched process -- which is exactly how an agent's MCP server
// tends to be started.
func FindDelve() (string, error) {
	if p := os.Getenv("DBGMCP_DLV"); p != "" {
		return p, nil
	}
	var searched []string
	if p, err := exec.LookPath("dlv"); err == nil {
		return p, nil
	}
	searched = append(searched, "PATH")

	for _, dir := range gopathBins() {
		candidate := filepath.Join(dir, "dlv"+exeSuffix())
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
		searched = append(searched, dir)
	}
	return "", &NotInstalledError{Searched: searched}
}

// Info is what is actually installed, as opposed to what is required.
type Info struct {
	Path        string `json:"path"`
	Version     string `json:"version"`
	SupportedGo string `json:"supported_go"`
}

// VersionMismatchError is raised before anything is launched. The alternative
// is discovering the mismatch as a malformed RPC reply somewhere in the middle
// of a debugging session.
type VersionMismatchError struct {
	Path, Found, Required string
}

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf(
		"the Delve at %s is version %s, but this server speaks the %s RPC API. "+
			"Install the matching one with: go install github.com/go-delve/delve/cmd/dlv@%s "+
			"(or point DBGMCP_DLV at it)",
		e.Path, e.Found, requiredSeries, RequiredVersion)
}

var (
	probeMu    sync.Mutex
	probeCache = map[string]Info{}
)

// Resolve finds a usable Delve and verifies it, caching the answer because the
// check costs a process launch and the answer cannot change for a given path
// within a session.
func Resolve() (Info, error) {
	path, err := FindDelve()
	if err != nil {
		return Info{}, err
	}
	probeMu.Lock()
	cached, hit := probeCache[path]
	probeMu.Unlock()
	if hit {
		return cached, nil
	}

	version, err := probeVersion(path)
	if err != nil {
		return Info{}, err
	}
	if !strings.HasPrefix(version, requiredSeries+".") && version != requiredSeries {
		return Info{}, &VersionMismatchError{Path: path, Found: version, Required: requiredSeries}
	}

	info := Info{Path: path, Version: version, SupportedGo: SupportedGoRange}
	probeMu.Lock()
	probeCache[path] = info
	probeMu.Unlock()
	return info, nil
}

// probeVersion reads the version out of `dlv version`, whose second line is
// "Version: 1.27.2".
func probeVersion(path string) (string, error) {
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return "", fmt.Errorf("could not run %s version: %w", path, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), "Version:"); found {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("could not read a version from `%s version`: %s", path, strings.TrimSpace(string(out)))
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func gopathBins() []string {
	var dirs []string
	if b := os.Getenv("GOBIN"); b != "" {
		dirs = append(dirs, b)
	}
	if g := os.Getenv("GOPATH"); g != "" {
		dirs = append(dirs, filepath.Join(g, "bin"))
	}
	if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		if g := trimLine(string(out)); g != "" {
			dirs = append(dirs, filepath.Join(g, "bin"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	return dedupe(dirs)
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
