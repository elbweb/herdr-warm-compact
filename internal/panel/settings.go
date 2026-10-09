package panel

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/store"
)

// settings is the panel's second view: config.toml's settings, edited in place.
type settings struct {
	cfg     config.Config
	set     map[string]bool // keys the file sets; the rest are at their default
	err     error           // config.toml does not parse: the view shows why and edits nothing
	cursor  int
	editing string // the key being typed, "" when none
	input   textinput.Model
	area    textarea.Model // for instructions, which may run over several lines
}

func (x m) configPath() string { return filepath.Join(x.dir, "config.toml") }

func (x m) loadSettings() m {
	var err error
	x.st.cfg, x.st.err = config.Load(x.configPath())
	if x.st.set, err = config.Explicit(x.configPath()); x.st.err == nil {
		x.st.err = err
	}
	return x
}

func settingsLayout(st settings, s store.Status, now time.Time, v view) (head, body, foot screen) {
	w := v.w()
	head = top(s, now, v, " Warm Compact · settings", " tab sessions · enter edit · r reset")
	body.add(-1, "")
	if st.err != nil { // its values would be guesses, and every write would fail
		for _, l := range wrap(" ✗ config.toml: "+st.err.Error(), w, "   ") {
			body.add(-1, l)
		}
		body.add(-1, "")
		for _, l := range wrap(" Fix it by hand (herdr plugin config-dir herdr.warm-compact); this view edits again once it parses.", w, " ") {
			body.add(-1, l)
		}
		return head, body, foot
	}
	for i, key := range config.Keys {
		mark := " "
		if i == st.cursor {
			mark = "▶"
		}
		val, tag := st.cfg.Value(key), ""
		if !st.set[key] {
			tag = "(default)"
		}
		if key == "instructions" {
			body.add(i, clip(fmt.Sprintf(" %s %-22s %s", mark, key, tag), w))
			for j, l := range wrap("     "+flat(val), w, "     ") {
				if j == 3 {
					break
				}
				body.add(i, l)
			}
			continue
		}
		body.add(i, clip(strings.TrimRight(fmt.Sprintf(" %s %-15s %-6s %s", mark, key, val, tag), " "), w))
	}
	if st.editing != "" {
		foot.add(-1, "")
		save := "enter"
		if st.editing == "instructions" {
			save = "ctrl+s"
		}
		foot.add(-1, clip(" "+st.editing+":", w))
		foot.add(-1, clip(" "+save+" save · esc cancel", w))
		widget := st.input.View()
		if st.editing == "instructions" {
			widget = st.area.View()
		}
		for _, l := range splitLines(widget) { // styled by bubbles; sized to fit, so never clipped
			foot.add(-1, " "+l)
		}
	}
	if v.footer != "" {
		foot.add(-1, "")
		for _, l := range wrap(v.footer, w, "   ") {
			foot.add(-1, l)
		}
	}
	return head, body, foot
}

// activate is enter or a tap on a setting: a choice moves to its next value, anything else opens an editor.
func (x m) activate() (m, tea.Cmd) {
	if x.st.err != nil {
		return x, nil
	}
	key := config.Keys[x.st.cursor]
	if opts, ok := config.Choices[key]; ok {
		next := opts[(slices.Index(opts, x.st.cfg.Value(key))+1)%len(opts)]
		return x.act(func() error { return config.Set(x.configPath(), key, next) }).loadSettings(), nil
	}
	x.notice, x.problem = "", ""
	x.st.editing = key
	w := x.view().w() - 3
	if key == "instructions" {
		x.st.area = textarea.New()
		x.st.area.ShowLineNumbers, x.st.area.Prompt, x.st.area.CharLimit = false, "", 0
		// plain styles: the default ones use adaptive colours, which make lipgloss ask the terminal for its
		// background, and herdr's pane or a phone mirror may never answer
		x.st.area.FocusedStyle, x.st.area.BlurredStyle = textarea.Style{}, textarea.Style{}
		x.st.area.SetWidth(w)
		x.st.area.SetHeight(6)
		x.st.area.SetValue(x.st.cfg.Value(key))
		return x, x.st.area.Focus()
	}
	x.st.input = textinput.New()
	x.st.input.Prompt, x.st.input.Width, x.st.input.CharLimit = "", w-1, 0
	x.st.input.SetValue(x.st.cfg.Value(key))
	return x, x.st.input.Focus()
}

// editKey routes a key to the open editor: save, cancel, or typing.
func (x m) editKey(msg tea.KeyMsg) (m, tea.Cmd) {
	multi := x.st.editing == "instructions"
	switch {
	case msg.String() == "esc":
		x.st.editing, x.problem, x.notice = "", "", ""
		return x, nil
	case msg.String() == "ctrl+s" || (msg.String() == "enter" && !multi):
		val := x.st.input.Value()
		if multi {
			val = x.st.area.Value()
		}
		key := x.st.editing
		if !x.st.set[key] && val == x.st.cfg.Value(key) { // unchanged: keep following the default
			x.st.editing, x.problem, x.notice = "", "", ""
			return x, nil
		}
		x = x.act(func() error { return config.Set(x.configPath(), key, val) })
		if x.problem == "" && x.notice == "" {
			x.st.editing = ""
		}
		return x.loadSettings(), nil
	}
	var cmd tea.Cmd
	if multi {
		x.st.area, cmd = x.st.area.Update(msg)
	} else {
		x.st.input, cmd = x.st.input.Update(msg)
	}
	return x, cmd
}

func (x m) settingsKey(msg tea.KeyMsg) (m, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if x.st.cursor > 0 {
			x.st.cursor--
		}
	case "down", "j":
		if x.st.cursor < len(config.Keys)-1 {
			x.st.cursor++
		}
	case "enter", " ":
		return x.activate()
	case "r":
		if x.st.err != nil {
			return x, nil
		}
		key := config.Keys[x.st.cursor]
		return x.act(func() error { return config.Unset(x.configPath(), key) }).loadSettings(), nil
	}
	return x, nil
}
