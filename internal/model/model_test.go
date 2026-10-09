package model

import "testing"

func TestParseSetting(t *testing.T) {
	cases := map[string]Setting{"default": Inherit, "": Inherit, "auto": Auto, "on": On, "off": Off}
	for in, want := range cases {
		got, err := ParseSetting(in)
		if err != nil || got != want {
			t.Errorf("ParseSetting(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseSetting("always"); err == nil {
		t.Error("ParseSetting(always) should fail")
	}
}

func TestNextCycles(t *testing.T) {
	order := []Setting{Inherit, Auto, On, Off, Inherit}
	for i := 0; i < 4; i++ {
		if got := order[i].Next(); got != order[i+1] {
			t.Errorf("%q.Next() = %q, want %q", order[i], got, order[i+1])
		}
	}
}

func TestLabel(t *testing.T) {
	if Inherit.Label() != "default" || Off.Label() != "off" {
		t.Error("labels")
	}
}