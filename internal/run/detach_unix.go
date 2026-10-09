//go:build !windows

package run

import (
	"os"
	"os/exec"
	"syscall"
)

func Detach(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// alive: on Unix FindProcess always succeeds; signal 0 tests the process.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
