// Package engine is the plugin's state machine: one record per Claude pane, armed on idle, warned, then
// compacted with the draft stashed and restored. Every method runs on one goroutine; timer callbacks
// re-enter through post.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/display"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/policy"
	"github.com/elbweb/herdr-warm-compact/internal/screen"
	"github.com/elbweb/herdr-warm-compact/internal/store"
	"github.com/elbweb/herdr-warm-compact/internal/transcript"
)

type Pane struct {
	ID, Session, Workspace, Folder, Name, Status string
}

type Herdr interface {
	Read(ctx context.Context, pane string) (string, error)
	SendKeys(ctx context.Context, pane string, keys ...string) error
	SendText(ctx context.Context, pane, text string) error
	Tokens(ctx context.Context, pane string, tokens map[string]*string, ttl time.Duration) error
	Notify(ctx context.Context, title, body string) error
}

type Transcripts interface {
	Facts(session string) (transcript.Facts, error)
	SubagentsSince(session string, t time.Time) bool
}

type Overrides interface {
	Get(session string) model.Setting
	Set(session string, s model.Setting) error
}

type Timer interface{ Stop() bool }

type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

const (
	settle    = 400 * time.Millisecond // screen redraw after a key (Task 0 Step 3)
	tokenTTL  = 3 * time.Minute
	tokenKey  = "compact"
	clockSkew = 5 * time.Second
	callLimit = 5 * time.Second
)

type tracked struct {
	pane     Pane
	facts    transcript.Facts
	phase    model.Phase
	reason   string
	deadline time.Time
	raw      time.Time // facts.At + TTL - Lead as armed; the deadline itself may be pushed out by policy
	draft    bool
	stashed  bool
	sawWork  bool
	fp       string
	started  time.Time
	skipAt   time.Time // the transcript time "skip this time" applied to
	gen      int
	timers   []Timer
	shown    *string
	sent     bool
}

type Engine struct {
	h     Herdr
	tr    Transcripts
	clk   Clock
	ov    Overrides
	cfg   config.Config
	post  func(func())
	logf  func(string, ...any)
	panes map[string]*tracked
	flash bool
}

func New(h Herdr, tr Transcripts, clk Clock, ov Overrides, cfg config.Config, post func(func()), logf func(string, ...any)) *Engine {
	return &Engine{h: h, tr: tr, clk: clk, ov: ov, cfg: cfg, post: post, logf: logf, panes: map[string]*tracked{}}
}

func idle(s string) bool { return s == "idle" || s == "done" }
func busy(s string) bool { return s == "working" || s == "blocked" }

func (e *Engine) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), callLimit)
}

func (t *tracked) stop() {
	for _, tm := range t.timers {
		tm.Stop()
	}
	t.timers = nil
	t.gen++
}

// after runs f on the loop after d, unless the record was stopped, replaced or closed meanwhile.
func (e *Engine) after(t *tracked, d time.Duration, f func()) {
	if d < 0 {
		d = 0
	}
	gen := t.gen
	t.timers = append(t.timers, e.clk.AfterFunc(d, func() {
		e.post(func() {
			if cur, ok := e.panes[t.pane.ID]; !ok || cur != t || t.gen != gen {
				return
			}
			f()
		})
	}))
}

func (e *Engine) Known(id string) (Pane, bool) {
	t, ok := e.panes[id]
	if !ok {
		return Pane{}, false
	}
	return t.pane, true
}

func (e *Engine) Status(p Pane) {
	t, ok := e.panes[p.ID]
	fresh := !ok || t.pane.Session != p.Session
	if fresh {
		if ok {
			t.stop()
		}
		t = &tracked{pane: p, phase: model.Quiet}
		e.panes[p.ID] = t
	}
	prev := t.pane.Status
	if p.Name == "" {
		p.Name, p.Workspace, p.Folder = t.pane.Name, t.pane.Workspace, t.pane.Folder
	}
	t.pane = p
	switch t.phase {
	case model.Compacting:
		if busy(p.Status) {
			t.sawWork = true
		} else if idle(p.Status) && t.sawWork {
			e.after(t, settle, func() { e.finish(t) })
		}
		return
	case model.Restoring:
		return
	case model.Failed:
		if busy(p.Status) {
			e.set(t, model.Quiet, "")
		}
		return
	}
	if !fresh && prev == p.Status {
		return // the resident repeats Status on every herdr event; only a change is evaluated
	}
	if !idle(p.Status) {
		t.stop()
		e.set(t, model.Quiet, "busy")
		return
	}
	e.evaluate(t)
}

