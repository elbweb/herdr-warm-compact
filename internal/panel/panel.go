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
	"github.com/elbweb/herdr-warm-compact/internal/run"
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

func uptime(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// view is everything render needs beyond the status itself.
type view struct {
	cursor  int
	width   int
	height  int // 0 = unlimited
	running bool
	footer  string
}

func (v view) maxRows(n int) int {
	if v.height <= 0 {
		return n
	}
	lim := v.height - headerLines - 1
	if lim < 0 {
		lim = 0
	}
	if lim > n {
		lim = n
	}
	return lim
}

func render(s store.Status, cursor int, now time.Time, width int) string {
	return renderView(s, now, view{cursor: cursor, width: width, running: true})
}

func renderView(s store.Status, now time.Time, v view) string {
	var b strings.Builder
	line := func(l string) { b.WriteString(clip(l, v.width) + "\n") }
	cfg := "config ok"
	if s.ConfigError != "" {
		cfg = "✗ config: " + s.ConfigError
	}
	if !v.running {
		line(fmt.Sprintf(" Warm Compact is not running (last status from %s shown below; start it with the plugin's restart action)", s.Started.Format("2006-01-02 15:04")))
	} else {
		last := "none"
		if !s.LastEvent.IsZero() {
			last = now.Sub(s.LastEvent).Round(time.Second).String() + " ago"
		}
		line(fmt.Sprintf(" Warm Compact · default: %s · running %s · last event %s · %s", s.Default.Label(), uptime(now.Sub(s.Started)), last, cfg))
	}
	line(" ↑↓ select · enter/click cycle setting · c compact now · s skip this time · d change default · q close")
	line(fmt.Sprintf("   %-24s %-14s %7s %4s %-9s %s", "SESSION", "WHERE", "TOKENS", "TTL", "SETTING", "STATUS"))
	for i, r := range s.Rows[:v.maxRows(len(s.Rows))] {
		mark := " "
		if i == v.cursor {
			mark = "▶"
		}
		setting := r.Override.Label()
		if r.Override == model.Inherit {
			setting = "default"
		}
		line(fmt.Sprintf(" %s %-24s %-14s %7s %4s %-9s %s", mark, clip(r.Name, 24), clip(r.Workspace, 14), k(r.Tokens), ttl(r.TTL), setting, status(r, now)))
	}
	if len(s.Rows) == 0 {
		line("   no Claude sessions")
	}
	if v.footer != "" {
		line(v.footer)
	}
	return b.String()
}

type tick time.Time

type m struct {
	dir     string
	running func(string) bool
	s       store.Status
	err     error
	live    bool
	cursor  int
	pane    string // selected pane id, kept across reloads
	width   int
	height  int
	problem string // last write error
	notice  string
}

func newModel(dir string) m {
	return m{dir: dir, running: run.Running}.load()
}

func (x m) visible() int { return view{height: x.height}.maxRows(len(x.s.Rows)) }

func (x m) load() m {
	x.s, x.err = store.ReadStatus(filepath.Join(x.dir, "status.json"))
	x.live = x.running(x.dir)
	if x.pane != "" {
		for i, r := range x.s.Rows {
			if r.Pane == x.pane {
				x.cursor = i
			}
		}
	}
	if n := x.visible(); x.cursor >= n {
		x.cursor = n - 1
	}
	if x.cursor < 0 {
		x.cursor = 0
	}
	if x.cursor < len(x.s.Rows) {
		x.pane = x.s.Rows[x.cursor].Pane
	}
	return x
}

func (x m) Init() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tick(t) }) }

func (x m) setCursor(i int) m {
	x.cursor = i
	if i < len(x.s.Rows) {
		x.pane = x.s.Rows[i].Pane
	}
	return x
}

// act runs a write only while the resident is running; errors stay visible in the footer.
func (x m) act(f func() error) m {
	x.notice, x.problem = "", ""
	if !x.live {
		x.notice = " not running: nothing was sent"
		return x
	}
	if err := f(); err != nil {
		x.problem = err.Error()
	}
	return x
}

func (x m) request(kind string) m {
	if x.cursor >= len(x.s.Rows) {
		return x
	}
	pane := x.s.Rows[x.cursor].Pane
	return x.act(func() error {
		return store.WriteRequest(store.RequestsDir(x.dir), store.Request{Kind: kind, Pane: pane})
	})
}

func (x m) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tick:
		return x.load(), x.Init()
	case tea.WindowSizeMsg:
		x.width, x.height = msg.Width, msg.Height
		return x.load(), nil
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			if row := msg.Y - headerLines; row >= 0 && row < x.visible() {
				return x.setCursor(row).request("toggle"), nil
			}
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return x, tea.Quit
		case "up", "k":
			if x.cursor > 0 {
				x = x.setCursor(x.cursor - 1)
			}
		case "down", "j":
			if x.cursor < x.visible()-1 {
				x = x.setCursor(x.cursor + 1)
			}
		case "enter", " ":
			return x.request("toggle"), nil
		case "c":
			return x.request("compact_now"), nil
		case "s":
			return x.request("skip"), nil
		case "d":
			next := model.Off
			if x.s.Default == model.Off {
				next = model.Auto
			}
			return x.act(func() error { return config.SetDefault(filepath.Join(x.dir, "config.toml"), next) }), nil
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
	footer := ""
	if x.problem != "" {
		footer = " ✗ " + x.problem
	} else if x.notice != "" {
		footer = x.notice
	}
	return renderView(x.s, time.Now(), view{cursor: x.cursor, width: w, height: x.height, running: x.live, footer: footer})
}

func Run(dir string) error {
	_, err := tea.NewProgram(newModel(dir), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
