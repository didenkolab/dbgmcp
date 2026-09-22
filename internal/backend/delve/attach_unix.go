//go:build !windows

package delve

import (
	"fmt"
	"syscall"
)

// checkProcessExists answers before Delve is even started. Signal 0 performs the
// permission and existence checks without delivering anything, which turns a
// puzzling debugger failure into one sentence naming the pid.
func checkProcessExists(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("a pid is required to attach, and %d is not one", pid)
	}
	switch err := syscall.Kill(pid, 0); err {
	case nil:
		return nil
	case syscall.EPERM:
		// It exists but belongs to another user. Delve would fail later and
		// less clearly.
		return fmt.Errorf("process %d exists but this user cannot debug it; run as its owner, or with the privileges your platform requires", pid)
	default:
		return fmt.Errorf("no process with pid %d is running. Find the right one with `pgrep -f <name>` or `ps`", pid)
	}
}
