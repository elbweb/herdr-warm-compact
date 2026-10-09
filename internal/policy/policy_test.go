package policy

import (
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/transcript"
)

var t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

func big() transcript.Facts { return transcript.Facts{At: t0, Tokens: 200000, TTL: time.Hour} }

func TestArmsBigSessionAt55Minutes(t *testing.T) {
	d := Decide(config.Defaults(), model.Inherit, big(), t0.Add(time.Second))
	if !d.Armed || !d.Deadline.Equal(t0.Add(55*time.Minute)) || d.Effective != model.Auto {
		t.Fatalf("%+v", d)
	}
}

func TestReasons(t *testing.T) {
	cfg := config.Defaults()
	small := big()
	small.Tokens = 1000
	five := big()
	five.TTL = 5 * time.Minute
	unknown := big()
	unknown.TTL = 0
	compacted := big()
	compacted.Compacted = true
	offCfg := cfg
	offCfg.Default = model.Off
	cases := []struct {
		name string
		cfg  config.Config
		ov   model.Setting
		f    transcript.Facts
		now  time.Time
		want string
	}{
		{"override off", cfg, model.Off, big(), t0, "off"},
		{"default off", offCfg, model.Inherit, big(), t0, "off"},
		{"small auto", cfg, model.Inherit, small, t0, "below threshold"},
		{"five minute", cfg, model.On, five, t0, "5-minute cache"},
		{"unknown ttl", cfg, model.Inherit, unknown, t0, "cache lifetime unknown"},
		{"compacted", cfg, model.Inherit, compacted, t0, "compacted"},
		{"expired", cfg, model.Inherit, big(), t0.Add(61 * time.Minute), "cache expired"},
		{"too close", cfg, model.Inherit, big(), t0.Add(59*time.Minute + 30*time.Second), "too close to expiry"},
	}
	for _, c := range cases {
		if d := Decide(c.cfg, c.ov, c.f, c.now); d.Armed || d.Reason != c.want {
			t.Errorf("%s: %+v, want reason %q", c.name, d, c.want)
		}
	}
}

func TestOnIgnoresSizeAndOffDefaultCanBeOverridden(t *testing.T) {
	cfg := config.Defaults()
	cfg.Default = model.Off
	small := big()
	small.Tokens = 10
	if d := Decide(cfg, model.On, small, t0); !d.Armed {
		t.Fatalf("%+v", d)
	}
	if d := Decide(cfg, model.Auto, big(), t0); !d.Armed {
		t.Fatalf("%+v", d)
	}
}

func TestFiveMinuteAllowedWhenConfigured(t *testing.T) {
	cfg := config.Defaults()
	cfg.FiveMinuteTTL = true
	cfg.Lead = time.Minute
	cfg.Warning = 30 * time.Second
	f := big()
	f.TTL = 5 * time.Minute
	d := Decide(cfg, model.Inherit, f, t0)
	if !d.Armed || !d.Deadline.Equal(t0.Add(4*time.Minute)) {
		t.Fatalf("%+v", d)
	}
}

func TestDeadlineAlreadyPassedButStillWarmIsPushedOut(t *testing.T) {
	now := t0.Add(56 * time.Minute) // plugin started late
	d := Decide(config.Defaults(), model.Inherit, big(), now)
	if !d.Armed || !d.Deadline.Equal(now.Add(time.Minute)) {
		t.Fatalf("%+v", d)
	}
}
