package run

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/herdr"
)

func TestToEnginePane(t *testing.T) {
	ws := map[string]string{"w1": "proj"}
	p, ok := ToEnginePane(herdr.Agent{PaneID: "w1:p1", WorkspaceID: "w1", Agent: "claude", Status: "idle", CWD: "/x", Title: "topic", SessionID: "s1"}, ws)
	if !ok || p.ID != "w1:p1" || p.Session != "s1" || p.Workspace != "proj" || p.Folder != "/x" || p.Name != "topic" || p.Status != "idle" {
		t.Fatalf("%+v %v", p, ok)
	}
	if _, ok := ToEnginePane(herdr.Agent{PaneID: "w1:p2", Agent: "codex", SessionID: "x"}, ws); ok {
		t.Fatal("non-claude pane accepted")
	}
	if _, ok := ToEnginePane(herdr.Agent{PaneID: "w1:p3", Agent: "claude"}, ws); ok {
		t.Fatal("claude pane without a session accepted")
	}
}

func TestReachabilityExitsAfterFiveMinutesAbsent(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	refused := &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	var r reachability
	if r.observe(refused, t0) || r.observe(refused, t0.Add(4*time.Minute)) {
		t.Fatal("gone before five minutes")
	}
	if r.observe(nil, t0.Add(4*time.Minute+30*time.Second)) {
		t.Fatal("gone on a success")
	}
	if r.observe(refused, t0.Add(6*time.Minute)) || r.observe(refused, t0.Add(10*time.Minute)) {
		t.Fatal("a success did not reset the clock")
	}
	if !r.observe(refused, t0.Add(11*time.Minute)) {
		t.Fatal("not gone after five minutes absent")
	}
	var slow reachability
	slow.observe(refused, t0)
	if slow.observe(context.DeadlineExceeded, t0.Add(6*time.Minute)) || slow.observe(refused, t0.Add(7*time.Minute)) {
		t.Fatal("a timeout counted as absent")
	}
}

func TestExeGone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plugin.exe")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if exeGone(p) || exeGone("") {
		t.Fatal("present executable reported gone")
	}
	os.Remove(p)
	if !exeGone(p) {
		t.Fatal("removed executable not reported gone")
	}
}

func TestProjectsDir(t *testing.T) {
	home := filepath.Join("h", "me")
	if got, want := projectsDir("", home), filepath.Join(home, ".claude", "projects"); got != want {
		t.Fatalf("default: %q, want %q", got, want)
	}
	cfg := filepath.Join("c", "claude")
	if got, want := projectsDir(cfg, home), filepath.Join(cfg, "projects"); got != want {
		t.Fatalf("CLAUDE_CONFIG_DIR: %q, want %q", got, want)
	}
}
