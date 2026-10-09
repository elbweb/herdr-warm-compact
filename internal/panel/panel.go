// Package panel is the plugin's terminal UI, hosted by herdr as a popup or a tab: every Claude session, its
// countdown and its setting. It runs only while open, reads status.json, and writes request files.
package panel

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/display"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/run"
	"github.com/elbweb/herdr-warm-compact/internal/store"
)

// width is the panel's fixed text width: Collie wraps a pane at about 37 columns on a phone and mirrors
// the pane's desktop width, so the panel never uses more than this, whatever its window.
const width = 36

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

// cols measures text in terminal columns: a CJK character takes two. East Asian ambiguous symbols (▶ · …)
// count as one whatever the console's code page, as the terminals and Collie draw them.
var cols = &runewidth.Condition{EastAsianWidth: false}

func clip(s string, n int) string {
	if cols.StringWidth(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return cols.Truncate(s, n, "…")
}

// flat puts text from a session (a name, a workspace, a reason) on one line.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// wrap breaks s into lines of at most n columns at spaces, each continuation indented by indent.
func wrap(s string, n int, indent string) []string {
	var out []string
	lead := s[:len(s)-len(strings.TrimLeft(s, " "))]
	line := ""
	var words []string
	for _, w := range strings.Fields(s) { // a word too long for any line (a path) is split
		for room := n - cols.StringWidth(indent); room > 0 && cols.StringWidth(w) > room; {
			head := cols.Truncate(w, room, "")
			if head == "" { // a two-column character in a one-column room
				head = string([]rune(w)[:1])
			}
			words, w = append(words, head), w[len(head):]
		}
		words = append(words, w)
	}
	for _, w := range words {
		switch {
		case line == "":
			line = lead + w
		case cols.StringWidth(line)+1+cols.StringWidth(w) <= n:
			line += " " + w
		default:
			out = append(out, line)
			line = indent + w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	for i := range out {
		out[i] = clip(out[i], n)
	}
	return out
}

func uptime(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

var keyHelp = []string{
	" ↑↓ select · enter/tap cycle",
	" c compact now · s skip this time",
	" d change default · tab settings",
	" ? hide keys · q close",
}

// view is everything render needs beyond the status itself.
type view struct {
	cursor  int
	width   int // the window's width; the panel uses at most the width constant
	height  int // 0 = unlimited
	running bool
	keys    bool // the full key list is shown
	footer  string
}

// w is the width the panel draws in: the width constant, or the window if narrower.
func (v view) w() int {
	if v.width > 0 && v.width < width {
		return v.width
	}
	return width
}

func splitLines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// screen is a rendered panel: its lines, and for each line the row it belongs to (-1 = none).
type screen struct {
	lines []string
	row   []int
}

func (sc *screen) add(row int, l string) {
	sc.lines = append(sc.lines, l)
	sc.row = append(sc.row, row)
}

// top is the header both views share: the title (or that the plugin is not running), a config error, and the
// one-line help or, with ?, every key.
func top(s store.Status, now time.Time, v view, title, help string) (head screen) {
	w := v.w()
	if !v.running {
		for _, l := range wrap(fmt.Sprintf(" Warm Compact is not running. Last status from %s; start it with the plugin's restart action.", s.Started.Format("2006-01-02 15:04")), w, " ") {
			head.add(-1, l)
		}
	} else {
		head.add(-1, clip(title, w))
	}
	if s.ConfigError != "" {
		for _, l := range wrap(" ✗ config: "+s.ConfigError, w, "   ") {
			head.add(-1, l)
		}
	}
	if v.keys {
		for _, l := range keyHelp {
			head.add(-1, clip(l, w))
		}
		if v.running {
			last := "none"
			if !s.LastEvent.IsZero() {
				last = now.Sub(s.LastEvent).Round(time.Second).String() + " ago"
			}
			head.add(-1, clip(" up "+uptime(now.Sub(s.Started))+" · last event "+last, w))
		}
	} else {
		head.add(-1, clip(help, w))
	}
	return head
}

func layout(s store.Status, now time.Time, v view) (head, body, foot screen) {
	w := v.w()
	head = top(s, now, v, " Warm Compact · default "+s.Default.Label(), " ? keys · tab settings · q close")
	ws, first := "", true
	for i, r := range s.Rows {
		if first || r.Workspace != ws {
			ws, first = r.Workspace, false
			body.add(-1, "")
			body.add(-1, clip(" "+flat(ws), w))
		}
		mark := " "
		if i == v.cursor {
			mark = "▶"
		}
		tok := k(r.Tokens)
		name := clip(flat(r.Name), w-3-1-len(tok))
		gap := w - 3 - cols.StringWidth(name) - len(tok)
		if gap < 1 {
			gap = 1
		}
		body.add(i, clip(" "+mark+" "+name+strings.Repeat(" ", gap)+tok, w))
		body.add(i, clip(fmt.Sprintf("     %s · %s · %s", r.Override.Label(), ttl(r.TTL), flat(status(r, now))), w))
	}
	if len(s.Rows) == 0 {
		body.add(-1, "")
		body.add(-1, "   no Claude sessions")
	}
	if v.footer != "" {
		foot.add(-1, "")
		for _, l := range wrap(v.footer, w, "   ") {
			foot.add(-1, l)
		}
	}
	return head, body, foot
}

// frame is the sessions view fitted to the window.
func frame(s store.Status, now time.Time, v view) screen {
	head, body, foot := layout(s, now, v)
	return fit(head, body, foot, v)
}

// fit fits a view to the window height, scrolling the body to keep the cursor's row in view.
func fit(head, body, foot screen, v view) screen {
	room := len(body.lines)
	if v.height > 0 {
		room = v.height - len(head.lines) - len(foot.lines)
		if room < 0 {
			room = 0
		}
	}
	off := 0
	if room < len(body.lines) {
		last := -1
		for i, r := range body.row {
			if r == v.cursor {
				last = i
			}
		}
		if last >= room {
			off = last - room + 1
		}
		if off+room > len(body.lines) {
			off = len(body.lines) - room
		}
	} else {
		room = len(body.lines)
	}
	out := head
	for i := off; i < off+room; i++ {
		out.add(body.row[i], body.lines[i])
	}
	for i := range foot.lines {
		out.add(-1, foot.lines[i])
	}
	if v.height > 0 && len(out.lines) > v.height { // header and footer alone overflow: cut the bottom here,
		out.lines, out.row = out.lines[:v.height], out.row[:v.height] // or bubbletea cuts the top and clicks miss
	}
	return out
}

func render(s store.Status, cursor int, now time.Time, w int) string {
	return renderView(s, now, view{cursor: cursor, width: w, running: true})
}

// renderView has no trailing newline: bubbletea counts one as an extra line and, on a full window, drops the
// top line, which moves every line up one and sends clicks to the wrong row.
func renderView(s store.Status, now time.Time, v view) string {
	return strings.Join(frame(s, now, v).lines, "\n")
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
	keys    bool   // the full key list is shown
	problem string // last write error
	notice  string
	tab     int // sessionsTab or settingsTab
	st      settings
}

const (
	sessionsTab = iota
	settingsTab
)

func newModel(dir string) m {
	return m{dir: dir, running: run.Running}.load()
}

func (x m) view() view {
	footer := ""
	if x.problem != "" {
		footer = " ✗ " + x.problem
	} else if x.notice != "" {
		footer = x.notice
	}
	return view{cursor: x.cursor, width: x.width, height: x.height, running: x.live, keys: x.keys, footer: footer}
}

// screen is the current view fitted to the window.
func (x m) screen(now time.Time) screen {
	v := x.view()
	if x.tab == settingsTab {
		v.cursor = x.st.cursor
		head, body, foot := settingsLayout(x.st, x.s, now, v)
		return fit(head, body, foot, v)
	}
	return frame(x.s, now, v)
}

// rowAt is the row (a session, or a setting) drawn on screen line y, or -1.
func (x m) rowAt(y int) int {
	sc := x.screen(time.Now())
	if y < 0 || y >= len(sc.row) {
		return -1
	}
	return sc.row[y]
}

func (x m) load() m {
	x.s, x.err = store.ReadStatus(filepath.Join(x.dir, "status.json"))
	x.live = x.running(x.dir)
	x = x.loadSettings()
	if x.pane != "" {
		for i, r := range x.s.Rows {
			if r.Pane == x.pane {
				x.cursor = i
			}
		}
	}
	if x.cursor >= len(x.s.Rows) {
		x.cursor = len(x.s.Rows) - 1
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
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft && x.st.editing == "" {
			if row := x.rowAt(msg.Y); row >= 0 {
				if x.tab == settingsTab {
					x.st.cursor = row
					return x.activate()
				}
				return x.setCursor(row).request("toggle"), nil
			}
		}
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return x, tea.Quit
		}
		if x.st.editing != "" {
			return x.editKey(msg)
		}
		switch msg.String() {
		case "esc":
			if x.tab == settingsTab {
				x.tab, x.notice, x.problem = sessionsTab, "", ""
				return x, nil
			}
			return x, tea.Quit
		case "q":
			return x, tea.Quit
		case "?":
			x.keys = !x.keys
			return x, nil
		case "tab", "shift+tab":
			x.tab = 1 - x.tab
			x.notice, x.problem = "", ""
			return x, nil
		}
		if x.tab == settingsTab {
			return x.settingsKey(msg)
		}
		switch msg.String() {
		case "up", "k":
			if x.cursor > 0 {
				x = x.setCursor(x.cursor - 1)
			}
		case "down", "j":
			if x.cursor < len(x.s.Rows)-1 {
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
	default:
		if x.st.editing != "" { // the editor's cursor blink
			var cmd tea.Cmd
			if x.st.editing == "instructions" {
				x.st.area, cmd = x.st.area.Update(msg)
			} else {
				x.st.input, cmd = x.st.input.Update(msg)
			}
			return x, cmd
		}
	}
	return x, nil
}

func (x m) View() string {
	if x.err != nil {
		w := width
		if x.width > 0 && x.width < w {
			w = x.width
		}
		return strings.Join(append(wrap(fmt.Sprintf(" Warm Compact is not running (no status: %v)", x.err), w, "   "), " q close"), "\n")
	}
	return strings.Join(x.screen(time.Now()).lines, "\n")
}

// fps caps bubbletea's redraw timer (default 60 a second), which is most of an idle panel's CPU; the view
// changes once a second, and 10 keeps a key press feeling immediate.
const fps = 10

func Run(dir string) error {
	_, err := tea.NewProgram(newModel(dir), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithFPS(fps)).Run()
	return err
}
