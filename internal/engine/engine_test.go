package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/store"
	"github.com/elbweb/herdr-warm-compact/internal/transcript"
)

var t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

type rig struct {
	e   *Engine
	clk *fakeClock
	h   *fakeHerdr
	tr  *fakeTranscripts
	ov  fakeOverrides
}

func newRig() *rig {
	r := &rig{clk: &fakeClock{now: t0}, h: newHerdr(), ov: fakeOverrides{},
		tr: &fakeTranscripts{facts: map[string]transcript.Facts{
			"s1": {At: t0, Tokens: 200000, TTL: time.Hour},
		}, errs: map[string]error{}}}
	r.e = New(r.h, r.tr, r.clk, r.ov, config.Defaults(), func(f func()) { f() }, func(string, ...any) {})
	r.h.screens["p1"] = []string{scr("❯ ")}
	return r
}

// typed is the screen after /compact was typed into an empty box, before Enter.
func typed() string { return scr("❯ /compact The owner stepped away and will resume later.") }

func pane(status string) Pane {
	return Pane{ID: "p1", Session: "s1", Workspace: "proj", Name: "topic", Status: status}
}

// compactDone simulates Claude running the compaction and writing its marker.
func (r *rig) compactDone(pane Pane) {
	r.e.Status(Pane{ID: pane.ID, Session: pane.Session, Status: "working"})
	f := r.tr.facts[pane.Session]
	f.Compacted, f.CompactedAt = true, r.clk.Now()
	r.tr.facts[pane.Session] = f
	r.e.Status(Pane{ID: pane.ID, Session: pane.Session, Status: "idle"})
	r.clk.Advance(time.Second)
}

func TestCompactsIdleBigSessionAtDeadline(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	if r.h.tokens["p1"] != "⏱ 55m" {
		t.Fatalf("token %q", r.h.tokens["p1"])
	}
	r.clk.Advance(54 * time.Minute)
	if len(r.h.sent("toast")) != 1 || !strings.HasPrefix(r.h.tokens["p1"], "· 1:00") {
		t.Fatalf("warning: %v %q", r.h.calls, r.h.tokens["p1"])
	}
	r.clk.Advance(time.Minute + time.Second) // deadline, then the settle read before Enter
	text := r.h.sent("text")
	if len(text) != 1 || !strings.HasPrefix(text[0], "text p1 /compact The owner stepped away") || len(r.h.sent("keys p1 enter")) != 1 {
		t.Fatalf("calls %v", r.h.calls)
	}
	if r.h.tokens["p1"] != "⏳ compacting" {
		t.Fatalf("token %q", r.h.tokens["p1"])
	}
	r.compactDone(pane("idle"))
	if _, ok := r.h.tokens["p1"]; ok {
		t.Fatalf("token should be cleared, got %q", r.h.tokens["p1"])
	}
	if row := r.e.Rows()[0]; row.Phase != model.Quiet || row.Reason != "compacted" {
		t.Fatalf("%+v", row)
	}
}

func TestWorkingDisarms(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.e.Status(pane("working"))
	r.clk.Advance(2 * time.Hour)
	if len(r.h.sent("text")) != 0 || len(r.h.sent("toast")) != 0 {
		t.Fatalf("%v", r.h.calls)
	}
}

func TestBlockedDisarms(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.e.Status(pane("blocked"))
	r.clk.Advance(2 * time.Hour)
	if len(r.h.sent("text")) != 0 {
		t.Fatalf("%v", r.h.calls)
	}
}

func TestBelowThresholdUnlessOn(t *testing.T) {
	r := newRig()
	f := r.tr.facts["s1"]
	f.Tokens = 1000
	r.tr.facts["s1"] = f
	r.e.Status(pane("idle"))
	if r.e.Rows()[0].Reason != "below threshold" {
		t.Fatalf("%+v", r.e.Rows()[0])
	}
	r.e.Request(store.Request{Kind: "set", Pane: "p1", Value: "on"})
	if r.e.Rows()[0].Phase != model.Armed || r.ov["s1"] != model.On {
		t.Fatalf("%+v", r.e.Rows()[0])
	}
}

func TestOffFromRequestDisarms(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.e.Request(store.Request{Kind: "set", Pane: "p1", Value: "off"})
	r.clk.Advance(2 * time.Hour)
	if len(r.h.sent("text")) != 0 || r.h.tokens["p1"] != "off" {
		t.Fatalf("%v %q", r.h.calls, r.h.tokens["p1"])
	}
}

