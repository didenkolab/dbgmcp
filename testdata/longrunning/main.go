// Command longrunning exists so the attach path can be tested against a process
// that is genuinely already running, rather than one the debugger started.
package main

import (
	"fmt"
	"os"
	"time"
)

func tick(n int) int {
	return n * 2
}

func main() {
	fmt.Println("longrunning started", os.Getpid())
	for i := 0; ; i++ {
		_ = tick(i)
		time.Sleep(50 * time.Millisecond)
	}
}
