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
)

// RequiredVersion is pinned so a surprise upgrade cannot silently change
// behaviour underneath a running agent.
const RequiredVersion = "v1.27.2"

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
