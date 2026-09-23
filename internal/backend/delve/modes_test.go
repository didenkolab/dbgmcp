package delve

import (
	"strings"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// TestEveryLaunchModeIsHandledDeliberately walks every mode rather than the
// ones that came to mind.
//
// This is the test that would have caught two shipped defects. Both came from a
// condition written as "not exec" when it meant "test or debug": when attach
// arrived it fell through into the compiled-target branch, so the server asked
// Delve to build a binary for a process that was already running, and told the
// agent optimisations were disabled in a binary it had never touched.
func TestEveryLaunchModeIsHandledDeliberately(t *testing.T) {
	// What each mode is actually true of, stated once so a new mode cannot be
	// added without a decision being made about it here.
	expected := map[model.LaunchMode]struct {
		subcommand    string
		buildsTarget  bool
		redirectsIO   bool
		takesTestFlag bool
	}{
		model.LaunchTest:   {subcommand: "test", buildsTarget: true, redirectsIO: true, takesTestFlag: true},
		model.LaunchDebug:  {subcommand: "debug", buildsTarget: true, redirectsIO: true},
		model.LaunchExec:   {subcommand: "exec", buildsTarget: false, redirectsIO: true},
		model.LaunchAttach: {subcommand: "attach", buildsTarget: false, redirectsIO: false},
	}

	all := model.AllLaunchModes()
	if len(expected) != len(all) {
		t.Fatalf("a launch mode was added without deciding how it behaves here: %d modes, %d covered",
			len(all), len(expected))
	}

	redirects := redirectPaths{stdout: "/tmp/t/out", stderr: "/tmp/t/err"}
	for _, mode := range all {
		want, covered := expected[mode]
		if !covered {
			t.Fatalf("mode %q is not covered by this test", mode)
		}

		args, optimisationsDisabled, err := launchArgs(mode, "target", "/tmp/s.sock", "/tmp/o.bin",
			redirects, nil, "TestFoo", buildOptions{})
		if err != nil {
			t.Errorf("%s: %v", mode, err)
			continue
		}
		got := strings.Join(args, " ")

		if args[0] != want.subcommand {
			t.Errorf("%s: subcommand is %q, expected %q", mode, args[0], want.subcommand)
		}
		if builds := strings.Contains(got, "--output"); builds != want.buildsTarget {
			t.Errorf("%s: builds a target = %v, expected %v (%s)", mode, builds, want.buildsTarget, got)
		}
		// Delve only rebuilds with the optimiser off for the modes where it does
		// the building, so the two must agree.
		if optimisationsDisabled != want.buildsTarget {
			t.Errorf("%s: reports optimisations disabled = %v, but builds a target = %v",
				mode, optimisationsDisabled, want.buildsTarget)
		}
		if redirected := strings.Contains(got, "stdout:"); redirected != want.redirectsIO {
			t.Errorf("%s: redirects output = %v, expected %v", mode, redirected, want.redirectsIO)
		}
		if takes := strings.Contains(got, "-test.run"); takes != want.takesTestFlag {
			t.Errorf("%s: passes -test.run = %v, expected %v", mode, takes, want.takesTestFlag)
		}
	}
}

// TestEveryStepKindIsAccepted keeps a new step kind from silently becoming an
// error at runtime.
func TestEveryStepKindIsAccepted(t *testing.T) {
	b := New()
	for _, kind := range model.AllStepKinds() {
		_, err := b.Step(nil, kind)
		// No session, so every one must fail for that reason and not because the
		// kind was unrecognised.
		if err == nil {
			t.Errorf("%s: expected a no-session error", kind)
			continue
		}
		if strings.Contains(err.Error(), "unknown step kind") {
			t.Errorf("%s is not handled", kind)
		}
	}
}

func TestBuildTagsReachTheCompiler(t *testing.T) {
	// A project can keep most of its suite behind a tag. Without this the tagged
	// half cannot be built, so it cannot be debugged -- and that is usually the
	// half that talks to a database and holds the interesting defects.
	build := buildOptions{tags: []string{"integration", "devsecrets"}}
	args, _, err := launchArgs(model.LaunchTest, ".", "/tmp/s.sock", "/tmp/o.bin",
		redirectPaths{}, nil, "TestCharge", build)
	if err != nil {
		t.Fatal(err)
	}
	got := joined(args)
	// Comma-separated, because a space in a build-flags value becomes a separate
	// argument to the Go tool and the build fails on a phantom second package.
	if !strings.Contains(got, "--build-flags=-tags=integration,devsecrets") {
		t.Errorf("build tags did not reach the compiler correctly: %s", got)
	}
	if strings.Contains(got, "integration devsecrets") {
		t.Errorf("tags were joined with a space, which the Go tool reads as two arguments: %s", got)
	}
}

func TestDeterminismSwitchesOffShuffling(t *testing.T) {
	// A shuffled run puts a different test where the breakpoint was set, which
	// looks like the breakpoint not working.
	args, _, err := launchArgs(model.LaunchTest, ".", "/tmp/s.sock", "/tmp/o.bin",
		redirectPaths{}, nil, "TestCharge", buildOptions{deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined(args), "-test.shuffle=off") {
		t.Errorf("shuffling was not switched off: %s", joined(args))
	}
}

func TestBuildFlagsAreNotSentWhereNothingIsBuilt(t *testing.T) {
	// exec and attach have no build step; sending build flags to them is a
	// request Delve cannot honour.
	for _, mode := range []model.LaunchMode{model.LaunchExec, model.LaunchAttach} {
		args, _, err := launchArgs(mode, "target", "/tmp/s.sock", "/tmp/o.bin",
			redirectPaths{}, nil, "", buildOptions{tags: []string{"integration"}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(joined(args), "build-flags") {
			t.Errorf("%s: build flags sent to a mode that builds nothing", mode)
		}
	}
}