func (e *Engine) Closed(id string) {
	if t, ok := e.panes[id]; ok {
		t.stop()
		delete(e.panes, id)
	}
}

func (e *Engine) otherHolder(t *tracked) string {
	for id, o := range e.panes {
		if id != t.pane.ID && o.pane.Session == t.pane.Session {
			switch o.phase {
			case model.Armed, model.Warning, model.Compacting, model.Restoring:
				return id
			}
		}
	}
	return ""
}

func (e *Engine) evaluate(t *tracked) {
	now := e.clk.Now()
	f, err := e.tr.Facts(t.pane.Session)
	switch {
	case errors.Is(err, transcript.ErrNotFound), errors.Is(err, transcript.ErrNoEntry):
		t.stop()
		e.set(t, model.Quiet, "no transcript yet")
		return
	case err != nil:
		t.stop()
		e.logf("pane %s: transcript: %v", t.pane.ID, err)
		e.set(t, model.Failed, "transcript unreadable")
		return
	}
	t.facts = f
	if !t.skipAt.IsZero() && t.skipAt.Equal(f.At) {
		t.stop()
		e.set(t, model.Quiet, "skipped")
		return
	}
	if other := e.otherHolder(t); other != "" {
		t.stop()
		e.set(t, model.Quiet, "session open in pane "+other)
		return
	}
	d := policy.Decide(e.cfg, e.ov.Get(t.pane.Session), f, now)
	if !d.Armed {
		t.stop()
		e.set(t, model.Quiet, d.Reason)
		return
	}
	// Compare the raw deadline: policy pushes a passed one out to now+warning, which would re-arm and
	// re-warn on every re-evaluation inside the warning window.
	raw := f.At.Add(f.TTL).Add(-e.cfg.Lead)
	if (t.phase == model.Armed || t.phase == model.Warning) && raw.Equal(t.raw) {
		return
	}
	t.stop()
	t.raw = raw
	t.deadline, t.fp, t.draft = d.Deadline, "", false
	e.set(t, model.Armed, "")
	until := d.Deadline.Sub(now)
	e.after(t, until-e.cfg.HoldIfActive, func() { e.snapshot(t) })
	e.after(t, until-e.cfg.Warning, func() { e.warn(t) })
	e.after(t, until, func() { e.fire(t, false) })
}

func (e *Engine) snapshot(t *tracked) {
	ctx, cancel := e.ctx()
	defer cancel()
	if text, err := e.h.Read(ctx, t.pane.ID); err == nil {
		t.fp = screen.Fingerprint(text)
	}
}

func (e *Engine) name(t *tracked) string {
	if t.pane.Name != "" {
		return t.pane.Name
	}
	return t.pane.ID
}

func (e *Engine) warn(t *tracked) {
	if !e.clk.Now().Before(t.facts.At.Add(t.facts.TTL)) { // woke from sleep past expiry
		e.quiet(t, "cache expired before compacting")
		return
	}
	ctx, cancel := e.ctx()
	defer cancel()
	if text, err := e.h.Read(ctx, t.pane.ID); err == nil {
		d, _ := screen.Draft(text)
		t.draft = d != ""
	}
	e.set(t, model.Warning, "")
	body := fmt.Sprintf("%s compacts in %s", e.name(t), e.cfg.Warning)
	if t.draft {
		body += "; its draft will be stashed and put back"
	}
	e.h.Notify(ctx, "Warm Compact", body)
}

func (e *Engine) quiet(t *tracked, reason string) {
	t.stop()
	e.set(t, model.Quiet, reason)
}

