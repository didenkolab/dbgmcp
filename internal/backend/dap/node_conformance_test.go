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

func jsFixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "buggy_js")
}

// TestNodeConformance runs the same suite as the Go and Python backends.
//
// Three runtimes through two backends, one suite: that is the only evidence that
// the neutral model is a model rather than a description of whichever debugger
// was written first.
func TestNodeConformance(t *testing.T) {
	requireJsDebug(t)
	if _, err := os.Stat(jsFixtureDir(t)); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	dir := jsFixtureDir(t)
	cart := filepath.Join(dir, "cart.js")

	base := model.LaunchRequest{Mode: model.LaunchDebug, Target: "cart.js", WorkDir: dir}

	conformance.Run(t, conformance.Fixture{
		Name: "node/js-debug",
		New: func() backend.Backend {
			b, err := dap.New("node")
			if err != nil {
				t.Fatalf("node backend: %v", err)
			}
			return b
		},
		Launch:     base,
		CallSymbol: "lineTotal",
		CallFile:   cart,
		CallLine:   conformance.LineContaining(t, cart, "NEVER-REACHED") - 1,
		IntExpr:    "it.price",
		CallExpr:   "lineTotal(it)",
		LoopSymbol: "subtotal",
		LoopFile:   cart,
		LoopLine:   conformance.LineContaining(t, cart, "total += lineTotal"),
		LoopLocal:  "total",
		// JavaScript has one thread of its own, so there is no second unit to
		// ask about ancestry -- the suite checks the refusal instead.
		SpawnedSymbol: "rotate",
		SpawnedFile:   cart,
		SpawnedLine:   conformance.LineContaining(t, cart, "token = refreshToken"),
	})
}

// requireJsDebug skips rather than fails when the adapter is not installed.
//
// It is an optional toolchain, like Delve for the Go backend: a machine without
// it should report "not exercised here", not "broken". A hard failure on a
// missing optional dependency trains people to ignore a red suite.
func requireJsDebug(t *testing.T) {
	t.Helper()
	if _, err := dap.JsDebugInstalled(); err != nil {
		t.Skipf("the JavaScript debug adapter is not installed: %v", err)
	}
}
