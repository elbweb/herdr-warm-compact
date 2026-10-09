package config

import (
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
		c.HoldIfActive != time.Minute || c.FiveMinuteTTL || c.CompactTimeout != 10*time.Minute || c.Show != "armed" ||
		!strings.Contains(c.Instructions, "stepped away") {
		t.Fatalf("%+v", c)
	}
}

func TestParseOverridesSomeKeys(t *testing.T) {
	c, err := Parse([]byte("default = \"off\"\nmin_tokens = 90000\nlead = \"57m\"\nshow = \"warnings\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Default != model.Off || c.MinTokens != 90000 || c.Lead != 57*time.Minute || c.Show != "warnings" || c.Warning != time.Minute {
		t.Fatalf("%+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for _, bad := range []string{
		`default = "on"`, `show = "all"`, `lead = "60m"`, `lead = "soon"`, `warning = "0s"`, `min_tokens = -1`, `nope = 1`,
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
