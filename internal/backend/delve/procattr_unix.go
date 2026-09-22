//go:build !windows

package delve

import (
	"os/exec"
	"syscall"
)

// isolateProcessGroup puts dlv in its own process group so the debuggee it
// spawns can be killed with it. Killing only the dlv pid leaves the debuggee
// running, which is how orphaned processes at 450% CPU happen.
func isolateProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) {
	// Negative pid means "the whole group". Best effort: the group may already
	// be gone, and that is the outcome we wanted anyway.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
