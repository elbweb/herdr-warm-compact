// Package policy decides, without I/O, whether a session is armed for compaction and when.
package policy

import (
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/transcript"
)

func Effective(def, override model.Setting) model.Setting {
	if override == model.Inherit {
		return def
	}
	return override
}

// Eligible answers whether the session may be compacted at all, ignoring timing.
func Eligible(cfg config.Config, override model.Setting, f transcript.Facts) (bool, string) {
	eff := Effective(cfg.Default, override)
	switch {
	case eff == model.Off:
		return false, "off"
	case f.Compacted:
		return false, "compacted"
	case f.TTL == 0:
		return false, "cache lifetime unknown"
	case f.TTL < time.Hour && !cfg.FiveMinuteTTL:
		return false, "5-minute cache"
	case eff == model.Auto && f.Tokens < cfg.MinTokens:
		return false, "below threshold"
	}
	return true, ""
}

type Decision struct {
	Armed     bool
	Reason    string
	Deadline  time.Time
	Effective model.Setting
}

func Decide(cfg config.Config, override model.Setting, f transcript.Facts, now time.Time) Decision {
	d := Decision{Effective: Effective(cfg.Default, override)}
	if ok, why := Eligible(cfg, override, f); !ok {
		d.Reason = why
		return d
	}
	expiry := f.At.Add(f.TTL)
	if !now.Before(expiry) {
		d.Reason = "cache expired"
		return d
	}
	if !now.Add(cfg.Warning).Before(expiry) {
		d.Reason = "too close to expiry"
		return d
	}
	deadline := expiry.Add(-cfg.Lead)
	if !deadline.After(f.At) {
		d.Reason = "lead longer than the cache"
		return d
	}
	if earliest := now.Add(cfg.Warning); deadline.Before(earliest) {
		deadline = earliest
	}
	d.Armed, d.Deadline = true, deadline
	return d
}
