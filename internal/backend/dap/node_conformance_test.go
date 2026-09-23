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
	if _, err := os.Stat(jsFixtureDir(t)); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	// This suite does not pass yet, and that is the point of it: it is the
	// definition of done for the node profile, and the reason the profile is not
	// offered to agents. Run it deliberately with DBGMCP_NODE_WIP=1; leaving it
	// red in every run would train everyone to ignore a red suite.
	if os.Getenv("DBGMCP_NODE_WIP") == "" {
		t.Skip("node profile is under construction; set DBGMCP_NODE_WIP=1 to run its conformance suite")
	}
	dir := jsFixtureDir(t)
	cart := filepath.Join(dir, "cart.js")

	base := model.LaunchRequest{Mode: model.LaunchDebug, Target: "cart.js", WorkDir: dir}

	conformance.Run(t, conformance.Fixture{
		Name: "node/js-debug",
		New: func() backend.Backend {
			b, err := dap.NewUnderConstruction("node")
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
