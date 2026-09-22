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
		args, _, err := launchArgs(mode, ".", "/tmp/s.sock", "/tmp/dbgmcp-x/debug.bin", nil, "")
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
	args, optimisationsDisabled, err := launchArgs(model.LaunchExec, "./app", "/tmp/s.sock", "/tmp/dbgmcp-x/debug.bin", nil, "")
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
	args, _, err := launchArgs(model.LaunchTest, ".", "/tmp/s.sock", "/tmp/o.bin", []string{"-x"}, "TestFoo")
	if err != nil {
		t.Fatal(err)
	}
	got := joined(args)
	if !strings.Contains(got, "-- -test.run TestFoo -x") {
		t.Errorf("test filter is not passed through correctly: %s", got)
	}
}

func TestLaunchArgsRejectAnUnknownMode(t *testing.T) {
	if _, _, err := launchArgs("teleport", ".", "/tmp/s.sock", "/tmp/o.bin", nil, ""); err == nil {
		t.Fatal("an unknown launch mode must be refused, not guessed at")
	}
}
