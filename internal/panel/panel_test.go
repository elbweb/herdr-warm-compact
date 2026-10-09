package panel

import (
	"strings"
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/store"
)

func TestRenderRowsAndHeader(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	s := store.Status{Started: now.Add(-time.Hour), LastEvent: now.Add(-5 * time.Second), Default: model.Auto, Rows: []store.Row{
		{Pane: "p1", Name: "parser fix", Workspace: "proj", Tokens: 212000, TTL: time.Hour, Effective: model.Auto, Phase: model.Armed, Deadline: now.Add(38 * time.Minute)},
		{Pane: "p2", Name: "notes", Workspace: "proj", Tokens: 190000, TTL: time.Hour, Override: model.Off, Effective: model.Off, Phase: model.Quiet, Reason: "off"},
		{Pane: "p3", Name: "big one", Workspace: "work", Tokens: 260000, TTL: time.Hour, Effective: model.Auto, Phase: model.Failed, Reason: "draft not restored: it is in Claude's stash (Ctrl+S)"},
	}}
	out := render(s, 1, now, 120)
	for _, want := range []string{"default: auto", "parser fix", "212k", "1h", "⏱ 38m", "notes", "off", "▶", "✗ draft not restored", "config ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	s.ConfigError = "lead must be under 1h"
	if !strings.Contains(render(s, 0, now, 120), "✗ config: lead must be under 1h") {
		t.Error("config error not shown")
	}
}
