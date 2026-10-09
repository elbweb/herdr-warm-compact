package run

import (
	"testing"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/store"
)

func TestRequestTakerRetriesUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	rd := store.RequestsDir(dir)
	if err := store.WriteRequest(rd, store.Request{Kind: "set", Pane: "p1", Value: "off"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var got []store.Request
	var delays []time.Duration
	var queued []func()
	rt := &requestTaker{
		dir: rd,
		take: func(d string) ([]store.Request, error) {
			calls++
			if calls == 1 { // the OS still holds the file: nothing could be read
				return nil, nil
			}
			return store.TakeRequests(d)
		},
		handle:   func(r store.Request) { got = append(got, r) },
		schedule: func(d time.Duration, f func()) { delays = append(delays, d); queued = append(queued, f) },
		logf:     func(string, ...any) {},
	}
	rt.pass()
	if len(got) != 0 || len(queued) != 1 || delays[0] != 50*time.Millisecond {
		t.Fatalf("after first pass: got=%v queued=%d delays=%v", got, len(queued), delays)
	}
	rt.pass() // a watcher event meanwhile: applies at once, schedules nothing more
	if len(got) != 1 || got[0].Value != "off" || len(queued) != 1 {
		t.Fatalf("after event pass: got=%v queued=%d", got, len(queued))
	}
	queued[0]() // the stale retry finds an empty folder
	if len(queued) != 1 || rt.pending {
		t.Fatalf("retry scheduled on an empty folder: queued=%d pending=%v", len(queued), rt.pending)
	}
}

func TestRequestTakerBackoffCapsAtTwoSeconds(t *testing.T) {
	dir := t.TempDir()
	rd := store.RequestsDir(dir)
	store.WriteRequest(rd, store.Request{Kind: "set", Pane: "p", Value: "on"})
	var delays []time.Duration
	var next func()
	rt := &requestTaker{dir: rd,
		take:     func(string) ([]store.Request, error) { return nil, nil },
		handle:   func(store.Request) {},
		schedule: func(d time.Duration, f func()) { delays = append(delays, d); next = f },
		logf:     func(string, ...any) {},
	}
	rt.pass()
	for i := 0; i < 8; i++ {
		next()
	}
	want := []time.Duration{50, 100, 200, 400, 800, 1600, 2000, 2000, 2000}
	for i, w := range want {
		if delays[i] != w*time.Millisecond {
			t.Fatalf("delays %v", delays)
		}
	}
}

func TestRequestTakerNoRetryWhenEmpty(t *testing.T) {
	rt := &requestTaker{dir: store.RequestsDir(t.TempDir()), take: store.TakeRequests,
		handle:   func(store.Request) {},
		schedule: func(time.Duration, func()) { t.Fatal("scheduled with an empty folder") },
		logf:     func(string, ...any) {},
	}
	rt.pass()
}

func TestRequestTakerLogsOnlyAfterTwoSeconds(t *testing.T) {
	dir := t.TempDir()
	rd := store.RequestsDir(dir)
	store.WriteRequest(rd, store.Request{Kind: "set", Pane: "p", Value: "on"})
	clock := time.Unix(1000, 0)
	logs := 0
	var next func()
	rt := &requestTaker{dir: rd, now: func() time.Time { return clock },
		take:     func(string) ([]store.Request, error) { return nil, nil },
		handle:   func(store.Request) {},
		schedule: func(d time.Duration, f func()) { next = f },
		logf:     func(string, ...any) { logs++ },
	}
	rt.pass()
	clock = clock.Add(500 * time.Millisecond)
	next()
	if logs != 0 {
		t.Fatalf("logged %d before 2 s", logs)
	}
	clock = clock.Add(2 * time.Second)
	next()
	next()
	if logs != 1 {
		t.Fatalf("logged %d, want exactly 1", logs)
	}
}
