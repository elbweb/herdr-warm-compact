package run

import (
	"testing"

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
