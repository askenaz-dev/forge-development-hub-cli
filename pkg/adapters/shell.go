package adapters

import (
	"os/exec"
	"runtime"
)

// defaultShellRun executes a one-line shell command via the platform's
// default shell. On POSIX systems we use `sh -c`; on Windows we use
// `cmd /C` to keep the dependency on Git-for-Windows or PowerShell off the
// probe path.
//
// Output is discarded; only the exit status matters. A probe must never
// require interaction or stdin.
func defaultShellRun(cmd string) error {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	return c.Run()
}
