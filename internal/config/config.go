// Package config reads the plugin's global limits from config.toml in its config dir.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/elbweb/herdr-warm-compact/internal/model"
)

type Config struct {
	Default        model.Setting // Auto or Off
	MinTokens      int
	Lead           time.Duration
	Warning        time.Duration
	HoldIfActive   time.Duration
	CompactTimeout time.Duration
	FiveMinuteTTL  bool
	Show           string // "armed" or "warnings"
	Panel          string // how the open-panel action opens the panel: "popup" or "tab"
	Instructions   string
}

const defaultInstructions = "The user stepped away and will resume later. Keep open decisions, the current task and its " +
	"next step, file paths and commands in play, and anything the user said they want."

func Defaults() Config {
	return Config{
		Default: model.Auto, MinTokens: 175000, Lead: 5 * time.Minute, Warning: time.Minute,
		HoldIfActive: time.Minute, CompactTimeout: 10 * time.Minute, Show: "armed", Panel: "popup",
		Instructions: defaultInstructions,
	}
}

type file struct {
	Default        *string `toml:"default"`
	MinTokens      *int    `toml:"min_tokens"`
	Lead           *string `toml:"lead"`
	Warning        *string `toml:"warning"`
	HoldIfActive   *string `toml:"hold_if_active"`
	CompactTimeout *string `toml:"compact_timeout"`
	FiveMinuteTTL  *bool   `toml:"five_minute_ttl"`
	Show           *string `toml:"show"`
	Panel          *string `toml:"panel"`
	Instructions   *string `toml:"instructions"`
}

func Parse(data []byte) (Config, error) {
	c := Defaults()
	var f file
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return c, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return c, fmt.Errorf("unknown key %q", und[0].String())
	}
	if f.Default != nil {
		switch *f.Default {
		case "auto":
			c.Default = model.Auto
		case "off":
			c.Default = model.Off
		default:
			return c, fmt.Errorf("default must be auto or off, not %q", *f.Default)
		}
	}
	if f.MinTokens != nil {
		if *f.MinTokens < 0 {
			return c, errors.New("min_tokens must not be negative")
		}
		c.MinTokens = *f.MinTokens
	}
	for _, d := range []struct {
		name string
		in   *string
		out  *time.Duration
	}{{"lead", f.Lead, &c.Lead}, {"warning", f.Warning, &c.Warning}, {"hold_if_active", f.HoldIfActive, &c.HoldIfActive}, {"compact_timeout", f.CompactTimeout, &c.CompactTimeout}} {
		if d.in == nil {
			continue
		}
		v, err := time.ParseDuration(*d.in)
		if err != nil || v <= 0 {
			return c, fmt.Errorf("%s must be a positive duration like \"5m\", not %q", d.name, *d.in)
		}
		*d.out = v
	}
	if c.Lead >= time.Hour {
		return c, errors.New("lead must be under 1h, the longest cache lifetime")
	}
	if f.FiveMinuteTTL != nil {
		c.FiveMinuteTTL = *f.FiveMinuteTTL
	}
	if f.Show != nil {
		if *f.Show != "armed" && *f.Show != "warnings" {
			return c, fmt.Errorf("show must be armed or warnings, not %q", *f.Show)
		}
		c.Show = *f.Show
	}
	if f.Panel != nil {
		if *f.Panel != "popup" && *f.Panel != "tab" {
			return c, fmt.Errorf("panel must be popup or tab, not %q", *f.Panel)
		}
		c.Panel = *f.Panel
	}
	if f.Instructions != nil {
		c.Instructions = *f.Instructions
	}
	return c, nil
}

// Load reads path; a missing file is the defaults.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), err
	}
	return Parse(b)
}

// Keys are the settings, in the order the panel lists them.
var Keys = []string{"default", "min_tokens", "lead", "warning", "hold_if_active", "compact_timeout", "five_minute_ttl", "show", "panel", "instructions"}

// Choices are the values a key with a fixed set cycles through; any other key is typed.
var Choices = map[string][]string{
	"default": {"auto", "off"}, "five_minute_ttl": {"false", "true"}, "show": {"armed", "warnings"}, "panel": {"popup", "tab"},
}

