//go:build windows

package delve

import (
	"os/exec"
	"strconv"
)

func isolateProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
