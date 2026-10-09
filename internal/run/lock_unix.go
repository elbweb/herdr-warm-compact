//go:build !windows

package run

import (
	"os"
	"syscall"
)

// acquireLock holds an exclusive flock on the lock file for the process's life.
func acquireLock(dir string) (*Lock, error) {
	f, err := os.OpenFile(lockPath(dir), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, errLocked
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Running reports whether some process holds the lock.
func Running(dir string) bool {
	f, err := os.OpenFile(lockPath(dir), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err == syscall.EWOULDBLOCK
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
