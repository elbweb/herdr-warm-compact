// Package run is the resident: it keeps herdr's event stream, feeds the engine on one goroutine, watches
// the config file and the request folder, and writes the status file the panel reads.
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elbweb/herdr-warm-compact/internal/config"
	"github.com/elbweb/herdr-warm-compact/internal/engine"
	"github.com/elbweb/herdr-warm-compact/internal/herdr"
	"github.com/elbweb/herdr-warm-compact/internal/store"
	"github.com/elbweb/herdr-warm-compact/internal/transcript"
	"github.com/fsnotify/fsnotify"
)

const PluginID = "herdr.warm-compact"

type Env struct {
	Socket, ConfigDir, ProjectsDir, Exe string
}

func EnvFromOS() (Env, error) {
	exe, _ := os.Executable()
	home, _ := os.UserHomeDir()
	env := Env{Socket: os.Getenv("HERDR_SOCKET_PATH"), ConfigDir: os.Getenv("HERDR_PLUGIN_CONFIG_DIR"),
		ProjectsDir: filepath.Join(home, ".claude", "projects"), Exe: exe}
	if env.Socket == "" || env.ConfigDir == "" {
		return env, errors.New("HERDR_SOCKET_PATH and HERDR_PLUGIN_CONFIG_DIR must be set (run me from herdr)")
	}
	return env, nil
}

