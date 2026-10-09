package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/elbweb/herdr-warm-compact/internal/config"
)

func typ(x m, keys ...tea.KeyMsg) m {
	for _, k := range keys {
		x = send(x, k)
	}
	return x
}

func backspaces(n int) []tea.KeyMsg {
	var k []tea.KeyMsg
	for i := 0; i < n; i++ {
		k = append(k, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	return k
}

func file(t *testing.T, x m) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(x.dir, "config.toml"))
	return string(b)
}

func settingsOf(t *testing.T, running bool) m {
	t.Helper()
	x := send(fixture(t, running), tea.KeyMsg{Type: tea.KeyTab})
	if x.tab != settingsTab || !strings.Contains(x.View(), "tab sessions") {
		t.Fatalf("tab did not open settings:\n%s", x.View())
	}
	return x
}

// row is the line showing setting key, without the cursor mark.
func row(x m, key string) string {
	for _, l := range strings.Split(x.View(), "\n") {
		l = strings.TrimPrefix(strings.TrimSpace(l), "▶")
		if f := strings.Fields(l); len(f) > 0 && f[0] == key {
			return l
		}
	}
	return ""
}

func TestSettingsListsEveryKeyWithDefaults(t *testing.T) {
	x := settingsOf(t, true)
	v := x.View()
	if !strings.Contains(v, "Warm Compact · settings") {
		t.Errorf("title:\n%s", v)
	}
	for _, k := range config.Keys {
		if row(x, k) == "" {
			t.Errorf("no %s:\n%s", k, v)
		}
	}
	if f := strings.Fields(row(x, "lead")); len(f) != 3 || f[1] != "5m" || f[2] != "(default)" || !strings.Contains(v, "     The user stepped away") {
		t.Errorf("values:\n%s", v)
	}
	if send(x, tea.KeyMsg{Type: tea.KeyTab}).tab != sessionsTab {
		t.Error("tab did not go back to sessions")
	}
}

func TestSettingsChoiceCycles(t *testing.T) {
	x := settingsOf(t, true)
	x = send(x, key("enter")) // default: auto -> off
	if f := file(t, x); f != "default = \"off\"\n" {
		t.Fatalf("config.toml = %q", f)
	}
	if r := row(x, "default"); !strings.Contains(r, "off") || strings.Contains(r, "(default)") {
		t.Errorf("a set value still says default:\n%s", x.View())
	}
	x = send(x, key("r"))
	if c, _ := config.Load(filepath.Join(x.dir, "config.toml")); c.Default != config.Defaults().Default || strings.Contains(file(t, x), "default") {
		t.Fatalf("reset left %q", file(t, x))
	}
}

func TestSettingsEditSavesValidAndRefusesInvalid(t *testing.T) {
	x := settingsOf(t, true)
	x = typ(x, key("j"), key("j"), key("enter")) // lead
	if x.st.editing != "lead" {
		t.Fatalf("editing %q", x.st.editing)
	}
	x = typ(x, backspaces(2)...)
	x = typ(x, key("s"), key("o"), key("o"), key("n"), key("enter"))
	if x.st.editing != "lead" || !strings.Contains(x.View(), "✗ lead must be") || file(t, x) != "" {
		t.Fatalf("invalid value: editing %q, file %q:\n%s", x.st.editing, file(t, x), x.View())
	}
	x = typ(x, backspaces(4)...)
	x = typ(x, key("7"), key("m"), key("enter"))
	if f := strings.Fields(row(x, "lead")); x.st.editing != "" || file(t, x) != "lead = \"7m\"\n" || len(f) != 2 || f[1] != "7m" {
		t.Fatalf("valid value: editing %q, file %q:\n%s", x.st.editing, file(t, x), x.View())
	}
	x = typ(x, key("enter"), key("9"), tea.KeyMsg{Type: tea.KeyEsc})
	if x.st.editing != "" || file(t, x) != "lead = \"7m\"\n" {
		t.Fatalf("esc did not cancel: %q", file(t, x))
	}
}