// fire runs the checks and starts the sequence. force (compact now) skips eligibility, expiry and the
// activity check, never the idle and background checks.
func (e *Engine) fire(t *tracked, force bool) {
	now := e.clk.Now()
	if !idle(t.pane.Status) {
		e.quiet(t, "busy")
		return
	}
	f, err := e.tr.Facts(t.pane.Session)
	if err != nil {
		t.stop()
		e.set(t, model.Failed, "transcript unreadable")
		return
	}
	if !force {
		if !f.At.Equal(t.facts.At) {
			e.evaluate(t)
			return
		}
		if !now.Before(f.At.Add(f.TTL)) {
			e.quiet(t, "cache expired before compacting")
			return
		}
		if ok, why := policy.Eligible(e.cfg, e.ov.Get(t.pane.Session), f); !ok {
			e.quiet(t, why)
			return
		}
	}
	ctx, cancel := e.ctx()
	defer cancel()
	text, err := e.h.Read(ctx, t.pane.ID)
	if err != nil {
		e.quiet(t, "screen unreadable")
		return
	}
	if !force && (t.fp == "" || screen.Fingerprint(text) != t.fp) {
		e.quiet(t, "you were active in the pane")
		return
	}
	if screen.Busy(text) || e.tr.SubagentsSince(t.pane.Session, now.Add(-e.cfg.HoldIfActive)) {
		e.quiet(t, "background work running")
		return
	}
	draft, ok := screen.Draft(text)
	if !ok {
		e.quiet(t, "prompt box not found")
		return
	}
	t.stop()
	t.facts = f
	if draft == "" {
		e.compact(t)
		return
	}
	e.h.SendKeys(ctx, t.pane.ID, "ctrl+s")
	e.after(t, settle, func() { e.afterStash(t) })
}

func (e *Engine) afterStash(t *tracked) {
	ctx, cancel := e.ctx()
	defer cancel()
	text, err := e.h.Read(ctx, t.pane.ID)
	d, ok := screen.Draft(text)
	if err != nil || !ok || d != "" {
		e.fail(t, "stash failed")
		return
	}
	t.stashed = true
	e.compact(t)
}

func (e *Engine) compact(t *tracked) {
	ctx, cancel := e.ctx()
	defer cancel()
	instr := strings.Join(strings.Fields(e.cfg.Instructions), " ")
	if err := e.h.SendText(ctx, t.pane.ID, "/compact "+instr); err != nil {
		e.fail(t, "could not type /compact")
		return
	}
	e.h.SendKeys(ctx, t.pane.ID, "enter")
	t.started, t.sawWork = e.clk.Now(), false
	e.set(t, model.Compacting, "")
	e.after(t, e.cfg.CompactTimeout, func() { e.fail(t, "compact timed out") })
}

func (e *Engine) finish(t *tracked) {
	t.stop()
	f, err := e.tr.Facts(t.pane.Session)
	compacted := err == nil && f.Compacted && !f.CompactedAt.Before(t.started.Add(-clockSkew))
	if err == nil {
		t.facts = f
	}
	if !t.stashed {
		e.done(t, compacted)
		return
	}
	e.set(t, model.Restoring, "")
	ctx, cancel := e.ctx()
	defer cancel()
	text, rerr := e.h.Read(ctx, t.pane.ID)
	if d, ok := screen.Draft(text); rerr != nil || !ok || d != "" {
		e.fail(t, "draft not restored: it is in Claude's stash (Ctrl+S)")
		return
	}
	e.h.SendKeys(ctx, t.pane.ID, "ctrl+s")
	e.after(t, settle, func() {
		ctx, cancel := e.ctx()
		defer cancel()
		text, err := e.h.Read(ctx, t.pane.ID)
		if d, _ := screen.Draft(text); err != nil || d == "" {
			e.fail(t, "draft not restored: it is in Claude's stash (Ctrl+S)")
			return
		}
		t.stashed = false
		e.done(t, compacted)
	})
}

func (e *Engine) done(t *tracked, compacted bool) {
	t.stop()
	if !compacted {
		e.fail(t, "compact failed")
		return
	}
	e.logf("pane %s: compacted %d tokens", t.pane.ID, t.facts.Tokens)
	e.set(t, model.Quiet, "compacted")
}

