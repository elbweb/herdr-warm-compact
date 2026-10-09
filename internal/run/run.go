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
	"strconv"
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
	if err := claimSingleInstance(env.ConfigDir); err != nil {
		return err
	}
	os.WriteFile(filepath.Join(env.ConfigDir, "exe-path"), []byte(env.Exe), 0o600)

	identity := herdr.ServerIdentity(env.Socket)
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
	watcher.Add(env.ConfigDir)
	watcher.Add(store.RequestsDir(env.ConfigDir))

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
	resync()
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
				reqs, _ := store.TakeRequests(store.RequestsDir(env.ConfigDir))
				for _, r := range reqs {
					eng.Request(r)
				}
			}
		case err := <-watcher.Errors:
			logf("watch: %v", err)
		case <-minute.C:
			if herdr.ServerIdentity(env.Socket) != identity {
				logf("herdr server changed; exiting so the new server's copy runs")
				return nil
			}
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
		backoff = time.Second
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

// claimSingleInstance writes our pid, refusing when the pid file names a live process.
func claimSingleInstance(dir string) error {
	pidFile := filepath.Join(dir, "warm-compact.pid")
	if b, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid != os.Getpid() && alive(pid) {
			return fmt.Errorf("already running as pid %d", pid)
		}
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// Stop kills the resident named by the pid file, if any.
func Stop(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, "warm-compact.pid"))
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && alive(pid) {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	}
}
