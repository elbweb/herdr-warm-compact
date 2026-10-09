// Package model holds the small types every other package shares.
package model

import "fmt"

// Setting is a session's compaction override. Inherit means the session follows the global default.
type Setting string

const (
	Inherit Setting = ""
	Auto    Setting = "auto"
	On      Setting = "on"
	Off     Setting = "off"
)

// ParseSetting reads a setting as the owner types it; "default" and "" both mean Inherit.
func ParseSetting(s string) (Setting, error) {
	switch s {
	case "", "default":
		return Inherit, nil
	case "auto":
		return Auto, nil
	case "on":
		return On, nil
	case "off":
		return Off, nil
	}
	return Inherit, fmt.Errorf("unknown setting %q (want default, auto, on or off)", s)
}

// Next is the panel's and the key's cycle: default, auto, on, off, default.
func (s Setting) Next() Setting {
	switch s {
	case Inherit:
		return Auto
	case Auto:
		return On
	case On:
		return Off
	}
	return Inherit
}

// Label is the setting as the panel shows it.
func (s Setting) Label() string {
	if s == Inherit {
		return "default"
	}
	return string(s)
}

// Phase is where one session stands.
type Phase string

const (
	Quiet      Phase = "quiet"
	Armed      Phase = "armed"
	Warning    Phase = "warning"
	Compacting Phase = "compacting"
	Restoring  Phase = "restoring"
	Failed     Phase = "failed"
)
