package dap_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/backend/conformance"
	"github.com/didenkolab/dbgmcp/internal/backend/dap"
	"github.com/didenkolab/dbgmcp/internal/model"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "buggy_py")
}

// testPython is the interpreter the fixture runs under. The repository keeps an
// isolated virtualenv for this precisely so the suite never depends on whatever
// happens to be installed in the developer's active environment.
func testPython(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	venv := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".venv-test", "bin", "python")
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	t.Skip("no .venv-test with debugpy; create it with: python3 -m venv .venv-test && .venv-test/bin/pip install debugpy")
	return ""
}

// TestPythonConformance runs the same suite as the Delve backend, against a
// backend with a genuinely smaller capability set. That is the point: an
// abstraction with one implementation is usually wrong, and this is where the
// wrongness shows up.
func TestPythonConformance(t *testing.T) {
	t.Setenv("DBGMCP_PYTHON", testPython(t))
	dir := fixtureDir(t)
	cart := filepath.Join(dir, "cart.py")

	base := model.LaunchRequest{Mode: model.LaunchDebug, Target: "cart.py", WorkDir: dir}

	conformance.Run(t, conformance.Fixture{
		Name: "python/debugpy",
		New: func() backend.Backend {
			b, err := dap.New("python")
			if err != nil {
				t.Fatalf("python backend: %v", err)
			}
			return b
		},
		Launch:     base,
		CallSymbol: "line_total",
		CallFile:   cart,
		CallLine:   conformance.LineContaining(t, cart, "NEVER-REACHED") - 1,
		IntExpr:    "it.price",
		CallExpr:   "line_total(it)",
		LoopSymbol: "subtotal",
		LoopFile:   cart,
		LoopLine:   conformance.LineContaining(t, cart, "total += line_total"),
		LoopLocal:  "total",
		// Threads in Python are created by other threads, but DAP reports no
		// ancestry, so the suite checks the refusal rather than a chain.
		SpawnedSymbol: "worker",
		SpawnedFile:   cart,
		SpawnedLine:   conformance.LineContaining(t, cart, "out.append(subtotal"),
	})
}
