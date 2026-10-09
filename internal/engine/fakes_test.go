package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/transcript"
)

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
	seq     int
}

func (t *fakeTimer) Stop() bool { was := !t.stopped; t.stopped = true; return was }

type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
	seq    int
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.seq++
	t := &fakeTimer{at: c.now.Add(d), f: f, seq: c.seq}
	c.timers = append(c.timers, t)
	return t
}

// Advance fires due timers in time order (ties in creation order), moving now to each.
func (c *fakeClock) Advance(d time.Duration) {
	target := c.now.Add(d)
	for {
		sort.SliceStable(c.timers, func(i, j int) bool {
			if !c.timers[i].at.Equal(c.timers[j].at) {
				return c.timers[i].at.Before(c.timers[j].at)
			}
			return c.timers[i].seq < c.timers[j].seq
		})
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.stopped && !t.at.After(target) {
				next = t
				break
			}
		}
		if next == nil {
			break
		}
		next.stopped = true
		if next.at.After(c.now) {
			c.now = next.at
		}
		next.f()
	}
	c.now = target
}

// Sleep moves the clock without firing anything, like a laptop lid closing.
func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

type fakeHerdr struct {
	screens map[string][]string // per pane: successive reads; the last one repeats
	calls   []string
	tokens  map[string]string
}

func newHerdr() *fakeHerdr {
	return &fakeHerdr{screens: map[string][]string{}, tokens: map[string]string{}}
}

func (h *fakeHerdr) Read(_ context.Context, pane string) (string, error) {
	q := h.screens[pane]
	if len(q) == 0 {
		return "", fmt.Errorf("no screen")
	}
	s := q[0]
	if len(q) > 1 {
		h.screens[pane] = q[1:]
	}
	return s, nil
}
func (h *fakeHerdr) SendKeys(_ context.Context, pane string, keys ...string) error {
	h.calls = append(h.calls, "keys "+pane+" "+strings.Join(keys, ","))
	return nil
}
func (h *fakeHerdr) SendText(_ context.Context, pane, text string) error {
	h.calls = append(h.calls, "text "+pane+" "+text)
	return nil
}
func (h *fakeHerdr) Tokens(_ context.Context, pane string, t map[string]*string, _ time.Duration) error {
	if v := t["compact"]; v != nil {
		h.tokens[pane] = *v
	} else {
		delete(h.tokens, pane)
	}
	return nil
}
func (h *fakeHerdr) Notify(_ context.Context, title, body string) error {
	h.calls = append(h.calls, "toast "+title+": "+body)
	return nil
}
func (h *fakeHerdr) sent(prefix string) []string {
	var out []string
	for _, c := range h.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

type fakeTranscripts struct {
	facts map[string]transcript.Facts
	errs  map[string]error
	subs  bool
}

func (f *fakeTranscripts) Facts(s string) (transcript.Facts, error) {
	if e := f.errs[s]; e != nil {
		return transcript.Facts{}, e
	}
	return f.facts[s], nil
}
func (f *fakeTranscripts) SubagentsSince(string, time.Time) bool { return f.subs }

type fakeOverrides map[string]model.Setting

func (o fakeOverrides) Get(s string) model.Setting { return o[s] }
func (o fakeOverrides) Set(s string, v model.Setting) error {
	if v == model.Inherit {
		delete(o, s)
	} else {
		o[s] = v
	}
	return nil
}

const rule = "──────────────────────────────"

func scr(prompt string) string {
	return "● answer\n" + rule + "\n" + prompt + "\n" + rule + "\n  footer 22%\n"
}
