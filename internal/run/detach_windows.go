//go:build windows

package run

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008
const createNoWindow = 0x08000000

// Detach starts exe with args outside herdr's command slot: no console, no inherited pipes.
func Detach(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP}
	return cmd.Start()
}
