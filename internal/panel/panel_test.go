package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	rowOf := func(name string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, name) {
				return l
			}
		}
		return ""
	}
	if !strings.Contains(rowOf("parser fix"), " 1h ") || !strings.Contains(rowOf("notes"), " off ") || !strings.HasPrefix(strings.TrimLeft(rowOf("notes"), " "), "▶") {
		t.Errorf("columns wrong:\n%s", out)
	}
	s.ConfigError = "lead must be under 1h"
	if !strings.Contains(render(s, 0, now, 120), "✗ config: lead must be under 1h") {
		t.Error("config error not shown")
	}
}

func fixture(t *testing.T, running bool) m {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	s := store.Status{Started: now.Add(-time.Hour), LastEvent: now, Default: model.Auto, Rows: []store.Row{
		{Pane: "p1", Name: "one", TTL: time.Hour, Phase: model.Quiet},
		{Pane: "p2", Name: "two", TTL: time.Hour, Phase: model.Quiet},
	}}
	if err := store.WriteStatus(filepath.Join(dir, "status.json"), s); err != nil {
		t.Fatal(err)
	}
	return m{dir: dir, running: func(string) bool { return running }}.load()
}

func send(x m, msg tea.Msg) m {
	n, _ := x.Update(msg)
	return n.(m)
}

func key(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func taken(t *testing.T, x m) []store.Request {
	t.Helper()
	r, err := store.TakeRequests(store.RequestsDir(x.dir))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestKeysWriteRequests(t *testing.T) {
	x := fixture(t, true)
	x = send(x, key("j"))
	for _, k := range []string{"enter", "c", "s"} {
		x = send(x, key(k))
	}
	got := taken(t, x)
	want := []store.Request{{Kind: "toggle", Pane: "p2"}, {Kind: "compact_now", Pane: "p2"}, {Kind: "skip", Pane: "p2"}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("requests = %+v", got)
	}
}

func TestClickTogglesClickedRow(t *testing.T) {
	x := fixture(t, true)
	x = send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: headerLines + 1})
	got := taken(t, x)
	if len(got) != 1 || got[0].Kind != "toggle" || got[0].Pane != "p2" {
		t.Fatalf("requests = %+v", got)
	}
	send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: headerLines + 5})
	if len(taken(t, x)) != 0 {
		t.Error("click below the rows wrote a request")
	}
}

func TestClickOnlyRenderedRows(t *testing.T) {
	x := fixture(t, true)
	x = send(x, tea.WindowSizeMsg{Width: 120, Height: headerLines + 2}) // room for one row
	send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: headerLines + 1})
	if len(taken(t, x)) != 0 {
		t.Error("click on a row that is not rendered wrote a request")
	}
	if strings.Contains(x.View(), "two") {
		t.Errorf("overflow row rendered:\n%s", x.View())
	}
}

func TestDefaultKeyWritesConfig(t *testing.T) {
	x := fixture(t, true)
	send(x, key("d"))
	b, err := os.ReadFile(filepath.Join(x.dir, "config.toml"))
	if err != nil || !strings.Contains(string(b), `default = "off"`) {
		t.Fatalf("config.toml = %q, %v", b, err)
	}
}

func TestNotRunning(t *testing.T) {
	x := fixture(t, false)
	if v := x.View(); !strings.Contains(v, "Warm Compact is not running (last status from") {
		t.Errorf("view:\n%s", v)
	}
	x = send(x, key("c"))
	x = send(x, key("d"))
	if len(taken(t, x)) != 0 {
		t.Error("request written while not running")
	}
	if _, err := os.Stat(filepath.Join(x.dir, "config.toml")); err == nil {
		t.Error("config written while not running")
	}
	if !strings.Contains(x.View(), "not running: nothing was sent") {
		t.Errorf("no notice:\n%s", x.View())
	}
}

func TestWriteErrorShownAndCleared(t *testing.T) {
	x := fixture(t, true)
	if err := os.WriteFile(filepath.Join(x.dir, "requests"), nil, 0o600); err != nil { // a file where the dir belongs
		t.Fatal(err)
	}
	x = send(x, key("c"))
	if !strings.Contains(x.View(), " ✗ ") {
		t.Errorf("error not shown:\n%s", x.View())
	}
	os.Remove(filepath.Join(x.dir, "requests"))
	x = send(x, key("c"))
	if strings.Contains(x.View(), " ✗ ") {
		t.Errorf("error not cleared:\n%s", x.View())
	}
}

func TestSelectionFollowsPane(t *testing.T) {
	x := fixture(t, true)
	x = send(x, key("j")) // p2
	s, _ := store.ReadStatus(filepath.Join(x.dir, "status.json"))
	s.Rows[0], s.Rows[1] = s.Rows[1], s.Rows[0]
	store.WriteStatus(filepath.Join(x.dir, "status.json"), s)
	x = x.load()
	if x.cursor != 0 || x.pane != "p2" {
		t.Fatalf("cursor %d pane %q", x.cursor, x.pane)
	}
}

func TestHeaderFormats(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	out := render(store.Status{Started: now.Add(-65 * time.Minute), Default: model.Auto}, 0, now, 120)
	if !strings.Contains(out, "running 1h05m") || !strings.Contains(out, "last event none") {
		t.Errorf("header:\n%s", out)
	}
	if uptime(12*time.Minute) != "12m" {
		t.Error("uptime")
	}
	for _, l := range strings.Split(render(store.Status{Default: model.Auto}, 0, now, 30), "\n") {
		if len([]rune(l)) > 30 {
			t.Errorf("line not clipped: %q", l)
		}
	}
}
