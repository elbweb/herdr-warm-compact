package run

import (
	"testing"
	"time"
)

func TestLockExclusiveAndRunning(t *testing.T) {
	dir := t.TempDir()
	if Running(dir) {
		t.Fatal("Running true with no lock held")
	}
	l, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(dir); err == nil {
		t.Fatal("second acquire succeeded while the first is held")
	}
	if !Running(dir) {
		t.Fatal("Running false while held")
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if Running(dir) {
		t.Fatal("Running true after release")
	}
	l2, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	l2.Release()
}

func TestLockHolderInfoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	want := LockInfo{PID: 4242, Server: "srv-1", Started: time.Now().UTC().Format(time.RFC3339)}
	if err := l.Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := readHolder(dir)
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	// A shorter rewrite must not leave trailing bytes of the longer one.
	want.Server = "s"
	if err := l.Write(want); err != nil {
		t.Fatal(err)
	}
	if got, err := readHolder(dir); err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestStopNotRunningReturnsQuickly(t *testing.T) {
	start := time.Now()
	if err := Stop(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Stop on a dir with no resident was slow")
	}
}
