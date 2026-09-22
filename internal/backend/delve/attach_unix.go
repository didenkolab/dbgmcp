//go:build !windows

package delve

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
)

// attachPermissionAdvice names the actual obstacle rather than restating the
// error. On Linux the usual cause is not file permissions at all: Yama's
// ptrace_scope, which CI runners and most distributions set to 1, allows a
// process to debug only its own descendants. An agent told "permission denied"
// retries; an agent told this does not.
func attachPermissionAdvice() string {
	if runtime.GOOS != "linux" {
		return "run as its owner, or with the privileges your platform requires for debugging"
	}
	scope := "unknown"
	if raw, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope"); err == nil {
		scope = strings.TrimSpace(string(raw))
	}
	if scope == "0" {
		return "run as its owner, or with CAP_SYS_PTRACE"
	}
	return fmt.Sprintf(
		"Yama ptrace_scope is %s, so a process may only debug its own descendants. "+
			"Run as the target's owner with CAP_SYS_PTRACE, or set ptrace_scope to 0 "+
			"(`sudo sysctl kernel.yama.ptrace_scope=0`) if that is acceptable on this machine", scope)
}

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
		// It exists but cannot be signalled by this user. Delve would fail later
		// and less clearly.
		return fmt.Errorf("process %d exists but this user cannot debug it: %s", pid, attachPermissionAdvice())
	default:
		return fmt.Errorf("no process with pid %d is running. Find the right one with `pgrep -f <name>` or `ps`", pid)
	}
}
