// Package display turns a session's phase into the $compact sidebar token. herdr styles a token by its
// value (fg, bold, dim), so the warning "flash" alternates two prefixes the owner's sidebar rules colour
// differently: "⚠" bright, "·" dim.
package display

import (
	"fmt"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
)

type View struct {
	Phase     model.Phase
	Override  model.Setting
	Remaining time.Duration
	Draft     bool
	Flash     bool
	Reason    string
	Show      string
}

func str(s string) *string { return &s }

func Token(v View) *string {
	switch v.Phase {
	case model.Failed:
		return str("✗ " + v.Reason)
	case model.Compacting, model.Restoring:
		return str("⏳ compacting")
	case model.Warning:
		prefix := "·"
		if v.Flash {
			prefix = "⚠"
		}
		secs := int(v.Remaining.Round(time.Second) / time.Second)
		if secs < 0 {
			secs = 0
		}
		t := fmt.Sprintf("%s %d:%02d", prefix, secs/60, secs%60)
		if v.Draft {
			t += " DRAFT"
		}
		return str(t)
	case model.Armed:
		mins := int((v.Remaining + time.Minute - 1) / time.Minute)
		if mins < 0 {
			mins = 0
		}
		t := fmt.Sprintf("⏱ %dm", mins)
		if v.Override != model.Inherit {
			t += " " + string(v.Override)
		}
		if v.Show == "armed" || v.Override != model.Inherit {
			return str(t)
		}
		return nil
	}
	if v.Override != model.Inherit {
		return str(string(v.Override))
	}
	return nil
}
