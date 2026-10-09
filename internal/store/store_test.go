package store

import (
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
	o.Set("s1", model.Off)
	o.Set("s2", model.On)
	o.Set("s2", model.Inherit) // back to default removes it
	o2, _ := LoadOverrides(p)
	if o2.Get("s1") != model.Off || o2.Get("s2") != model.Inherit {
		t.Fatalf("reloaded: %v %v", o2.Get("s1"), o2.Get("s2"))
	}
}

func TestRequestsRoundTripInOrderAndAreConsumed(t *testing.T) {
	dir := t.TempDir()
	WriteRequest(dir, Request{Kind: "set", Pane: "w1:p1", Value: "off"})
	WriteRequest(dir, Request{Kind: "toggle", Pane: "w1:p2"})
	got, err := TakeRequests(dir)
	if err != nil || len(got) != 2 || got[0].Kind != "set" || got[1].Pane != "w1:p2" {
		t.Fatalf("%+v %v", got, err)
	}
	again, _ := TakeRequests(dir)
	if len(again) != 0 {
		t.Fatal("requests not consumed")
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
