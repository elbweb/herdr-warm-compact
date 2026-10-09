// Package config reads the plugin's global limits from config.toml in its config dir.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
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
	Instructions   string
}

const defaultInstructions = "The owner stepped away and will resume later. Keep open decisions, the current task and its " +
	"next step, file paths and commands in play, and anything the owner said they want."

func Defaults() Config {
	return Config{
		Default: model.Auto, MinTokens: 175000, Lead: 5 * time.Minute, Warning: time.Minute,
		HoldIfActive: time.Minute, CompactTimeout: 10 * time.Minute, Show: "armed",
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

var defaultLine = regexp.MustCompile(`(?m)^default\s*=.*$`)

// SetDefault changes only the `default` line, keeping the rest of the file as the owner wrote it.
func SetDefault(path string, s model.Setting) error {
	if s != model.Auto && s != model.Off {
		return fmt.Errorf("default must be auto or off")
	}
	line := fmt.Sprintf("default = %q", string(s))
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var out []byte
	if defaultLine.Match(b) {
		out = defaultLine.ReplaceAll(b, []byte(line))
	} else {
		out = append([]byte(line+"\n"), b...)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
