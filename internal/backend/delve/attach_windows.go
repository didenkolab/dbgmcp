//go:build windows

package delve

import "fmt"

func checkProcessExists(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("a pid is required to attach, and %d is not one", pid)
	}
	// No cheap pre-flight check here; Delve's own failure is the report.
	return nil
}
