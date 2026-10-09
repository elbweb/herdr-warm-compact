package run

import (
	"os"
	"path/filepath"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/store"
)

// requestTaker takes waiting request files and, while any *.json remains (the OS may still hold a file
// just after the writer's rename, so a read can fail), schedules another pass with a growing delay.
type requestTaker struct {
	dir      string
	take     func(dir string) ([]store.Request, error)
	handle   func(store.Request)
	schedule func(d time.Duration, f func()) // f must run on the resident's loop
	logf     func(string, ...any)

	pending bool
	delay   time.Duration
	now     func() time.Time // defaults to time.Now
	first   map[string]time.Time
	logged  map[string]bool
}

func (t *requestTaker) pass() {
	reqs, _ := t.take(t.dir)
	for _, r := range reqs {
		t.handle(r)
	}
	left := t.remaining()
	if len(left) == 0 {
		t.delay = 0
		return
	}
	t.noteStuck(left)
	if t.pending {
		return
	}
	switch {
	case t.delay == 0:
		t.delay = 50 * time.Millisecond
	case t.delay < 2*time.Second:
		t.delay *= 2
		if t.delay > 2*time.Second {
			t.delay = 2 * time.Second
		}
	}
	t.pending = true
	t.schedule(t.delay, func() {
		t.pending = false
		t.pass()
	})
}

func (t *requestTaker) remaining() []string {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			out = append(out, e.Name())
		}
	}
	return out
}

// noteStuck logs a file still present 2 s after it was first seen unread, once per file name; a request
// the retry reads in time logs nothing.
func (t *requestTaker) noteStuck(left []string) {
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	if t.first == nil {
		t.first, t.logged = map[string]time.Time{}, map[string]bool{}
	}
	present := map[string]bool{}
	for _, n := range left {
		present[n] = true
		if _, ok := t.first[n]; !ok {
			t.first[n] = now()
		}
		if !t.logged[n] && now().Sub(t.first[n]) >= 2*time.Second {
			t.logged[n] = true
			t.logf("request %s is still unreadable after 2 s; retrying", n)
		}
	}
	for n := range t.first {
		if !present[n] {
			delete(t.first, n)
			delete(t.logged, n)
		}
	}
}
