package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func assistant(ts string, in, create, read, h1, m5 int, side bool) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"timestamp":%q,"message":{"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":9,"cache_creation":{"ephemeral_1h_input_tokens":%d,"ephemeral_5m_input_tokens":%d}}}}`,
		side, ts, in, create, read, h1, m5)
}

func write(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadLastTakesLastMainThreadAssistant(t *testing.T) {
	p := write(t,
		`{"type":"user","timestamp":"2026-01-01T10:00:00Z"}`,
		assistant("2026-01-01T10:00:05Z", 2, 1000, 100, 1000, 0, false),
		assistant("2026-01-01T10:00:09Z", 5, 50, 199000, 0, 0, true), // subagent: ignored
		`{"type":"user","timestamp":"2026-01-01T10:01:00Z"}`,
	)
	f, err := ReadLast(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Tokens != 1102 || f.TTL != time.Hour || !f.At.Equal(time.Date(2026, 1, 1, 10, 0, 5, 0, time.UTC)) {
		t.Fatalf("got %+v", f)
	}
}

func TestTTLFromEarlierCreationWhenLastOnlyReads(t *testing.T) {
	p := write(t,
		assistant("2026-01-01T10:00:00Z", 1, 500, 0, 0, 500, false),
		assistant("2026-01-01T10:02:00Z", 1, 0, 500, 0, 0, false),
	)
	f, _ := ReadLast(p)
	if f.TTL != 5*time.Minute {
		t.Fatalf("TTL = %v, want 5m", f.TTL)
	}
}

func TestTTLUnknownWithoutAnyCreation(t *testing.T) {
	p := write(t, assistant("2026-01-01T10:00:00Z", 1, 0, 500, 0, 0, false))
	f, _ := ReadLast(p)
	if f.TTL != 0 {
		t.Fatalf("TTL = %v, want 0", f.TTL)
	}
}

func TestCompactMarkerAfterLastAssistant(t *testing.T) {
	p := write(t,
		assistant("2026-01-01T10:00:00Z", 1, 900, 0, 900, 0, false),
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-01-01T10:55:10Z"}`,
	)
	f, _ := ReadLast(p)
	if !f.Compacted || !f.CompactedAt.Equal(time.Date(2026, 1, 1, 10, 55, 10, 0, time.UTC)) {
		t.Fatalf("got %+v", f)
	}
}

func TestNoAssistantEntry(t *testing.T) {
	p := write(t, `{"type":"user","timestamp":"2026-01-01T10:00:00Z"}`)
	if _, err := ReadLast(p); err != ErrNoEntry {
		t.Fatalf("err = %v, want ErrNoEntry", err)
	}
}

func TestPartialLastLineIgnored(t *testing.T) {
	p := write(t, assistant("2026-01-01T10:00:00Z", 1, 10, 0, 10, 0, false), `{"type":"assist`)
	if _, err := ReadLast(p); err != nil {
		t.Fatal(err)
	}
}

func TestReadLastGrowsWindowPastHugeLine(t *testing.T) {
	huge := `{"type":"user","timestamp":"2026-01-01T10:01:00Z","message":{"content":"` + strings.Repeat("x", 3<<20) + `"}}`
	p := write(t, assistant("2026-01-01T10:00:00Z", 1, 10, 0, 10, 0, false), huge)
	f, err := ReadLast(p)
	if err != nil || f.Tokens != 11 {
		t.Fatalf("got %+v, %v", f, err)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "C--proj")
	os.MkdirAll(dir, 0o700)
	want := filepath.Join(dir, "abc.jsonl")
	os.WriteFile(want, []byte("{}\n"), 0o600)
	got, err := Find(root, "abc")
	if err != nil || got != want {
		t.Fatalf("Find = %q, %v", got, err)
	}
	if _, err := Find(root, "nope"); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestSubagentsWrittenSince(t *testing.T) {
	p := write(t, "{}")
	sub := filepath.Join(strings.TrimSuffix(p, ".jsonl"), "subagents")
	os.MkdirAll(sub, 0o700)
	if SubagentsWrittenSince(p, time.Now().Add(-time.Minute)) {
		t.Fatal("empty subagents dir counts as written")
	}
	os.WriteFile(filepath.Join(sub, "agent-1.jsonl"), []byte("{}"), 0o600)
	if !SubagentsWrittenSince(p, time.Now().Add(-time.Minute)) {
		t.Fatal("fresh subagent file not seen")
	}
}
