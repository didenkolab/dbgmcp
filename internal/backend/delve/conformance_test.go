package delve_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/backend/conformance"
	"github.com/didenkolab/dbgmcp/internal/backend/delve"
	"github.com/didenkolab/dbgmcp/internal/model"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "buggy")
}

// TestDelveConformance is what keeps describe_backend honest for this backend.
// If a capability is declared and this fails, the declaration is the bug.
func TestDelveConformance(t *testing.T) {
	if _, err := delve.FindDelve(); err != nil {
		t.Skipf("delve is not installed: %v", err)
	}
	dir := fixtureDir(t)
	main := filepath.Join(dir, "main.go")
	base := model.LaunchRequest{Mode: model.LaunchDebug, Target: ".", WorkDir: dir}

	withAncestry := base
	// Goroutine ancestry does not exist unless the runtime was told to record
	// it, and recording costs a stack capture at every goroutine creation.
	withAncestry.Env = map[string]string{"GODEBUG": "tracebackancestors=10"}

	conformance.Run(t, conformance.Fixture{
		Name:               "go/delve",
		New:                func() backend.Backend { return delve.New() },
		Launch:             base,
		LaunchWithAncestry: withAncestry,
		CallSymbol:         "main.lineTotal",
		CallFile:           main,
		CallLine:           conformance.LineContaining(t, main, "NEVER-REACHED") - 1,
		IntExpr:            "it.Price",
		CallExpr:           "lineTotal(it)",
		LoopSymbol:         "main.Subtotal",
		LoopFile:           main,
		LoopLine:           conformance.LineContaining(t, main, "total += lineTotal"),
		LoopLocal:          "total",
		SpawnedSymbol:      "main.worker",
		SpawnedFile:        main,
		SpawnedLine:        conformance.LineContaining(t, main, "out <- Subtotal"),
	})
}
