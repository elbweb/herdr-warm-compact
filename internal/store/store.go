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

func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type Overrides struct {
	path string
	m    map[string]model.Setting
}

func LoadOverrides(path string) (*Overrides, error) {
	o := &Overrides{path: path, m: map[string]model.Setting{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	return o, json.Unmarshal(b, &o.m)
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

// TakeRequests reads and deletes every request in write order. A file that does not parse is deleted.
func TakeRequests(dir string) ([]Request, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Request
	for _, n := range names {
		b, err := os.ReadFile(n)
		os.Remove(n)
		if err != nil {
			continue
		}
		var r Request
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
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