func (e *Engine) fail(t *tracked, reason string) {
	t.stop()
	if t.stashed && !strings.HasPrefix(reason, "draft not restored") {
		reason += "; draft is in Claude's stash (Ctrl+S)"
	}
	e.logf("pane %s: %s", t.pane.ID, reason)
	e.set(t, model.Failed, reason)
	ctx, cancel := e.ctx()
	defer cancel()
	e.h.Notify(ctx, "Warm Compact failed", e.name(t)+": "+reason)
}

func (e *Engine) Request(r store.Request) {
	t, ok := e.panes[r.Pane]
	if !ok || t.pane.Session == "" {
		return
	}
	switch r.Kind {
	case "set", "toggle":
		v := e.ov.Get(t.pane.Session).Next()
		if r.Kind == "set" {
			var err error
			if v, err = model.ParseSetting(r.Value); err != nil {
				return
			}
		}
		if err := e.ov.Set(t.pane.Session, v); err != nil {
			e.logf("overrides: %v", err)
		}
		e.reconsider(t)
	case "skip":
		if t.phase == model.Armed || t.phase == model.Warning {
			t.skipAt = t.facts.At
			e.quiet(t, "skipped")
		}
	case "compact_now":
		if t.phase == model.Compacting || t.phase == model.Restoring {
			return
		}
		t.stop()
		e.fire(t, true)
	}
}

// reconsider re-evaluates a pane after the owner changed something, clearing a failure.
func (e *Engine) reconsider(t *tracked) {
	switch t.phase {
	case model.Compacting, model.Restoring:
		e.render(t, false)
		return
	}
	t.skipAt = time.Time{}
	if idle(t.pane.Status) {
		if t.phase == model.Failed {
			t.phase = model.Quiet
		}
		e.evaluate(t)
		return
	}
	e.set(t, model.Quiet, "busy")
}

func (e *Engine) SetConfig(c config.Config) {
	e.cfg = c
	for _, t := range e.panes {
		switch t.phase {
		case model.Compacting, model.Restoring, model.Failed:
			continue
		}
		if idle(t.pane.Status) {
			e.evaluate(t)
		}
	}
}

func (e *Engine) set(t *tracked, p model.Phase, reason string) {
	t.phase, t.reason = p, reason
	e.render(t, false)
}

func (e *Engine) render(t *tracked, force bool) {
	v := display.Token(display.View{
		Phase: t.phase, Override: e.ov.Get(t.pane.Session), Remaining: t.deadline.Sub(e.clk.Now()),
		Draft: t.draft, Flash: e.flash, Reason: t.reason, Show: e.cfg.Show,
	})
	same := (v == nil && t.shown == nil) || (v != nil && t.shown != nil && *v == *t.shown)
	if same && !force {
		return
	}
	if v == nil && !t.sent {
		return
	}
	ctx, cancel := e.ctx()
	defer cancel()
	if err := e.h.Tokens(ctx, t.pane.ID, map[string]*string{tokenKey: v}, tokenTTL); err == nil {
		t.shown, t.sent = v, v != nil
	}
}

// Tick re-sends every shown token once a minute, refreshing countdowns and token TTLs.
func (e *Engine) Tick() {
	for _, t := range e.panes {
		if t.shown != nil || t.phase == model.Armed {
			e.render(t, true)
		}
	}
}

// Flash flips the warning prefix; the resident calls it every second only while Warnings() is true.
func (e *Engine) Flash() {
	e.flash = !e.flash
	for _, t := range e.panes {
		if t.phase == model.Warning {
			e.render(t, false)
		}
	}
}

func (e *Engine) Warnings() bool {
	for _, t := range e.panes {
		if t.phase == model.Warning {
			return true
		}
	}
	return false
}

func (e *Engine) Rows() []store.Row {
	rows := make([]store.Row, 0, len(e.panes))
	for _, t := range e.panes {
		ov := e.ov.Get(t.pane.Session)
		rows = append(rows, store.Row{
			Pane: t.pane.ID, Session: t.pane.Session, Name: t.pane.Name, Workspace: t.pane.Workspace, Folder: t.pane.Folder,
			Tokens: t.facts.Tokens, TTL: t.facts.TTL, Override: ov, Effective: policy.Effective(e.cfg.Default, ov),
			Phase: t.phase, Reason: t.reason, Deadline: t.deadline,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Pane < rows[j].Pane })
	return rows
}