func TestEditorKeepsItsKeys(t *testing.T) {
	x := settingsOf(t, true)
	for range config.Keys[5:] { // compact_timeout: the longest typed key, so its hint is the widest
		x = send(x, key("k"))
	}
	x.st.cursor = 5
	x = send(x, key("enter"))
	if x.st.editing != "compact_timeout" || !strings.Contains(x.View(), "enter save · esc cancel") {
		t.Fatalf("editing %q:\n%s", x.st.editing, x.View())
	}
	x = typ(x, key("q"), tea.KeyMsg{Type: tea.KeyTab}, key("?"))
	x = send(x, tick{}) // the once-a-second reload
	if x.st.editing != "compact_timeout" || x.tab != settingsTab || x.keys || !strings.HasSuffix(x.st.input.Value(), "q?") {
		t.Fatalf("keys leaked out of the editor: editing %q tab %d keys %v value %q", x.st.editing, x.tab, x.keys, x.st.input.Value())
	}
	x = send(x, tea.KeyMsg{Type: tea.KeyEsc})
	if x.tab != settingsTab || x.st.editing != "" {
		t.Fatal("esc in the editor did more than cancel")
	}
	if send(x, tea.KeyMsg{Type: tea.KeyEsc}).tab != sessionsTab {
		t.Error("esc in settings did not go back to sessions")
	}
}

func TestSettingsWithABrokenFile(t *testing.T) {
	x := fixture(t, true)
	os.WriteFile(filepath.Join(x.dir, "config.toml"), []byte("lead = \"7m\"\nnope = 1\n"), 0o600)
	x = send(x.load(), tea.KeyMsg{Type: tea.KeyTab})
	x = typ(x, key("enter"), key("r"))
	if v := x.View(); !strings.Contains(v, "✗ config.toml") || !strings.Contains(v, "nope") || strings.Contains(v, "min_tokens") {
		t.Errorf("view:\n%s", v)
	}
	if file(t, x) != "lead = \"7m\"\nnope = 1\n" || x.st.editing != "" {
		t.Errorf("a broken file was edited: %q", file(t, x))
	}
}

func TestSettingsUnchangedDefaultIsNotWritten(t *testing.T) {
	x := settingsOf(t, true)
	x = typ(x, key("j"), key("enter"), key("enter")) // min_tokens, saved as it was
	if x.st.editing != "" || file(t, x) != "" {
		t.Fatalf("editing %q, file %q", x.st.editing, file(t, x))
	}
}

func TestSettingsInstructionsMultiLine(t *testing.T) {
	x := settingsOf(t, true)
	for range config.Keys[1:] {
		x = send(x, key("j"))
	}
	x = send(x, key("enter"))
	if x.st.editing != "instructions" {
		t.Fatalf("editing %q", x.st.editing)
	}
	x = typ(x, tea.KeyMsg{Type: tea.KeyEnter}, key("B"), key("e"), tea.KeyMsg{Type: tea.KeyCtrlS})
	c, err := config.Load(filepath.Join(x.dir, "config.toml"))
	if x.st.editing != "" || err != nil || !strings.HasSuffix(c.Instructions, "want.\nBe") {
		t.Fatalf("instructions = %q, %v, editing %q", c.Instructions, err, x.st.editing)
	}
}

func TestSettingsNotRunningWritesNothing(t *testing.T) {
	x := settingsOf(t, false)
	x = send(x, key("enter"))
	if file(t, x) != "" || !strings.Contains(x.View(), "not running: nothing was sent") {
		t.Fatalf("file %q:\n%s", file(t, x), x.View())
	}
}

func TestSettingsClickAndWidth(t *testing.T) {
	x := settingsOf(t, true)
	x = send(x, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Y: lineOf(t, x, "panel ")})
	if file(t, x) != "panel = \"tab\"\n" {
		t.Fatalf("click on panel: %q", file(t, x))
	}
	x = send(x, tea.WindowSizeMsg{Width: 120, Height: 40})
	x.st.cursor = 0
	// the list, editing a typed setting (lead), and editing instructions
	for _, at := range []m{x, typ(x, key("j"), key("j"), key("enter")), typ(x, key("k"), key("enter"))} {
		for _, l := range strings.Split(at.View(), "\n") {
			if ansi.StringWidth(l) > width {
				t.Errorf("line wider than %d: %q", width, l)
			}
		}
	}
}
