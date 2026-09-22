package delve

import (
	"strings"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/model"
)

func joined(args []string) string { return strings.Join(args, " ") }

func TestLaunchArgsSendTheBuiltBinaryToOurOwnDirectory(t *testing.T) {
	// Delve would otherwise build __debug_bin<random> in the user's working
	// directory and rely on its own exit to clean it up -- which never happens,
	// because this server kills the process group.
	for _, mode := range []model.LaunchMode{model.LaunchDebug, model.LaunchTest} {
		args, _, err := launchArgs(mode, ".", "/tmp/s.sock", "/tmp/dbgmcp-x/debug.bin", redirectPaths{}, nil, "")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if !strings.Contains(joined(args), "--output=/tmp/dbgmcp-x/debug.bin") {
			t.Errorf("%s does not redirect the build output: %s", mode, joined(args))
		}
	}
}

func TestLaunchArgsDoNotRebuildAnAlreadyBuiltBinary(t *testing.T) {
	// exec takes the binary as given; passing --output would be meaningless and
	// Delve rejects it.
	args, optimisationsDisabled, err := launchArgs(model.LaunchExec, "./app", "/tmp/s.sock", "/tmp/dbgmcp-x/debug.bin", redirectPaths{}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(joined(args), "--output") {
		t.Errorf("exec must not ask Delve to build anything: %s", joined(args))
	}
	// And the caller must be told the binary is the one that ships, unlike the
	// modes where Delve rebuilds with the optimiser off.
	if optimisationsDisabled {
		t.Error("exec runs the binary as given, so optimisations are whatever it was built with")
	}
}

func TestLaunchArgsPutTestFilterBeforeProgramArguments(t *testing.T) {
	args, _, err := launchArgs(model.LaunchTest, ".", "/tmp/s.sock", "/tmp/o.bin", redirectPaths{}, []string{"-x"}, "TestFoo")
	if err != nil {
		t.Fatal(err)
	}
	got := joined(args)
	if !strings.Contains(got, "-- -test.run TestFoo -x") {
		t.Errorf("test filter is not passed through correctly: %s", got)
	}
}

func TestLaunchArgsRejectAnUnknownMode(t *testing.T) {
	if _, _, err := launchArgs("teleport", ".", "/tmp/s.sock", "/tmp/o.bin", redirectPaths{}, nil, ""); err == nil {
		t.Fatal("an unknown launch mode must be refused, not guessed at")
	}
}

func TestLaunchArgsRedirectBothDebuggeeStreamsToSeparateFiles(t *testing.T) {
	// Delve's own log lines go to its stderr; letting the debuggee's stderr land
	// there too would interleave them beyond recovery.
	args, _, err := launchArgs(model.LaunchDebug, ".", "/tmp/s.sock", "/tmp/o.bin",
		redirectPaths{stdout: "/tmp/t/stdout", stderr: "/tmp/t/stderr"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	got := joined(args)
	for _, want := range []string{"stdout:/tmp/t/stdout", "stderr:/tmp/t/stderr"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing redirect %q in: %s", want, got)
		}
	}
}

func TestLaunchArgsAttachTakesAPidAndTouchesNothingElse(t *testing.T) {
	// Attach must not build anything and must not redirect the streams of a
	// process that already owns them.
	args, optimisationsDisabled, err := launchArgs(model.LaunchAttach, "4242", "/tmp/s.sock", "/tmp/o.bin",
		redirectPaths{stdout: "/tmp/t/out", stderr: "/tmp/t/err"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	got := joined(args)
	if args[0] != "attach" || args[len(args)-1] != "4242" {
		t.Errorf("attach did not receive the pid as its argument: %s", got)
	}
	for _, forbidden := range []string{"--output", "stdout:", "stderr:"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("attach must not use %q: %s", forbidden, got)
		}
	}
	if optimisationsDisabled {
		t.Error("attach debugs the binary as it is; nothing was rebuilt")
	}
}
