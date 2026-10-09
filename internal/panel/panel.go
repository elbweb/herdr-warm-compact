// Package panel is the plugin's terminal UI, hosted by herdr as a popup: every Claude session, its
// countdown and its setting. It runs only while open, reads status.json, and writes request files.
package panel

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/display"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/store"
)

const headerLines = 3

func k(n int) string { return fmt.Sprintf("%dk", (n+500)/1000) }

func ttl(d time.Duration) string {
	switch d {
	case time.Hour:
		return "1h"
	case 5 * time.Minute:
		return "5m"
	}
	return "?"
}

func status(r store.Row, now time.Time) string {
	if v := display.Token(display.View{Phase: r.Phase, Override: r.Override, Remaining: r.Deadline.Sub(now), Reason: r.Reason, Show: "armed", Flash: true}); v != nil && r.Phase != model.Quiet {
		return *v
	}
	if r.Reason != "" {
		return r.Reason
	}
	return "—"
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func render(s store.Status, cursor int, now time.Time, width int) string {
	var b strings.Builder
	cfg := "config ok"
	if s.ConfigError != "" {
		cfg = "✗ config: " + s.ConfigError
	}
	fmt.Fprintf(&b, " Warm Compact · default: %s · running %s · last event %s ago · %s\n",
		s.Default.Label(), now.Sub(s.Started).Round(time.Minute), now.Sub(s.LastEvent).Round(time.Second), cfg)
	b.WriteString(" ↑↓ select · enter/click cycle setting · c compact now · s skip this time · d change default · q close\n")
	fmt.Fprintf(&b, "   %-24s %-14s %7s %4s %-9s %s\n", "SESSION", "WHERE", "TOKENS", "TTL", "SETTING", "STATUS")
	for i, r := range s.Rows {
		mark := " "
		if i == cursor {
			mark = "▶"
		}
		setting := r.Override.Label()
		if r.Override == model.Inherit {
			setting = "default"
		}
		line := fmt.Sprintf(" %s %-24s %-14s %7s %4s %-9s %s", mark, clip(r.Name, 24), clip(r.Workspace, 14), k(r.Tokens), ttl(r.TTL), setting, status(r, now))
		b.WriteString(clip(line, width) + "\n")
	}
	if len(s.Rows) == 0 {
		b.WriteString("   no Claude sessions\n")
	}
	return b.String()
}

type tick time.Time

type m struct {
	dir    string
	s      store.Status
	err    error
	cursor int
	width  int
}

func (x m) load() m {
	x.s, x.err = store.ReadStatus(filepath.Join(x.dir, "status.json"))
	if x.cursor >= len(x.s.Rows) {
		x.cursor = len(x.s.Rows) - 1
	}
	if x.cursor < 0 {
		x.cursor = 0
	}
	return x
}

func (x m) Init() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tick(t) }) }

func (x m) request(kind, value string) {
	if x.cursor < len(x.s.Rows) {
		store.WriteRequest(store.RequestsDir(x.dir), store.Request{Kind: kind, Pane: x.s.Rows[x.cursor].Pane, Value: value})
	}
}

func (x m) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tick:
		return x.load(), x.Init()
	case tea.WindowSizeMsg:
		x.width = msg.Width
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			if row := msg.Y - headerLines; row >= 0 && row < len(x.s.Rows) {
				x.cursor = row
				x.request("toggle", "")
			}
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return x, tea.Quit
		case "up", "k":
			if x.cursor > 0 {
				x.cursor--
			}
		case "down", "j":
			if x.cursor < len(x.s.Rows)-1 {
				x.cursor++
			}
		case "enter", " ":
			x.request("toggle", "")
		case "c":
			x.request("compact_now", "")
		case "s":
			x.request("skip", "")
		case "d":
			next := model.Off
			if x.s.Default == model.Off {
				next = model.Auto
			}
			config.SetDefault(filepath.Join(x.dir, "config.toml"), next)
		}
	}
	return x, nil
}

func (x m) View() string {
	if x.err != nil {
		return fmt.Sprintf(" Warm Compact is not running (no status: %v)\n q close\n", x.err)
	}
	w := x.width
	if w == 0 {
		w = 120
	}
	return render(x.s, x.cursor, time.Now(), w)
}

func Run(dir string) error {
	_, err := tea.NewProgram(m{dir: dir}.load(), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