func TestToggleCycles(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	for _, want := range []model.Setting{model.Auto, model.On, model.Off, model.Inherit} {
		r.e.Request(store.Request{Kind: "toggle", Pane: "p1"})
		if r.ov.Get("s1") != want {
			t.Fatalf("toggle -> %q, want %q", r.ov.Get("s1"), want)
		}
	}
}

func TestDraftIsStashedAndRestored(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{
		scr("❯ my draft"), // hold snapshot (deadline-60s)
		scr("❯ my draft"), // warning read
		scr("❯ my draft"), // deadline read
		scr("❯ "),         // after ctrl+s: stashed
		typed(),           // after /compact was typed
		scr("❯ "),         // after compaction: box still empty
		scr("❯ my draft"), // after restoring ctrl+s
	}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	if !strings.Contains(strings.Join(r.h.calls, "|"), "keys p1 ctrl+s|text p1 /compact") {
		t.Fatalf("%v", r.h.calls)
	}
	r.compactDone(pane("idle"))
	if n := len(r.h.sent("keys p1 ctrl+s")); n != 2 {
		t.Fatalf("ctrl+s sent %d times: %v", n, r.h.calls)
	}
	if row := r.e.Rows()[0]; row.Phase != model.Quiet || row.Reason != "compacted" {
		t.Fatalf("%+v", row)
	}
}

func TestStashFailedSendsNothing(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft")}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	if len(r.h.sent("text")) != 0 || r.e.Rows()[0].Phase != model.Failed || r.h.tokens["p1"] != "✗ "+stashFailed {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestTypedDuringCompactionIsNotOverwritten(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft"), scr("❯ "), typed(), scr("❯ new typing")}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	r.compactDone(pane("idle"))
	if n := len(r.h.sent("keys p1 ctrl+s")); n != 1 {
		t.Fatalf("restoring ctrl+s must not be sent over new text: %v", r.h.calls)
	}
	if row := r.e.Rows()[0]; row.Phase != model.Failed || !strings.HasPrefix(row.Reason, "draft not restored") {
		t.Fatalf("%+v", row)
	}
}

