// Package store holds the plugin's files in its config dir: per-session overrides, the request drop
// folder short-lived commands write to, and the status file the panel reads.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/model"
)

// writeAtomic writes v as JSON to a unique temp file in path's directory, syncs it, and renames it over
// path. On Windows a rename fails while another process has the target open (the panel reads status.json
// while the resident rewrites it), so the rename is retried: up to 5 attempts, 20 ms apart. If every
// attempt fails the temp file is removed and the error returned.
func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	for attempt := 0; ; attempt++ {
		err = os.Rename(tmp, path)
		if err == nil {
			return nil
		}
		if attempt == 4 {
			os.Remove(tmp)
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Overrides is not safe for concurrent use; the engine's loop goroutine owns it.
type Overrides struct {
	path string
	m    map[string]model.Setting
}

// LoadOverrides reads the overrides file. A missing file, or a file holding null, is an empty set. A
// corrupt file returns an error together with a usable empty Overrides.
func LoadOverrides(path string) (*Overrides, error) {
	o := &Overrides{path: path, m: map[string]model.Setting{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	var m map[string]model.Setting
	if err := json.Unmarshal(b, &m); err != nil {
		return o, err
	}
	if m != nil {
		o.m = m
	}
	return o, nil
}

func (o *Overrides) Get(session string) model.Setting { return o.m[session] }

func (o *Overrides) Set(session string, s model.Setting) error {
	if s == model.Inherit {
		delete(o.m, session)
	} else {
		o.m[session] = s
	}
	return writeAtomic(o.path, o.m)
}

type Request struct {
	Kind  string `json:"kind"` // set, toggle, skip, compact_now
	Pane  string `json:"pane,omitempty"`
	Value string `json:"value,omitempty"`
}

func RequestsDir(configDir string) string { return filepath.Join(configDir, "requests") }

var seq atomic.Int64

// WriteRequest drops one request file; names sort in write order.
func WriteRequest(dir string, r Request) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%06d-%d.json", time.Now().UnixNano(), seq.Add(1), os.Getpid())
	return writeAtomic(filepath.Join(dir, name), r)
}

// TakeRequests returns and deletes the requests in write order. Each file is read first: a read error
// leaves it for the next pass; a file that does not parse is removed and dropped; a valid request is
// returned only once its file is removed, so if the removal fails it is not applied this pass and
// stays for the next one (a toggle never applies twice). A missing dir is no requests.
func TakeRequests(dir string) ([]Request, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); filepath.Ext(n) == ".json" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []Request
	for _, n := range names {
		path := filepath.Join(dir, n)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var r Request
		if json.Unmarshal(b, &r) != nil {
			os.Remove(path)
			continue
		}
		if os.Remove(path) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

type Row struct {
	Pane      string        `json:"pane"`
	Session   string        `json:"session"`
	Name      string        `json:"name"`
	Workspace string        `json:"workspace"`
	Folder    string        `json:"folder"`
	Tokens    int           `json:"tokens"`
	TTL       time.Duration `json:"ttl"`
	Override  model.Setting `json:"override"`
	Effective model.Setting `json:"effective"`
	Phase     model.Phase   `json:"phase"`
	Reason    string        `json:"reason"`
	Deadline  time.Time     `json:"deadline"`
}

type Status struct {
	PID         int           `json:"pid"`
	Exe         string        `json:"exe"`
	Started     time.Time     `json:"started"`
	LastEvent   time.Time     `json:"last_event"`
	ConfigError string        `json:"config_error"`
	Default     model.Setting `json:"default"`
	Rows        []Row         `json:"rows"`
}

func WriteStatus(path string, s Status) error { return writeAtomic(path, s) }

func ReadStatus(path string) (Status, error) {
	var s Status
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}
