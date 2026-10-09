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
	for _, want := range []string{"default auto", "parser fix", "212k", "⏱ 38m", "notes", "▶", "✗ draft not", "? keys"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	at := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(l, sub) {
				return i
			}
		}
		t.Fatalf("no line with %q:\n%s", sub, out)
		return -1
	}
	if i := at("notes"); !strings.HasPrefix(lines[i], " ▶ notes") || !strings.HasSuffix(lines[i], "190k") || lines[i+1] != "     off · 1h · off" {
		t.Errorf("row layout wrong:\n%s", out)
	}
	if at(" proj") > at("parser fix") || at(" work") > at("big one") || at(" work") < at("notes") {
		t.Errorf("workspace headings out of place:\n%s", out)
	}
	for _, l := range lines {
		if len([]rune(l)) > width {
			t.Errorf("line wider than %d: %q", width, l)
		}
	}
	s.ConfigError = "lead must be under 1h"
	if !strings.Contains(render(s, 0, now, 120), "✗ config: lead must be") {
		t.Error("config error not shown")
	}
}

func TestKeysToggleHelp(t *testing.T) {
	x := fixture(t, true)
	if strings.Contains(x.View(), "compact now") {
		t.Fatal("key list shown before ?")
	}
	x = send(x, key("?"))
	if !strings.Contains(x.View(), "c compact now") {
		t.Fatalf("key list not shown after ?:\n%s", x.View())
	}
	if send(x, key("?")).keys {
		t.Error("? did not hide the key list")
	}
}

func TestScrollKeepsCursorInView(t *testing.T) {
	x := fixture(t, true)
	x = send(x, tea.WindowSizeMsg{Width: 40, Height: 5}) // two header lines, room for three body lines
	if strings.Contains(x.View(), "two") {
		t.Fatalf("second row fits unexpectedly:\n%s", x.View())
	}
	x = send(x, key("j"))
	if v := x.View(); !strings.Contains(v, "▶ two") || len(strings.Split(strings.TrimSuffix(v, "\n"), "\n")) > 5 {
		t.Errorf("cursor row not scrolled into view, or too tall:\n%s", v)
	}
}

func TestScrolledClickHitsTheRowUnderIt(t *testing.T) {
	x := fixture(t, true)
	x = send(x, tea.WindowSizeMsg{Width: 40, Height: 5})
	x = send(x, key("j"))
	if n := len(strings.Split(x.View(), "\n")); n > 5 { // more lines than the window: bubbletea drops the top one
		t.Fatalf("view is %d lines in a 5-line window:\n%s", n, x.View())
	}
	for _, dy := range []int{0, 1} { // both lines of the row
		send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: lineOf(t, x, "▶ two") + dy})
		if got := taken(t, x); len(got) != 1 || got[0].Pane != "p2" {
			t.Fatalf("click on line %d of the row: requests = %+v", dy, got)
		}
	}
}

func TestWideCharactersStayInWidth(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	s := store.Status{Started: now, Default: model.Auto, Rows: []store.Row{
		{Pane: "p1", Name: "台北公寓搬家的所有事情和清單整理", Workspace: "個人", Tokens: 212000, TTL: time.Hour, Phase: model.Quiet, Reason: "below threshold"},
		{Pane: "p2", Name: "two\nlines", Workspace: "個人", Tokens: 9000, TTL: time.Hour, Phase: model.Quiet},
	}}
	for _, w := range []int{120, 36, 20, 5} {
		out := render(s, 0, now, w)
		for _, l := range strings.Split(out, "\n") {
			if got := cols.StringWidth(l); got > min(w, width) {
				t.Errorf("width %d: line is %d columns: %q", w, got, l)
			}
		}
		if strings.Contains(out, "two\n") {
			t.Errorf("a newline in a name split the row:\n%s", out)
		}
	}
	if l := strings.Split(render(s, 0, now, 120), "\n")[4]; !strings.HasSuffix(l, "212k") || cols.StringWidth(l) != width {
		t.Errorf("wide name row not padded to the width: %q", l)
	}
}

func lineOf(t *testing.T, x m, sub string) int {
	t.Helper()
	for i, l := range strings.Split(x.View(), "\n") {
		if strings.Contains(l, sub) {
			return i
		}
	}
	t.Fatalf("no line with %q:\n%s", sub, x.View())
	return -1
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
	click := func(y int) { send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: y}) }
	click(lineOf(t, x, "two") + 1) // the row's second line counts too
	got := taken(t, x)
	if len(got) != 1 || got[0].Kind != "toggle" || got[0].Pane != "p2" {
		t.Fatalf("requests = %+v", got)
	}
	click(0)
	click(lineOf(t, x, "two") + 5)
	if len(taken(t, x)) != 0 {
		t.Error("click off the rows wrote a request")
	}
}

func TestClickOnlyRenderedRows(t *testing.T) {
	x := fixture(t, true)
	x = send(x, tea.WindowSizeMsg{Width: 120, Height: 6}) // header, then room for the heading and one row
	if strings.Contains(x.View(), "two") {
		t.Fatalf("overflow row rendered:\n%s", x.View())
	}
	for y := 0; y < 10; y++ {
		send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: y})
	}
	for _, r := range taken(t, x) {
		if r.Pane != "p1" {
			t.Errorf("click reached a row that is not rendered: %+v", r)
		}
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
	if v := x.View(); !strings.Contains(v, "Warm Compact is not running. Last") {
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
	out := renderView(store.Status{Started: now.Add(-65 * time.Minute), Default: model.Auto}, now, view{running: true, keys: true})
	if !strings.Contains(out, "up 1h05m · last event none") {
		t.Errorf("header:\n%s", out)
	}
	got := wrap(" ✗ open C:\\a\\very\\long\\path\\that\\will\\not\\fit\\on\\one\\line.json: denied", 20, "   ")
	if strings.Join(got, "|") != ` ✗ open|   C:\a\very\long\pa|   th\that\will\not\|   fit\on\one\line.j|   son: denied` {
		t.Errorf("wrap = %q", got)
	}
	for _, l := range got {
		if len([]rune(l)) > 20 {
			t.Errorf("wrapped line too long: %q", l)
		}
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