// ConfigDir finds the plugin's config dir from outside herdr (the `set` command run by Claude).
func ConfigDir() (string, error) {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d, nil
	}
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	out, err := exec.Command(bin, "plugin", "config-dir", PluginID).Output()
	if err != nil {
		return "", fmt.Errorf("herdr plugin config-dir: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ToEnginePane(a herdr.Agent, ws map[string]string) (engine.Pane, bool) {
	if a.Agent != "claude" || a.SessionID == "" {
		return engine.Pane{}, false
	}
	return engine.Pane{ID: a.PaneID, Session: a.SessionID, Workspace: ws[a.WorkspaceID], Folder: a.CWD, Name: a.Title, Status: a.Status}, true
}

type realClock struct{}

func (realClock) Now() time.Time                                   { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) engine.Timer { return time.AfterFunc(d, f) }

type transcripts struct{ dir string }

func (t transcripts) Facts(session string) (transcript.Facts, error) {
	p, err := transcript.Find(t.dir, session)
	if err != nil {
		return transcript.Facts{}, err
	}
	return transcript.ReadLast(p)
}

func (t transcripts) SubagentsSince(session string, since time.Time) bool {
	p, err := transcript.Find(t.dir, session)
	return err == nil && transcript.SubagentsWrittenSince(p, since)
}

// cappedLog keeps warm-compact.log under 1 MB, rolling once to warm-compact.log.1.
type cappedLog struct{ path string }

func (c cappedLog) Write(b []byte) (int, error) {
	if st, err := os.Stat(c.path); err == nil && st.Size() > 1<<20 {
		os.Rename(c.path, c.path+".1")
	}
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(b)
}

func Resident(ctx context.Context, env Env) error {
	if err := os.MkdirAll(store.RequestsDir(env.ConfigDir), 0o700); err != nil {
		return err
	}
	logger := log.New(io.Writer(cappedLog{filepath.Join(env.ConfigDir, "warm-compact.log")}), "", log.LstdFlags)
	logf := logger.Printf
	err := resident(ctx, env, logf)
	if err != nil {
		logf("exiting: %v", err)
	}
	return err
}

// claim takes the single-instance lock. A holder from another herdr server is asked to quit; a holder
// from this server means a duplicate startup, reported as (nil, nil).
func claim(env Env, identity string, logf func(string, ...any)) (*Lock, error) {
	try := func(d time.Duration) (*Lock, error) {
		deadline := time.Now().Add(d)
		for {
			l, err := acquireLock(env.ConfigDir)
			if err != errLocked {
				return l, err
			}
			if time.Now().After(deadline) {
				return nil, errLocked
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	l, err := try(time.Second)
	if err != errLocked {
		return l, err
	}
	h, _ := readHolder(env.ConfigDir)
	if h.Server == "" || h.Server == identity {
		logf("already running as pid %d", h.PID)
		return nil, nil
	}
	logf("the holder (pid %d) belongs to another herdr server; asking it to quit", h.PID)
	if err := store.WriteRequest(store.RequestsDir(env.ConfigDir), store.Request{Kind: "quit"}); err != nil {
		return nil, err
	}
	if l, err = try(15 * time.Second); err != nil {
		return nil, fmt.Errorf("cannot take the lock: %w", err)
	}
	return l, nil
}

func resident(ctx context.Context, env Env, logf func(string, ...any)) error {
	identity := herdr.ServerIdentity(env.Socket)
	lock, err := claim(env, identity, logf)
	if err != nil {
		return err
	}
	if lock == nil {
		return nil
	}
	defer lock.Release()
	if err := lock.Write(LockInfo{PID: os.Getpid(), Server: identity, Started: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		logf("lock info: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.ConfigDir, "exe-path"), []byte(env.Exe), 0o600); err != nil {
		logf("exe-path: %v", err)
	}

	client := herdr.New(env.Socket)
	cfgPath := filepath.Join(env.ConfigDir, "config.toml")
	cfg, cfgErr := config.Load(cfgPath)
	ov, err := store.LoadOverrides(filepath.Join(env.ConfigDir, "overrides.json"))
	if err != nil {
		logf("overrides: %v (starting empty)", err)
	}
	work := make(chan func(), 256)
	post := func(f func()) {
		select {
		case work <- f:
		case <-ctx.Done():
		}
	}
	eng := engine.New(client, transcripts{env.ProjectsDir}, realClock{}, ov, cfg, post, logf)

	status := store.Status{PID: os.Getpid(), Exe: env.Exe, Started: time.Now()}
	statusPath := filepath.Join(env.ConfigDir, "status.json")
	var lastStatus []byte
	// writeStatus writes only when the content changed: status.json sits in the watched folder, so an
	// unconditional write would wake this loop again forever.
	writeStatus := func() {
		status.Default = cfg.Default
		status.ConfigError = ""
		if cfgErr != nil {
			status.ConfigError = cfgErr.Error()
		}
		status.Rows = eng.Rows()
		b, _ := json.Marshal(status)
		if bytes.Equal(b, lastStatus) {
			return
		}
		if err := store.WriteStatus(statusPath, status); err != nil {
			logf("status: %v", err)
			return
		}
		lastStatus = b
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(env.ConfigDir); err != nil {
		return fmt.Errorf("watch config dir: %w", err)
	}
	if err := watcher.Add(store.RequestsDir(env.ConfigDir)); err != nil {
		return fmt.Errorf("watch requests dir: %w", err)
	}

	events := make(chan herdr.Event, 256)
	panesChanged := make(chan struct{}, 1)
	go streamLoop(ctx, client, eng, events, panesChanged, post, logf)

	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	var flash *time.Ticker
	var flashC <-chan time.Time

	paneSet := func() string {
		var ids []string
		for _, r := range eng.Rows() {
			ids = append(ids, r.Pane)
		}
		return strings.Join(ids, ",")
	}
	// resync refreshes every Claude pane from one snapshot; it asks the stream to re-subscribe only when
	// the set of panes changed, not on every status change.
	resync := func() {
		before := paneSet()
		agents, ws, err := client.Agents(ctx)
		if err != nil {
			logf("snapshot: %v", err)
			return
		}
		seen := map[string]bool{}
		for _, a := range agents {
			if p, ok := ToEnginePane(a, ws); ok {
				seen[p.ID] = true
				eng.Status(p)
			}
		}
		for _, r := range eng.Rows() {
			if !seen[r.Pane] {
				eng.Closed(r.Pane)
			}
		}
		if paneSet() != before {
			select {
			case panesChanged <- struct{}{}:
			default:
			}
		}
	}
	// takeRequests applies waiting requests; it reports whether one asked the resident to quit.
	takeRequests := func(startup bool) (quit bool) {
		reqs, _ := store.TakeRequests(store.RequestsDir(env.ConfigDir))
		for _, r := range reqs {
			if r.Kind == "quit" {
				// A quit left over from before we started was meant for a previous holder.
				if !startup {
					quit = true
				}
				continue
			}
			eng.Request(r)
		}
		return quit
	}
	resync()
	takeRequests(true)
	writeStatus()
	logf("started pid %d", os.Getpid())

	for {
		select {
		case <-ctx.Done():
			return nil
		case f := <-work:
			f()
		case ev := <-events:
			status.LastEvent = time.Now()
			switch {
			case ev.Lost || ev.Kind == "reconnected" || ev.Kind == "pane.created" || ev.Kind == "pane.agent_detected" || ev.Kind == "pane.updated":
				resync()
			case ev.Kind == "pane.closed":
				eng.Closed(ev.PaneID)
				select {
				case panesChanged <- struct{}{}:
				default:
				}
			case ev.Kind == "pane.agent_status_changed":
				// Refresh the whole record on each change: the session id changes on /clear.
				resync()
			}
		case we := <-watcher.Events:
			switch {
			case filepath.Base(we.Name) == "config.toml":
				if c, err := config.Load(cfgPath); err != nil {
					cfgErr = err
					logf("config: %v (keeping the last good settings)", err)
				} else {
					cfg, cfgErr = c, nil
					eng.SetConfig(c)
				}
			case filepath.Dir(we.Name) == store.RequestsDir(env.ConfigDir) && strings.HasSuffix(we.Name, ".json"):
				if takeRequests(false) {
					logf("quit requested")
					return nil
				}
			default:
				continue
			}
		case err := <-watcher.Errors:
			logf("watch: %v", err)
			if takeRequests(false) {
				logf("quit requested")
				return nil
			}
		case <-minute.C:
			if now := herdr.ServerIdentity(env.Socket); now != "" && now != identity {
				logf("herdr server changed; exiting so the new server's copy runs")
				return nil
			}
			resync()
			eng.Tick()
		case <-flashC:
			eng.Flash()
		}
		if eng.Warnings() && flash == nil {
			flash = time.NewTicker(time.Second)
			flashC = flash.C
		} else if !eng.Warnings() && flash != nil {
			flash.Stop()
			flash, flashC = nil, nil
		}
		writeStatus()
	}
}

// streamLoop keeps one subscription open, re-subscribing when the set of Claude panes changes or the
// stream drops. It sends a synthetic "reconnected" event after each new stream so the loop resyncs.
func streamLoop(ctx context.Context, c *herdr.Client, eng *engine.Engine, out chan<- herdr.Event, changed <-chan struct{}, post func(func()), logf func(string, ...any)) {
	backoff := time.Second
	for ctx.Err() == nil {
		ids := make(chan []string, 1)
		post(func() {
			var l []string
			for _, r := range eng.Rows() {
				l = append(l, r.Pane)
			}
			ids <- l
		})
		var paneIDs []string
		select {
		case paneIDs = <-ids:
		case <-ctx.Done():
			return
		}
		s, err := c.Subscribe(ctx, paneIDs)
		if err != nil {
			logf("subscribe: %v", err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		started := time.Now()
		out <- herdr.Event{Kind: "reconnected"}
		done := make(chan struct{})
		go func() {
			for {
				ev, err := s.Next()
				if err != nil {
					close(done)
					return
				}
				out <- ev
				if ev.Lost {
					s.Close()
				}
			}
		}()
		select {
		case <-done:
			// Reset the backoff only after a stream that lived; a drop-loop backs off.
			if time.Since(started) > 10*time.Second {
				backoff = time.Second
			} else {
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return
				}
				if backoff < time.Minute {
					backoff *= 2
				}
			}
		case <-changed:
			time.Sleep(500 * time.Millisecond) // let a burst of pane changes settle
			s.Close()
			<-done
		case <-ctx.Done():
			s.Close()
			return
		}
	}
}