func dur(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Value is key's value as the file would spell it (a duration as "5m", a bool as "true").
func (c Config) Value(key string) string {
	switch key {
	case "default":
		return string(c.Default)
	case "min_tokens":
		return strconv.Itoa(c.MinTokens)
	case "lead":
		return dur(c.Lead)
	case "warning":
		return dur(c.Warning)
	case "hold_if_active":
		return dur(c.HoldIfActive)
	case "compact_timeout":
		return dur(c.CompactTimeout)
	case "five_minute_ttl":
		return strconv.FormatBool(c.FiveMinuteTTL)
	case "show":
		return c.Show
	case "panel":
		return c.Panel
	case "instructions":
		return c.Instructions
	}
	return ""
}

// Explicit reports which keys the file at path sets; a missing file sets none.
func Explicit(path string) (map[string]bool, error) {
	set := map[string]bool{}
	var m map[string]any
	if _, err := toml.DecodeFile(path, &m); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return set, nil
		}
		return set, err
	}
	for k := range m {
		set[k] = true
	}
	return set, nil
}

// Set writes key from the text the user typed (min_tokens and five_minute_ttl are converted), replacing only
// that key's line, or lines for a multi-line string, and keeping the rest of the file as the user wrote it.
// A value the plugin would reject is an error and nothing is written.
func Set(path, key, text string) error {
	if key != "instructions" {
		text = strings.TrimSpace(text)
	}
	var v any = text
	switch key {
	case "min_tokens":
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return fmt.Errorf("min_tokens must be a whole number, not %q", text)
		}
		v = n
	case "five_minute_ttl":
		b, err := strconv.ParseBool(text)
		if err != nil {
			return fmt.Errorf("five_minute_ttl must be true or false, not %q", text)
		}
		v = b
	}
	return edit(path, key, &v)
}

// Unset removes key from the file, so it takes its default.
func Unset(path, key string) error { return edit(path, key, nil) }

// SetDefault changes only the `default` line, keeping the rest of the file as the user wrote it.
func SetDefault(path string, s model.Setting) error {
	if s != model.Auto && s != model.Off {
		return fmt.Errorf("default must be auto or off")
	}
	return Set(path, "default", string(s))
}

// assignment is a top-level `key = value` line: the key, and the value's text.
var assignment = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*=\s*(.*)$`)

func edit(path, key string, value *any) error {
	known := false
	for _, k := range Keys {
		known = known || k == key
	}
	if !known {
		return fmt.Errorf("unknown key %q", key)
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	lines := strings.SplitAfter(string(b), "\n")
	start, end := -1, -1
	for i := 0; i < len(lines); i++ {
		m := assignment.FindStringSubmatch(strings.TrimRight(lines[i], "\r\n"))
		if m == nil {
			continue
		}
		last := i // a multi-line string runs to its closing quotes; a line inside one is never a key
		for _, q := range []string{`"""`, `'''`} {
			if strings.HasPrefix(m[2], q) && strings.Count(m[2], q) < 2 {
				for last+1 < len(lines) && !strings.Contains(lines[last+1], q) {
					last++
				}
				if last+1 < len(lines) {
					last++
				}
			}
		}
		if m[1] == key {
			start, end = i, last+1
			break
		}
		i = last
	}
	repl := ""
	if value != nil {
		var buf strings.Builder
		if err := toml.NewEncoder(&buf).Encode(map[string]any{key: *value}); err != nil {
			return err
		}
		repl = buf.String()
	}
	var out []string
	switch {
	case start >= 0:
		out = append(append(append(out, lines[:start]...), repl), lines[end:]...)
	case value == nil:
		return nil // not in the file: already the default
	default:
		out = lines
		if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
			out = append(out, "\n")
		}
		out = append(out, repl)
	}
	text := strings.Join(out, "")
	if _, err := Parse([]byte(text)); err != nil {
		if _, before := Parse(b); before != nil {
			return fmt.Errorf("config.toml already has an error; fix it by hand first: %v", before)
		}
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
