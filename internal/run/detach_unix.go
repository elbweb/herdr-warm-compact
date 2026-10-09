//go:build !windows

package run

import (
	"os/exec"
	"syscall"
)

func Detach(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
