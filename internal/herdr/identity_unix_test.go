//go:build !windows

package herdr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerIdentityChangesWithANewSocketFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sock")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := ServerIdentity(p)
	if first == "" {
		t.Fatal("no identity for an existing file")
	}
	if again := ServerIdentity(p); again != first {
		t.Fatalf("identity not stable: %q then %q", first, again)
	}
	os.Remove(p)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if next := ServerIdentity(p); next == first {
		t.Fatalf("a new socket file kept the identity %q", next)
	}
}
