package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
)

func TestOverridesPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "overrides.json")
	o, err := LoadOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if o.Get("s1") != model.Inherit {
		t.Fatal("unknown session is not Inherit")
	}
	if err := o.Set("s1", model.Off); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("s2", model.On); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("s2", model.Inherit); err != nil { // back to default removes it
		t.Fatal(err)
	}
	o2, _ := LoadOverrides(p)
	if o2.Get("s1") != model.Off || o2.Get("s2") != model.Inherit {
		t.Fatalf("reloaded: %v %v", o2.Get("s1"), o2.Get("s2"))
	}
}

func TestOverridesNullFileIsUsable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "overrides.json")
	if err := os.WriteFile(p, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := LoadOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Set("s1", model.On); err != nil {
		t.Fatalf("Set after null load: %v", err)
	}
	if o.Get("s1") != model.On {
		t.Fatal("Set after null load did not stick")
	}
}

func TestOverridesCorruptFileReturnsErrorAndUsableEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "overrides.json")
	if err := os.WriteFile(p, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := LoadOverrides(p)
	if err == nil {
		t.Fatal("corrupt overrides file should return an error")
	}
	if o == nil || o.Get("s1") != model.Inherit {
		t.Fatal("corrupt load should return an empty usable Overrides")
	}
	if err := o.Set("s1", model.Auto); err != nil {
		t.Fatalf("Set after corrupt load: %v", err)
	}
}

func TestRequestsRoundTripInOrderAndAreConsumed(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRequest(dir, Request{Kind: "set", Pane: "w1:p1", Value: "off"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteRequest(dir, Request{Kind: "toggle", Pane: "w1:p2"}); err != nil {
		t.Fatal(err)
	}
	got, err := TakeRequests(dir)
	if err != nil || len(got) != 2 || got[0].Kind != "set" || got[1].Pane != "w1:p2" {
		t.Fatalf("%+v %v", got, err)
	}
	again, _ := TakeRequests(dir)
	if len(again) != 0 {
		t.Fatal("requests not consumed")
	}
}

func TestTakeRequestsRemovesInvalidAndReturnsValidOnce(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "00000000000000000001-000001-1.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteRequest(dir, Request{Kind: "toggle", Pane: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	got, err := TakeRequests(dir)
	if err != nil || len(got) != 1 || got[0].Kind != "toggle" {
		t.Fatalf("got %+v %v; want the one valid request", got, err)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("invalid request file should be removed")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(left) != 0 {
		t.Fatalf("valid request file should be gone, found %v", left)
	}
	again, _ := TakeRequests(dir)
	if len(again) != 0 {
		t.Fatal("valid request applied twice")
	}
}

func TestTakeRequestsSkipsUnreadableAndLeavesItForNextPass(t *testing.T) {
	dir := t.TempDir()
	// A directory named like a request cannot be read as a file; it must be left alone.
	stuck := filepath.Join(dir, "00000000000000000001-000001-1.json")
	if err := os.Mkdir(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := TakeRequests(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v %v; want none", got, err)
	}
	if fi, err := os.Stat(stuck); err != nil || !fi.IsDir() {
		t.Fatal("unreadable request should be left for the next pass")
	}
}

func TestTakeRequestsIgnoresTempFilesAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "00000000000000000001-000001-1.json.123.tmp")
	if err := os.WriteFile(tmp, []byte(`{"kind":"toggle"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := TakeRequests(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("temp file should be ignored, got %+v %v", got, err)
	}
	missing, err := TakeRequests(filepath.Join(dir, "absent"))
	if err != nil || missing != nil {
		t.Fatalf("missing dir: got %+v %v; want nil, nil", missing, err)
	}
}

func TestTakeRequestsDirWithGlobMetacharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "req[1]")
	if err := WriteRequest(dir, Request{Kind: "skip", Pane: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	got, err := TakeRequests(dir)
	if err != nil || len(got) != 1 || got[0].Kind != "skip" {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "status.json")
	if err := WriteStatus(p, Status{PID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(p, Status{PID: 2}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "status.json" {
		t.Fatalf("expected only status.json, got %v", entries)
	}
	s, err := ReadStatus(p)
	if err != nil || s.PID != 2 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	in := Status{PID: 7, Default: model.Auto, Rows: []Row{{Pane: "w1:p1", TTL: time.Hour, Phase: model.Armed}}}
	if err := WriteStatus(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadStatus(p)
	if err != nil || out.PID != 7 || out.Rows[0].TTL != time.Hour || out.Rows[0].Phase != model.Armed {
		t.Fatalf("%+v %v", out, err)
	}
}
