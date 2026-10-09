package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Default != model.Auto || c.MinTokens != 175000 || c.Lead != 5*time.Minute || c.Warning != time.Minute ||
		c.HoldIfActive != time.Minute || c.FiveMinuteTTL || c.CompactTimeout != 10*time.Minute || c.Show != "armed" || c.Panel != "popup" ||
		!strings.Contains(c.Instructions, "stepped away") {
		t.Fatalf("%+v", c)
	}
}

func TestParseOverridesSomeKeys(t *testing.T) {
	c, err := Parse([]byte("default = \"off\"\nmin_tokens = 90000\nlead = \"57m\"\nshow = \"warnings\"\npanel = \"tab\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Default != model.Off || c.MinTokens != 90000 || c.Lead != 57*time.Minute || c.Show != "warnings" || c.Panel != "tab" || c.Warning != time.Minute {
		t.Fatalf("%+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for _, bad := range []string{
		`default = "on"`, `show = "all"`, `lead = "60m"`, `lead = "soon"`, `warning = "0s"`, `min_tokens = -1`, `nope = 1`, `panel = "split"`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%s) should fail", bad)
		}
	}
}

func TestLoadMissingIsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil || c.MinTokens != 175000 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSetReplacesOnlyItsKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("# mine\nlead = \"7m\" # keep me\ninstructions = \"\"\"\nold\ntext\n\"\"\"\nshow = \"warnings\"\n"), 0o600)
	if err := Set(p, "instructions", "Say \"hi\".\nThen stop."); err != nil {
		t.Fatal(err)
	}
	if err := Set(p, "min_tokens", "90000"); err != nil {
		t.Fatal(err)
	}
	if err := Set(p, "five_minute_ttl", "true"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Instructions != "Say \"hi\".\nThen stop." || c.MinTokens != 90000 || !c.FiveMinuteTTL || c.Lead != 7*time.Minute || c.Show != "warnings" {
		t.Fatalf("%+v, %v", c, err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), "# mine\nlead = \"7m\" # keep me\n") || strings.Contains(string(b), "old") {
		t.Fatalf("file:\n%s", b)
	}
	if err := Unset(p, "instructions"); err != nil {
		t.Fatal(err)
	}
	if set, _ := Explicit(p); set["instructions"] || !set["lead"] || !set["min_tokens"] {
		t.Fatalf("explicit = %v", set)
	}
	if c, _ := Load(p); c.Instructions != Defaults().Instructions {
		t.Fatal("unset did not restore the default")
	}
}

func TestSetSkipsTextInsideMultiLineStrings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	orig := "instructions = '''\nlead = \"1m\" is not a key here\n'''\nshow = \"warnings\""
	os.WriteFile(p, []byte(orig), 0o600) // and no trailing newline
	if err := Set(p, "lead", " 7m "); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != orig+"\nlead = \"7m\"\n" {
		t.Fatalf("file:\n%s", b)
	}
	if err := Set(p, "show", "armed"); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(p); c.Lead != 7*time.Minute || c.Show != "armed" || !strings.Contains(c.Instructions, "is not a key") {
		t.Fatalf("%+v", c)
	}
}

func TestSetOnABrokenFileSaysSo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("nope = 1\n"), 0o600)
	if err := Set(p, "lead", "7m"); err == nil || !strings.Contains(err.Error(), "already has an error") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetRejectsWithoutWriting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("lead = \"7m\"\n"), 0o600)
	for _, bad := range [][2]string{{"lead", "soon"}, {"lead", "2h"}, {"min_tokens", "lots"}, {"panel", "split"}, {"nope", "1"}} {
		if err := Set(p, bad[0], bad[1]); err == nil {
			t.Errorf("Set(%s, %q) should fail", bad[0], bad[1])
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "lead = \"7m\"\n" {
		t.Fatalf("file changed: %q", b)
	}
}

func TestValueSpellsLikeTheFile(t *testing.T) {
	c := Defaults()
	for k, want := range map[string]string{"lead": "5m", "warning": "1m", "compact_timeout": "10m", "min_tokens": "175000", "five_minute_ttl": "false", "panel": "popup"} {
		if got := c.Value(k); got != want {
			t.Errorf("Value(%s) = %q, want %q", k, got, want)
		}
	}
	c.Lead = 90 * time.Minute
	if c.Value("lead") != "1h30m" {
		t.Error(c.Value("lead"))
	}
	for _, k := range Keys {
		if _, err := Parse([]byte(fmt.Sprintf("%s = %q\n", k, Defaults().Value(k)))); err != nil && k != "min_tokens" && k != "five_minute_ttl" {
			t.Errorf("default %s does not round-trip: %v", k, err)
		}
	}
}

func TestSetDefaultRewritesOrAdds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := SetDefault(p, model.Off); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("# mine\ndefault = \"off\"\nmin_tokens = 1\n"), 0o600)
	if err := SetDefault(p, model.Auto); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "# mine\ndefault = \"auto\"\nmin_tokens = 1\n" {
		t.Fatalf("%q", b)
	}
}