func TestActivityInPanePostpones(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ x")}
	r.e.Status(pane("idle"))
	r.clk.Advance(56 * time.Minute)
	if len(r.h.sent("text")) != 0 || r.e.Rows()[0].Reason != "you were active in the pane" {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestBackgroundWorkPostpones(t *testing.T) {
	r := newRig()
	r.tr.subs = true
	r.e.Status(pane("idle"))
	r.clk.Advance(56 * time.Minute)
	if len(r.h.sent("text")) != 0 || r.e.Rows()[0].Reason != "background work running" {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestLateTimerAfterSleepDoesNothing(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.clk.Sleep(3 * time.Hour)
	r.clk.Advance(0)
	if len(r.h.sent("text")) != 0 || len(r.h.sent("keys")) != 0 {
		t.Fatalf("typed into a cold session: %v", r.h.calls)
	}
}

func TestNewSessionInSamePaneResets(t *testing.T) {
	r := newRig()
	r.tr.facts["s2"] = transcript.Facts{At: t0, Tokens: 10, TTL: time.Hour}
	r.ov["s1"] = model.On
	r.e.Status(pane("idle"))
	r.e.Status(Pane{ID: "p1", Session: "s2", Status: "idle"})
	r.clk.Advance(2 * time.Hour)
	if len(r.h.sent("text")) != 0 || r.e.Rows()[0].Session != "s2" || r.e.Rows()[0].Override != model.Inherit {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestSameSessionInTwoPanesArmsOnce(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.h.screens["p2"] = []string{scr("❯ ")}
	r.e.Status(pane("idle"))
	r.e.Status(Pane{ID: "p2", Session: "s1", Status: "idle"})
	r.clk.Advance(56 * time.Minute)
	if n := len(r.h.sent("text")); n != 1 {
		t.Fatalf("compacted %d times: %v", n, r.h.calls)
	}
}

func TestPaneClosedMidCompactionStopsEverything(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	r.clk.Advance(56 * time.Minute)
	n := len(r.h.calls)
	r.e.Closed("p1")
	r.clk.Advance(time.Hour)
	if len(r.h.calls) != n || len(r.e.Rows()) != 0 {
		t.Fatalf("calls after close: %v", r.h.calls[n:])
	}
}

func TestSkipThisTimeHoldsUntilNextRequest(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.e.Request(store.Request{Kind: "skip", Pane: "p1"})
	r.e.Status(pane("done")) // idle -> done when viewed: same transcript, must stay skipped
	r.clk.Advance(2 * time.Hour)
	if len(r.h.sent("text")) != 0 || r.e.Rows()[0].Reason != "skipped" {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestCompactTimeoutFails(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	r.clk.Advance(56 * time.Minute)
	r.clk.Advance(11 * time.Minute)
	if row := r.e.Rows()[0]; row.Phase != model.Failed || row.Reason != "compact timed out" {
		t.Fatalf("%+v", row)
	}
}

func TestCompactNowIgnoresThreshold(t *testing.T) {
	r := newRig()
	f := r.tr.facts["s1"]
	f.Tokens = 10
	r.tr.facts["s1"] = f
	r.e.Status(pane("idle"))
	r.e.Request(store.Request{Kind: "compact_now", Pane: "p1"})
	if len(r.h.sent("text")) != 1 {
		t.Fatalf("%v", r.h.calls)
	}
}

func TestNoTranscriptYetIsQuiet(t *testing.T) {
	r := newRig()
	r.tr.errs["s1"] = transcript.ErrNotFound
	r.e.Status(pane("idle"))
	if row := r.e.Rows()[0]; row.Phase != model.Quiet || row.Reason != "no transcript yet" {
		t.Fatalf("%+v", row)
	}
}

func TestFailureStaysUntilOwnerActs(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ d"), scr("❯ d"), scr("❯ d"), scr("❯ d")}
	r.e.Status(pane("idle"))
	r.clk.Advance(56 * time.Minute)
	r.e.Status(pane("done"))
	if r.e.Rows()[0].Phase != model.Failed {
		t.Fatal("failure cleared by a status event")
	}
	r.e.Status(pane("working")) // the owner sent something
	if r.e.Rows()[0].Phase == model.Failed {
		t.Fatal("failure not cleared when the owner acted")
	}
}

func TestReEvaluationInWarningWindowKeepsDeadline(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	r.clk.Advance(54*time.Minute + 30*time.Second) // inside the warning window
	r.e.Status(pane("done"))                       // owner glances at the pane: idle -> done
	r.e.Status(pane("idle"))
	r.clk.Advance(31 * time.Second) // past the original 55m deadline
	if n := len(r.h.sent("toast")); n != 1 {
		t.Fatalf("toasted %d times: %v", n, r.h.calls)
	}
	if n := len(r.h.sent("text")); n != 1 {
		t.Fatalf("compaction typed %d times by 55m: %v", n, r.h.calls)
	}
}

func TestUnchangedStatusDoesNotReReadTranscript(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.tr.facts["s1"] = transcript.Facts{At: t0, Tokens: 10, TTL: time.Hour} // would disarm if re-read
	r.e.Status(pane("idle"))                                                // same session, same status
	if r.e.Rows()[0].Phase != model.Armed {
		t.Fatalf("re-evaluated on an unchanged status: %+v", r.e.Rows()[0])
	}
}

func TestLateWakeInsideWarningWindowStillWarnsFirst(t *testing.T) {
	r := newRig()
	// late snapshot and warning, the re-armed snapshot and warning, the deadline read, then /compact typed
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	r.clk.Sleep(57 * time.Minute)
	r.clk.Advance(0)
	if n := len(r.h.sent("text")); n != 0 {
		t.Fatalf("compacted at the instant of waking: %v", r.h.calls)
	}
	if n := len(r.h.sent("toast")); n != 1 {
		t.Fatalf("toasted %d times: %v", n, r.h.calls)
	}
	r.clk.Advance(61 * time.Second)
	if n := len(r.h.sent("text")); n != 1 {
		t.Fatalf("compaction typed %d times: %v", n, r.h.calls)
	}
	if n := len(r.h.sent("toast")); n != 1 {
		t.Fatalf("toasted %d times after the re-arm: %v", n, r.h.calls)
	}
}

// draftScreens: snapshot, warning and deadline reads show a draft; Ctrl+S empties the box; /compact is typed.
func draftScreens() []string {
	return []string{scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft"), scr("❯ "), typed()}
}

func TestSkipDuringStashWindowDoesNotStrandDraft(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = draftScreens()
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute - 100*time.Millisecond)
	r.clk.Advance(100 * time.Millisecond) // deadline: Ctrl+S sent, settle pending
	r.e.Request(store.Request{Kind: "skip", Pane: "p1"})
	r.clk.Advance(time.Second)
	row := r.e.Rows()[0]
	if row.Reason == "skipped" || row.Phase == model.Quiet {
		t.Fatalf("skip dropped the stash window: %+v %v", row, r.h.calls)
	}
	if len(r.h.sent("text")) != 1 {
		t.Fatalf("expected the compaction to go on: %v", r.h.calls)
	}
}

func TestBusyDuringStashWindowFailsWithStashHint(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = draftScreens()
	r.e.Status(pane("idle"))
	r.clk.Advance(55 * time.Minute)
	r.e.Status(pane("working"))
	r.clk.Advance(time.Second)
	row := r.e.Rows()[0]
	if len(r.h.sent("text")) != 0 || row.Phase != model.Failed || !strings.Contains(row.Reason, "Claude's stash") {
		t.Fatalf("%v %+v", r.h.calls, row)
	}
}

func TestStashedFlagDoesNotLeakIntoNextCycle(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = append(draftScreens(), scr("❯ "), scr("❯ "), scr("❯ "), typed())
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	r.clk.Advance(11 * time.Minute) // compaction times out with the draft stashed
	if r.e.Rows()[0].Phase != model.Failed {
		t.Fatalf("%+v", r.e.Rows()[0])
	}
	f := r.tr.facts["s1"]
	f.At = r.clk.Now()
	r.tr.facts["s1"] = f
	r.e.Status(pane("working"))
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	r.compactDone(pane("idle"))
	if n := len(r.h.sent("keys p1 ctrl+s")); n != 1 {
		t.Fatalf("spurious restoring ctrl+s: %v", r.h.calls)
	}
	for _, c := range r.h.sent("toast") {
		if strings.Contains(c, "draft not restored") {
			t.Fatalf("%v", r.h.calls)
		}
	}
	if len(r.h.sent("text")) != 2 || r.e.Rows()[0].Reason != "compacted" {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestWorkResumingDuringFinishSettleCancelsRestore(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = append(draftScreens(), scr("❯ "), scr("❯ my draft"))
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	f := r.tr.facts["s1"]
	f.Compacted, f.CompactedAt = true, r.clk.Now()
	r.tr.facts["s1"] = f
	idleP := Pane{ID: "p1", Session: "s1", Status: "idle"}
	r.e.Status(Pane{ID: "p1", Session: "s1", Status: "working"})
	r.e.Status(idleP)
	r.clk.Advance(100 * time.Millisecond)
	r.e.Status(Pane{ID: "p1", Session: "s1", Status: "working"})
	r.clk.Advance(time.Second)
	if n := len(r.h.sent("keys p1 ctrl+s")); n != 1 || len(r.h.sent("toast Warm Compact failed")) != 0 {
		t.Fatalf("restore ran into a working pane: %v", r.h.calls)
	}
	if r.e.Rows()[0].Phase != model.Compacting {
		t.Fatalf("%+v", r.e.Rows()[0])
	}
	r.e.Status(idleP)
	r.clk.Advance(time.Second)
	if n := len(r.h.sent("keys p1 ctrl+s")); n != 2 || r.e.Rows()[0].Reason != "compacted" {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestNewSessionBelowThresholdClearsOldToken(t *testing.T) {
	r := newRig()
	r.tr.facts["s2"] = transcript.Facts{At: t0, Tokens: 10, TTL: time.Hour}
	r.e.Status(pane("idle"))
	if r.h.tokens["p1"] != "⏱ 55m" {
		t.Fatalf("token %q", r.h.tokens["p1"])
	}
	r.e.Status(Pane{ID: "p1", Session: "s2", Status: "idle"})
	if v, ok := r.h.tokens["p1"]; ok {
		t.Fatalf("old token left up: %q", v)
	}
}

func TestDraftRestoredByClaudeItselfCountsAsRestored(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{
		scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft"), // snapshot, warning, deadline
		scr("❯ "),         // after ctrl+s: stashed
		typed(),           // after /compact was typed
		scr("❯ my draft"), // after compaction: Claude put it back itself
	}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	r.compactDone(pane("idle"))
	if n := len(r.h.sent("keys p1 ctrl+s")); n != 1 {
		t.Fatalf("ctrl+s sent %d times: %v", n, r.h.calls)
	}
	if len(r.h.sent("toast Warm Compact failed")) != 0 {
		t.Fatalf("%v", r.h.calls)
	}
	if row := r.e.Rows()[0]; row.Phase != model.Quiet || row.Reason != "compacted" {
		t.Fatalf("%+v", row)
	}
}

func TestRestoredDifferentTextFails(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{
		scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft"),
		scr("❯ "), typed(), scr("❯ "), scr("❯ other text"), // after the restoring ctrl+s: not the draft
	}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	r.compactDone(pane("idle"))
	if row := r.e.Rows()[0]; row.Phase != model.Failed || !strings.HasPrefix(row.Reason, "draft not restored") {
		t.Fatalf("%+v %v", row, r.h.calls)
	}
}

const stashFailed = "stash failed; your draft may be in Claude's stash (Ctrl+S on an empty box)"

func TestEnterOnlyWhenPromptShowsCompact(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), scr("❯ something else")}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	if len(r.h.sent("text")) != 1 || len(r.h.sent("keys p1 enter")) != 0 {
		t.Fatalf("enter pressed over unexpected text: %v", r.h.calls)
	}
	if row := r.e.Rows()[0]; row.Phase != model.Failed || row.Reason != "prompt box not as typed; nothing was submitted" {
		t.Fatalf("%+v", row)
	}
}

func TestPromptNotAsTypedAfterStashAddsStashHint(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ my draft"), scr("❯ my draft"), scr("❯ my draft"), scr("❯ "), scr("❯ x")}
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	row := r.e.Rows()[0]
	if len(r.h.sent("keys p1 enter")) != 0 || row.Phase != model.Failed ||
		row.Reason != "prompt box not as typed; nothing was submitted; draft is in Claude's stash (Ctrl+S)" {
		t.Fatalf("%v %+v", r.h.calls, row)
	}
}

func TestBusyDuringTypeSettleDoesNotFinish(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	r.clk.Advance(55 * time.Minute) // /compact typed, settle pending
	r.e.Status(pane("working"))
	r.e.Status(pane("idle"))
	r.clk.Advance(time.Second)
	if len(r.h.sent("keys p1 enter")) != 1 || r.e.Rows()[0].Phase != model.Compacting {
		t.Fatalf("%v %+v", r.h.calls, r.e.Rows()[0])
	}
}

func TestFinishWithOtherTextInBoxSaysWhereTheDraftIs(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = append(draftScreens(), scr("❯ new typing"))
	r.e.Status(pane("idle"))
	r.clk.Advance(55*time.Minute + time.Second)
	r.compactDone(pane("idle"))
	want := "draft not restored: the box holds other text; your draft is in it or in Claude's stash (Ctrl+S on an empty box)"
	if row := r.e.Rows()[0]; row.Phase != model.Failed || row.Reason != want {
		t.Fatalf("%+v", row)
	}
}

func TestTickAfterSuspendReArmsHonestly(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.clk.Sleep(57 * time.Minute) // past the 55m deadline, 3m before expiry; timers have not fired
	r.e.Tick()
	row := r.e.Rows()[0]
	if row.Phase != model.Armed || row.Deadline.Before(r.clk.Now().Add(config.Defaults().Warning)) {
		t.Fatalf("not re-armed: %+v (now %v)", row, r.clk.Now())
	}
	if len(r.h.sent("text")) != 0 || len(r.h.sent("keys")) != 0 {
		t.Fatalf("typed on wake: %v", r.h.calls)
	}
}

func TestTickAfterSuspendTooCloseToExpiryGoesQuiet(t *testing.T) {
	r := newRig()
	r.e.Status(pane("idle"))
	r.clk.Sleep(59*time.Minute + 30*time.Second)
	r.e.Tick()
	if row := r.e.Rows()[0]; row.Phase != model.Quiet || row.Reason != "too close to expiry" {
		t.Fatalf("%+v", row)
	}
	r.clk.Advance(time.Hour)
	if len(r.h.sent("text")) != 0 {
		t.Fatalf("%v", r.h.calls)
	}
}

func TestWorkingAtSubmitPressesNoEnter(t *testing.T) {
	r := newRig()
	r.h.screens["p1"] = []string{scr("❯ "), scr("❯ "), scr("❯ "), typed()}
	r.e.Status(pane("idle"))
	r.clk.Advance(55 * time.Minute) // /compact typed, settle pending
	r.e.Status(pane("working"))
	r.clk.Advance(time.Second)
	if len(r.h.sent("keys p1 enter")) != 0 {
		t.Fatalf("enter pressed into a working pane: %v", r.h.calls)
	}
	if row := r.e.Rows()[0]; row.Phase != model.Failed || row.Reason != "busy before submit; nothing was submitted" {
		t.Fatalf("%+v", row)
	}
}
