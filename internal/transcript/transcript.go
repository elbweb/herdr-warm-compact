// Package transcript reads what the plugin needs from a Claude Code session transcript: when the main
// thread last called the API, how big its context is, which cache lifetime it used, and whether the
// session was compacted since. The format is internal to Claude Code; anything unrecognised is an error,
// never a guess.
package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Facts struct {
	At          time.Time     // the last main-thread assistant entry
	Tokens      int           // context size at that entry
	TTL         time.Duration // cache lifetime of the latest cache write; 0 = none seen
	Compacted   bool          // a compaction marker follows the last assistant entry
	CompactedAt time.Time
}

var (
	ErrNotFound     = errors.New("transcript not found")
	ErrNoEntry      = errors.New("no main-thread assistant entry in the transcript")
	ErrUnrecognised = errors.New("unrecognised transcript entry")
)

const (
	firstWindow = 256 << 10
	maxWindow   = 64 << 20
)

type usage struct {
	Input         int `json:"input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	Breakdown     *struct {
		H1 int `json:"ephemeral_1h_input_tokens"`
		M5 int `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

type entry struct {
	Type        string    `json:"type"`
	Subtype     string    `json:"subtype"`
	IsSidechain bool      `json:"isSidechain"`
	Timestamp   time.Time `json:"timestamp"`
	Message     *struct {
		Usage *usage `json:"usage"`
	} `json:"message"`
}

func isCompactMarker(e entry) bool { return e.Type == "system" && e.Subtype == "compact_boundary" }

// Find returns <projectsDir>/<any project>/<sessionID>.jsonl.
func Find(projectsDir, sessionID string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", ErrNotFound
	}
	return matches[0], nil
}

// ReadLast reads the file's tail, growing the window until it holds a main-thread assistant entry.
func ReadLast(path string) (Facts, error) {
	f, err := os.Open(path)
	if err != nil {
		return Facts{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Facts{}, err
	}
	for window := int64(firstWindow); ; window *= 4 {
		start := st.Size() - window
		if start < 0 {
			start = 0
		}
		buf := make([]byte, st.Size()-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return Facts{}, err
		}
		if start > 0 { // the first line is cut; drop it
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil
			}
		}
		facts, found, err := scan(buf)
		if err != nil {
			return Facts{}, err
		}
		complete := start == 0 || window >= maxWindow
		if found && (facts.TTL != 0 || complete) { // TTL unknown: the cache write may sit further back
			return facts, nil
		}
		if complete {
			return Facts{}, ErrNoEntry
		}
	}
}

// scan parses every line of the window. A line that does not parse is tolerated only as the last
// non-empty line and only when it is not valid JSON (a half-written tail); anywhere else it is
// ErrUnrecognised. An assistant entry without usage is skipped.
func scan(buf []byte) (Facts, bool, error) {
	var facts Facts
	found := false
	ttlKnown := false
	lines := bytes.Split(buf, []byte{'\n'})
	last := len(lines) - 1
	for last >= 0 && len(bytes.TrimSpace(lines[last])) == 0 {
		last--
	}
	for i := last; i >= 0; i-- {
		line := lines[i]
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			if i == last && !json.Valid(line) {
				continue
			}
			return Facts{}, false, ErrUnrecognised
		}
		if !found && !facts.Compacted && isCompactMarker(e) {
			facts.Compacted, facts.CompactedAt = true, e.Timestamp
			continue
		}
		if e.Type != "assistant" || e.IsSidechain || e.Message == nil || e.Message.Usage == nil {
			continue
		}
		u := e.Message.Usage
		if !found {
			found = true
			facts.At = e.Timestamp
			facts.Tokens = u.Input + u.CacheCreation + u.CacheRead
		}
		if b := u.Breakdown; !ttlKnown && b != nil && (b.H1 > 0 || b.M5 > 0) {
			if b.H1 > 0 {
				facts.TTL = time.Hour
			} else {
				facts.TTL = 5 * time.Minute
			}
			ttlKnown = true
		}
	}
	return facts, found, nil
}

// SubagentsWrittenSince reports whether any subagent transcript of this session changed after t.
func SubagentsWrittenSince(transcriptPath string, t time.Time) bool {
	dir := filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, d := range entries {
		if info, err := d.Info(); err == nil && info.ModTime().After(t) {
			return true
		}
	}
	return false
}
