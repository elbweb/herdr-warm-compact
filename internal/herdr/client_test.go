package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// fake answers one request per connection with the reply func; it records the requests.
type fake struct {
	got   []map[string]any
	reply func(method string) string
	lines []string // for subscriptions: lines sent after the reply
}

func (f *fake) client() *Client {
	c := New("unused")
	c.Dial = func(context.Context) (io.ReadWriteCloser, error) {
		a, b := net.Pipe()
		go func() {
			r := bufio.NewReader(b)
			line, _ := r.ReadBytes('\n')
			var req map[string]any
			json.Unmarshal(line, &req)
			f.got = append(f.got, req)
			b.Write([]byte(f.reply(req["method"].(string)) + "\n"))
			for _, l := range f.lines {
				b.Write([]byte(l + "\n"))
			}
			if len(f.lines) == 0 {
				b.Close()
			}
		}()
		return a, nil
	}
	return c
}

func TestAgentsFromSnapshot(t *testing.T) {
	f := &fake{reply: func(string) string {
		return `{"id":"1","result":{"type":"session_snapshot","snapshot":{"workspaces":[{"workspace_id":"w1","label":"proj"}],"agents":[` +
			`{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"idle","cwd":"/x","terminal_title_stripped":"topic","agent_session":{"agent":"claude","kind":"id","value":"s-1"}},` +
			`{"pane_id":"w1:p2","workspace_id":"w1","agent":"codex","agent_status":"idle"}]}}}`
	}}
	agents, ws, err := f.client().Agents(context.Background())
	if err != nil || len(agents) != 2 || agents[0].SessionID != "s-1" || agents[0].Title != "topic" || ws["w1"] != "proj" || agents[1].SessionID != "" {
		t.Fatalf("%+v %v %v", agents, ws, err)
	}
	if f.got[0]["method"] != "session.snapshot" {
		t.Fatal(f.got[0])
	}
}

func TestErrorReply(t *testing.T) {
	f := &fake{reply: func(string) string { return `{"id":"1","error":{"code":"pane_not_found","message":"gone"}}` }}
	if err := f.client().SendKeys(context.Background(), "w1:p1", "ctrl+s"); err == nil || ErrorCode(err) != "pane_not_found" {
		t.Fatalf("%v", err)
	}
}

func TestTokensParams(t *testing.T) {
	f := &fake{reply: func(string) string { return `{"id":"1","result":{}}` }}
	v := "⏱ 38m"
	if err := f.client().Tokens(context.Background(), "w1:p1", map[string]*string{"compact": &v}, 180_000_000_000); err != nil {
		t.Fatal(err)
	}
	p := f.got[0]["params"].(map[string]any)
	if f.got[0]["method"] != "pane.report_metadata" || p["source"] != "herdr.warm-compact" || p["ttl_ms"].(float64) != 180000 ||
		p["tokens"].(map[string]any)["compact"] != "⏱ 38m" {
		t.Fatal(f.got[0])
	}
}

func TestOpenPanelPassesPlacement(t *testing.T) {
	f := &fake{reply: func(string) string { return `{"id":"1","result":{}}` }}
	if err := f.client().OpenPanel(context.Background(), "tab"); err != nil {
		t.Fatal(err)
	}
	p := f.got[0]["params"].(map[string]any)
	if f.got[0]["method"] != "plugin.pane.open" || p["entrypoint"] != "panel" || p["placement"] != "tab" {
		t.Fatal(f.got[0])
	}
}

func TestReadReturnsResultReadText(t *testing.T) {
	f := &fake{reply: func(string) string {
		return `{"id":"1","result":{"type":"pane_read","read":{"pane_id":"w1:p1","text":"hello screen"}}}`
	}}
	got, err := f.client().Read(context.Background(), "w1:p1")
	if err != nil || got != "hello screen" {
		t.Fatalf("%q %v", got, err)
	}
	if f.got[0]["method"] != "pane.read" {
		t.Fatal(f.got[0])
	}
}

func TestSubscribeDecodesEvents(t *testing.T) {
	f := &fake{
		reply: func(string) string { return `{"id":"1","result":{"type":"subscription_started"}}` },
		lines: []string{
			`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"working"}}`,
			`{"event":"pane.closed","data":{"pane_id":"w1:p1","workspace_id":"w1"}}`,
			`{"event":"events.lost","data":{}}`,
			`{"event":"pane_closed","data":{"pane_id":"w1:p2"}}`,
			`{"event":"events_lost","data":{}}`,
		},
	}
	s, err := f.client().Subscribe(context.Background(), []string{"w1:p1"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e1, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	e3, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	e4, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	e5, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if e1.Kind != "pane.agent_status_changed" || e1.Status != "working" || e2.Kind != "pane.closed" || !e3.Lost {
		t.Fatalf("%+v %+v %+v", e1, e2, e3)
	}
	if e4.Kind != "pane.closed" || e4.PaneID != "w1:p2" || !e5.Lost {
		t.Fatalf("%+v %+v", e4, e5)
	}
	subs := f.got[0]["params"].(map[string]any)["subscriptions"].([]any)
	if len(subs) != 5 { // 4 global + 1 per pane
		t.Fatalf("subs %v", subs)
	}
}

func TestSubscribeHandshakeTimesOut(t *testing.T) {
	c := New("unused")
	c.Timeout = 100 * time.Millisecond
	c.Dial = func(context.Context) (io.ReadWriteCloser, error) {
		a, b := net.Pipe()
		go io.Copy(io.Discard, b) // accepts, reads, never answers
		return a, nil
	}
	start := time.Now()
	if _, err := c.Subscribe(context.Background(), nil); err == nil || time.Since(start) > time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func TestAbsent(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	missing := &fs.PathError{Op: "open", Path: "sock", Err: fs.ErrNotExist}
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{refused, true},
		{missing, true},
		{context.DeadlineExceeded, false},
		{&APIError{Code: "x", Message: "y"}, false},
		{errors.New("other"), false},
	}
	for _, c := range cases {
		if got := Absent(c.err); got != c.want {
			t.Errorf("Absent(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestServerIdentityOfMissingSocket(t *testing.T) {
	if id := ServerIdentity(filepath.Join(t.TempDir(), "none")); id != "" {
		t.Fatalf("identity of a missing socket: %q", id)
	}
}
