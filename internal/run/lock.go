package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/store"
)

// errLocked means another handle holds the lock file.
var errLocked = errors.New("lock is held")

// LockInfo is what the holder writes into the lock file.
type LockInfo struct {
	PID     int    `json:"pid"`
	Server  string `json:"server"`
	Started string `json:"started"`
}

// Lock is the OS-held single-instance lock; the OS drops it when the process dies.
type Lock struct{ f *os.File }

func lockPath(dir string) string { return filepath.Join(dir, "warm-compact.lock") }

// Write replaces the holder info through the held handle.
func (l *Lock) Write(i LockInfo) error {
	b, err := json.Marshal(i)
	if err != nil {
		return err
	}
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	if _, err := l.f.WriteAt(b, 0); err != nil {
		return err
	}
	return nil
}

// Release closes the handle, which frees the lock.
func (l *Lock) Release() error { return l.f.Close() }

func readHolder(dir string) (LockInfo, error) {
	var i LockInfo
	b, err := os.ReadFile(lockPath(dir))
	if err != nil {
		return i, err
	}
	return i, json.Unmarshal(b, &i)
}

// Stop asks the resident to quit (a request, never a kill) and waits for its lock to clear.
func Stop(dir string) error {
	if !Running(dir) {
		return nil
	}
	if err := store.WriteRequest(store.RequestsDir(dir), store.Request{Kind: "quit"}); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !Running(dir) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the resident did not quit within 15 s")
}
