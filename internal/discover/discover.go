// Package discover answers "what can be debugged here" without an IDE.
//
// An IDE plugin can list run configurations because a human already made them.
// With no IDE there is nothing to list, so the targets have to be derived from
// the project itself -- otherwise the agent has to be told what to run, which
// is one more thing a human has to be present for.
package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Target is one thing that can be put under a debugger.
type Target struct {
	// Mode is the launch mode this target wants: "debug" for a main package,
	// "test" for a package with tests.
	Mode       string `json:"mode"`
	ImportPath string `json:"import_path"`
	Dir        string `json:"dir"`
	Name       string `json:"name"`
	// Tests are the test function names in this package, so an agent can go
	// straight to the one it cares about instead of running the whole package.
	Tests []string `json:"tests,omitempty"`
}

type goPackage struct {
	Dir          string
	ImportPath   string
	Name         string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
}

// Go lists the debuggable targets of a Go module rooted at dir.
func Go(ctx context.Context, dir string) ([]Target, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// go list writes the useful part to stderr, and "no Go files" or a
		// broken module is exactly what an agent needs to hear verbatim.
		if ee, isExit := err.(*exec.ExitError); isExit && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("go list failed in %s: %s", dir, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go list failed in %s: %w", dir, err)
	}

	var targets []Target
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			break
		}
		if p.Name == "main" && len(p.GoFiles) > 0 {
			targets = append(targets, Target{
				Mode: "debug", ImportPath: p.ImportPath, Dir: p.Dir, Name: p.Name,
			})
		}
		if tests := testFunctions(p); len(tests) > 0 {
			targets = append(targets, Target{
				Mode: "test", ImportPath: p.ImportPath, Dir: p.Dir, Name: p.Name, Tests: tests,
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Mode != targets[j].Mode {
			return targets[i].Mode < targets[j].Mode
		}
		return targets[i].ImportPath < targets[j].ImportPath
	})
	return targets, nil
}

// testFunctions reads the test names out of the source rather than running the
// test binary to ask it. Listing must not have side effects: an agent calling
// "what is here" should never execute the project's code.
func testFunctions(p goPackage) []string {
	var names []string
	fset := token.NewFileSet()
	for _, group := range [][]string{p.TestGoFiles, p.XTestGoFiles} {
		for _, file := range group {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, file), nil, 0)
			if err != nil {
				continue // an unparseable test file is not a reason to report nothing
			}
			for _, decl := range f.Decls {
				fn, isFunc := decl.(*ast.FuncDecl)
				if !isFunc || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
					continue
				}
				// Test functions take exactly one parameter, *testing.T. Without
				// this check, a helper named TestHelper(x int) would be offered
				// as something runnable.
				if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
					continue
				}
				names = append(names, fn.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}
