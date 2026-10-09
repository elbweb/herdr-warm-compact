package display

import (
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
)

func s(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestTokens(t *testing.T) {
	cases := []struct {
		v    View
		want string
	}{
		{View{Phase: model.Armed, Remaining: 37*time.Minute + 10*time.Second, Show: "armed"}, "⏱ 38m"},
		{View{Phase: model.Armed, Remaining: 37 * time.Minute, Show: "warnings"}, "<nil>"},
		{View{Phase: model.Armed, Override: model.On, Remaining: 5 * time.Minute, Show: "warnings"}, "⏱ 5m on"},
		{View{Phase: model.Armed, Remaining: -2 * time.Minute, Show: "armed"}, "⏱ 0m"},
		{View{Phase: model.Warning, Remaining: 42 * time.Second, Flash: true}, "⚠ 0:42"},
		{View{Phase: model.Warning, Remaining: 42 * time.Second, Draft: true}, "· 0:42 DRAFT"},
		{View{Phase: model.Compacting}, "⏳ compacting"},
		{View{Phase: model.Restoring}, "⏳ compacting"},
		{View{Phase: model.Failed, Reason: "stash failed"}, "✗ stash failed"},
		{View{Phase: model.Quiet, Override: model.Off, Show: "warnings"}, "off"},
		{View{Phase: model.Quiet, Override: model.On, Show: "armed"}, "on"},
		{View{Phase: model.Quiet, Show: "armed"}, "<nil>"},
	}
	for _, c := range cases {
		if got := s(Token(c.v)); got != c.want {
			t.Errorf("%+v = %q, want %q", c.v, got, c.want)
		}
	}
}
