package dap_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

// TestPythonRecordsTheWholeFrame pins the translation rather than the mechanism.
//
// A logpoint evaluates expressions and lets the program run on, so there is no
// stopped frame for the protocol to enumerate. Python can still answer, because
// the language names its own scope; that is a fact about Python, not about DAP,
// and it belongs in the profile where it can be wrong for one runtime only.
func TestPythonRecordsTheWholeFrame(t *testing.T) {
	t.Setenv("DBGMCP_PYTHON", testPython(t))
	dir := fixtureDir(t)

	b, err := dap.New("python")
	if err != nil {
		t.Fatalf("python backend: %v", err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	ctx := context.Background()
	if err := b.Launch(ctx, model.LaunchRequest{Mode: model.LaunchDebug, Target: "cart.py", WorkDir: dir}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	tr, err := b.Trace(ctx, []model.Probe{{
		Location: model.Location{File: filepath.Join(dir, "cart.py"), Line: lineOfSubtotalLoop(t, dir)},
		Record:   []string{model.RecordEverythingInScope},
		MaxHits:  1,
	}}, 90*time.Second)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(tr.Hits) == 0 {
		t.Fatalf("the probe never fired: %+v", tr)
	}
	// The whole frame arrives under the expression this runtime uses to name its
	// own scope, and it has to actually carry the loop's variables.
	whole, found := tr.Hits[0].Values["locals()"]
	if !found {
		t.Fatalf("no whole-frame reading; got %v", tr.Hits[0].Values)
	}
	for _, name := range []string{"total", "it"} {
		if !strings.Contains(whole, name) {
			t.Errorf("the frame does not mention %q: %s", name, whole)
		}
	}
}

// TestNodeRefusesTheWholeFrameByName is the other half of the same contract: a
// runtime that cannot do it must say so and name the alternative, not evaluate
// an expression literally called "*" and report whatever comes back.
//
// It needs a launched session, because until the adapter has answered initialize
// the backend does not yet know what it can do and refuses for a different reason.
func TestNodeRefusesTheWholeFrameByName(t *testing.T) {
	requireJsDebug(t)
	dir := jsFixtureDir(t)
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	b, err := dap.New("node")
	if err != nil {
		t.Fatalf("node backend: %v", err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	ctx := context.Background()
	if err := b.Launch(ctx, model.LaunchRequest{Mode: model.LaunchDebug, Target: "cart.js", WorkDir: dir}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	_, err = b.Trace(ctx, []model.Probe{{
		Location: model.Location{File: filepath.Join(dir, "cart.js"), Line: 1},
		Record:   []string{model.RecordEverythingInScope},
	}}, 30*time.Second)
	if err == nil {
		t.Fatal("the node adapter accepted a whole-frame request")
	}
	for _, want := range []string{"everything in scope", "Name the expressions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// lineOfSubtotalLoop finds the accumulating line without pinning a number that a
// fixture edit would quietly invalidate.
func lineOfSubtotalLoop(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "cart.py"))
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "total += line_total(it)") {
			return i + 1
		}
	}
	t.Fatal("the fixture no longer accumulates into total")
	return 0
}
