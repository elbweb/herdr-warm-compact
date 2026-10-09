package screen

import "testing"

const rule = "──────────────────────────────"

func box(prompt, footer string) string {
	return "● Earlier answer text\n\n✻ Worked for 4s\n\n" + rule + " topic ─\n" + prompt + "\n" + rule + "\n" + footer + "\n"
}

const footer = "  ~/proj (main) Model · 22% │ 4% (4h03m)\n  ⏵⏵ auto mode on (shift+tab to cycle)"

func TestEmptyBox(t *testing.T) {
	d, ok := Draft(box("❯ ", footer))
	if !ok || d != "" {
		t.Fatalf("%q %v", d, ok)
	}
}

func TestDraftText(t *testing.T) {
	d, ok := Draft(box("❯ fix the parser", footer))
	if !ok || d != "fix the parser" {
		t.Fatalf("%q %v", d, ok)
	}
}

func TestMultiLineDraft(t *testing.T) {
	d, _ := Draft(box("❯ first line\n  second line", footer))
	if d != "first line\nsecond line" {
		t.Fatalf("%q", d)
	}
}

func TestPlaceholderIsNotADraft(t *testing.T) {
	d, ok := Draft(box("❯ \x1b[2mTry \"explain this repo\"\x1b[22m", footer))
	if !ok || d != "" {
		t.Fatalf("%q %v", d, ok)
	}
}

func TestObservedPlaceholderIsNotADraft(t *testing.T) {
	d, ok := Draft(box("❯ \x1b[0m\x1b[2mTry \"edit <filepath> to...\"\x1b[0m", footer))
	if !ok || d != "" {
		t.Fatalf("%q %v", d, ok)
	}
}

func TestColouredDraftIsADraft(t *testing.T) {
	d, _ := Draft(box("❯ \x1b[1mbold words\x1b[0m", footer))
	if d != "bold words" {
		t.Fatalf("%q", d)
	}
}

func TestNoBox(t *testing.T) {
	if _, ok := Draft("just a shell prompt $ "); ok {
		t.Fatal("found a box in a shell")
	}
}

func TestFingerprintIgnoresFooter(t *testing.T) {
	a := Fingerprint(box("❯ ", footer))
	b := Fingerprint(box("❯ ", "  ~/proj (main) Model · 23% │ 5% (4h02m)\n  ⏵⏵ auto mode on"))
	c := Fingerprint(box("❯ x", footer))
	if a != b || a == c {
		t.Fatal("fingerprint must ignore the footer and see the box")
	}
}

func TestBusy(t *testing.T) {
	if Busy(box("❯ ", footer)) {
		t.Fatal("idle footer counted as busy")
	}
	if !Busy(box("❯ ", footer+" · 1 background task")) {
		t.Fatal("background task not seen")
	}
}

func TestObservedFooters(t *testing.T) {
	busy := "  ⏵⏵ auto mode on · 1 shell · ← 1 agent"
	idle := "  ⏵⏵ auto mode on (shift+tab to cycle) · ← 1 agent"
	if !Busy(box("❯ ", busy)) {
		t.Fatal("observed busy footer (1 shell) not seen")
	}
	if Busy(box("❯ ", idle)) {
		t.Fatal("observed idle footer with ← 1 agent counted as busy")
	}
}
