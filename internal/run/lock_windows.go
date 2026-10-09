//go:build windows

package run

import (
	"os"
	"syscall"
)

const errSharingViolation = syscall.Errno(32)

func openLock(path string, share uint32) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, share, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
}

// acquireLock holds the lock file open with write access and read-only sharing: a second opener that
// wants write access gets a sharing violation, while readers can still read the holder info.
func acquireLock(dir string) (*Lock, error) {
	path := lockPath(dir)
	h, err := openLock(path, syscall.FILE_SHARE_READ)
	if err != nil {
		if err == errSharingViolation {
			return nil, errLocked
		}
		return nil, err
	}
	return &Lock{f: os.NewFile(uintptr(h), path)}, nil
}

// Running reports whether some process holds the lock.
func Running(dir string) bool {
	h, err := openLock(lockPath(dir), syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE)
	if err != nil {
		return err == errSharingViolation
	}
	syscall.CloseHandle(h)
	return false
}
