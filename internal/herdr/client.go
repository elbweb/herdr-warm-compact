// Package herdr is a small client for herdr's local socket API (protocol 22): newline-delimited JSON,
// one request per connection, except a subscription, which stays open.
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const Source = "herdr.warm-compact"

type APIError struct{ Code, Message string }

func (e *APIError) Error() string { return fmt.Sprintf("herdr %s: %s", e.Code, e.Message) }

func ErrorCode(err error) string {
	var a *APIError
	if errors.As(err, &a) {
		return a.Code
	}
	return ""
}

type Client struct {
	Socket  string
	Timeout time.Duration
	Dial    func(ctx context.Context) (io.ReadWriteCloser, error)
	seq     atomic.Int64
}

func New(socket string) *Client {
	c := &Client{Socket: socket, Timeout: 5 * time.Second}
	c.Dial = func(ctx context.Context) (io.ReadWriteCloser, error) { return dial(ctx, socket) }
	return c
}

type frame struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) open(ctx context.Context, method string, params any) (io.ReadWriteCloser, *bufio.Reader, json.RawMessage, error) {
	if params == nil {
		params = struct{}{}
	}
	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	req, _ := json.Marshal(map[string]any{"id": fmt.Sprint(c.seq.Add(1)), "method": method, "params": params})
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-done: // open already returned: the caller owns conn now
			default:
				conn.Close()
			}
		case <-done:
		}
	}()
	defer close(done)
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	r := bufio.NewReaderSize(conn, 1<<20)
	line, err := r.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("%s: no answer from herdr: %w", method, err)
	}
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	if f.Error != nil {
		conn.Close()
		return nil, nil, nil, &APIError{f.Error.Code, f.Error.Message}
	}
	return conn, r, f.Result, nil
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	conn, _, res, err := c.open(ctx, method, params)
	if err != nil {
		return err
	}
	conn.Close()
	if out != nil {
		return json.Unmarshal(res, out)
	}
	return nil
}

type Agent struct {
	PaneID, WorkspaceID, Agent, Status, CWD, Title string
	SessionID                                      string
}

func (c *Client) Agents(ctx context.Context) ([]Agent, map[string]string, error) {
	var res struct {
		Snapshot struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
			Agents []struct {
				PaneID      string `json:"pane_id"`
				WorkspaceID string `json:"workspace_id"`
				Agent       string `json:"agent"`
				Status      string `json:"agent_status"`
				CWD         string `json:"cwd"`
				Title       string `json:"terminal_title_stripped"`
				Session     *struct {
					Agent string `json:"agent"`
					Kind  string `json:"kind"`
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agents"`
		} `json:"snapshot"`
	}
	if err := c.call(ctx, "session.snapshot", nil, &res); err != nil {
		return nil, nil, err
	}
	ws := map[string]string{}
	for _, w := range res.Snapshot.Workspaces {
		ws[w.ID] = w.Label
	}
	var out []Agent
	for _, a := range res.Snapshot.Agents {
		ag := Agent{PaneID: a.PaneID, WorkspaceID: a.WorkspaceID, Agent: a.Agent, Status: a.Status, CWD: a.CWD, Title: a.Title}
		if s := a.Session; s != nil && s.Agent == a.Agent && s.Kind == "id" {
			ag.SessionID = s.Value
		}
		out = append(out, ag)
	}
	return out, ws, nil
}

func (c *Client) Read(ctx context.Context, pane string) (string, error) {
	var res struct {
		Text string `json:"text"`
		Read *struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	err := c.call(ctx, "pane.read", map[string]any{"pane_id": pane, "source": "visible", "format": "ansi", "strip_ansi": false}, &res)
	if err != nil {
		return "", err
	}
	if res.Read != nil {
		return res.Read.Text, nil
	}
	return res.Text, nil
}

func (c *Client) SendKeys(ctx context.Context, pane string, keys ...string) error {
	return c.call(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": keys}, nil)
}

func (c *Client) SendText(ctx context.Context, pane, text string) error {
	return c.call(ctx, "pane.send_text", map[string]any{"pane_id": pane, "text": text}, nil)
}

func (c *Client) Tokens(ctx context.Context, pane string, tokens map[string]*string, ttl time.Duration) error {
	return c.call(ctx, "pane.report_metadata", map[string]any{
		"pane_id": pane, "source": Source, "tokens": tokens, "ttl_ms": ttl.Milliseconds(),
	}, nil)
}

func (c *Client) Notify(ctx context.Context, title, body string) error {
	return c.call(ctx, "notification.show", map[string]any{"title": title, "body": body}, nil)
}

// OpenPanel opens the panel as placement ("popup" or "tab"), overriding the manifest's popup.
func (c *Client) OpenPanel(ctx context.Context, placement string) error {
	return c.call(ctx, "plugin.pane.open", map[string]any{"plugin_id": Source, "entrypoint": "panel", "placement": placement, "focus": true}, nil)
}

type Event struct {
	Kind, PaneID, Status string
	Lost                 bool
}

type Stream struct {
	conn io.Closer
	r    *bufio.Reader
}

// Subscribe opens one stream: the global pane events plus status changes of the given panes.
// ctx scopes only the handshake (bounded by c.Timeout); call Close to stop Next.
func (c *Client) Subscribe(ctx context.Context, paneIDs []string) (*Stream, error) {
	subs := []map[string]any{{"type": "pane.created"}, {"type": "pane.closed"}, {"type": "pane.updated"}, {"type": "pane.agent_detected"}}
	for _, id := range paneIDs {
		subs = append(subs, map[string]any{"type": "pane.agent_status_changed", "pane_id": id})
	}
	hctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	conn, r, _, err := c.open(hctx, "events.subscribe", map[string]any{"subscriptions": subs})
	if err != nil {
		return nil, err
	}
	return &Stream{conn: conn, r: r}, nil
}

func (s *Stream) Close() error { return s.conn.Close() }

// Next blocks for the next event; ctx scopes only the handshake, so call Close to stop it.
func (s *Stream) Next() (Event, error) {
	line, err := s.r.ReadBytes('\n')
	if err != nil {
		return Event{}, err
	}
	return decodeEvent(line)
}

func decodeEvent(line []byte) (Event, error) {
	var raw struct {
		Event string `json:"event"`
		Data  struct {
			PaneID string `json:"pane_id"`
			Status string `json:"agent_status"`
			Pane   *struct {
				PaneID string `json:"pane_id"`
				Status string `json:"agent_status"`
			} `json:"pane"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, err
	}
	kind := raw.Event
	if rest, ok := strings.CutPrefix(kind, "pane_"); ok {
		kind = "pane." + rest
	}
	e := Event{Kind: kind, PaneID: raw.Data.PaneID, Status: raw.Data.Status, Lost: raw.Event == "events.lost" || raw.Event == "events_lost"}
	if p := raw.Data.Pane; p != nil {
		e.PaneID, e.Status = p.PaneID, p.Status
	}
	return e, nil
}

// ServerIdentity names the running herdr server behind HERDR_SOCKET_PATH, so a new server yields a new
// identity; "" when unreadable. See identity_windows.go and identity_unix.go.
func ServerIdentity(socket string) string { return serverIdentity(socket) }

// Absent reports a dial failure that means no server is there: the socket or pipe does not exist, or
// the connection was refused. Timeouts and herdr's own errors mean a server answered or exists.
func Absent(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED))
}
