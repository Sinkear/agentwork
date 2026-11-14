package link

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// NewPeerForTest constructs a peer whose handlers are registered BEFORE the
// reader loop starts — the race-free equivalent of NewPeerUnserved + Handle +
// StartReadLoop (the production pattern).
func NewPeerForTest(conn *websocket.Conn, handlers map[string]Handler) *Peer {
	p := NewPeerUnserved(conn)
	for m, h := range handlers {
		p.Handle(m, h)
	}
	p.StartReadLoop()
	return p
}

// linkTestServer starts an httptest WS server whose peer handles the given
// methods; the client dials it and both peers are returned.
func linkTestServer(t *testing.T, handlers map[string]Handler) (*Peer, *Peer) {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Register handlers BEFORE the reader loop starts consuming frames.
		// NewPeer starts readLoop immediately; a request arriving before
		// Handle() lands used to race the dispatch ("method not found" under
		// load — live: a deliver-time verification flake). Handlers-first
		// closes the window deterministically.
		peer := NewPeerForTest(conn, handlers)
		peer.Wait()
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// COMPAT: NewPeer must keep starting the reader loop itself — a call-only
	// client has no start step, and Call would otherwise wait for a response
	// nobody reads. TestPeerCallRoundtrip et al. exercise this path.
	client := NewPeer(conn)
	t.Cleanup(client.Close)
	// The server peer is unreachable from here (created inside the handler) —
	// return nil for it; tests exercise the wire through the client.
	return client, nil
}

func TestPeerCallRoundtrip(t *testing.T) {
	client, _ := linkTestServer(t, map[string]Handler{
		"test.echo": func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			var in map[string]string
			_ = json.Unmarshal(params, &in)
			return map[string]any{"got": in["msg"]}, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out struct {
		Got string `json:"got"`
	}
	if err := client.Call(ctx, "test.echo", map[string]string{"msg": "hi"}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Got != "hi" {
		t.Fatalf("expected echo hi, got %+v", out)
	}
}

func TestPeerUnknownMethod(t *testing.T) {
	client, _ := linkTestServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Call(ctx, "nope.missing", nil, nil)
	if err == nil {
		t.Fatalf("expected method-not-found error")
	}
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeMethodNotFnd {
		t.Fatalf("expected -32601 RPCError, got %T %v", err, err)
	}
}

func TestPeerNotifyNoReply(t *testing.T) {
	got := make(chan struct{})
	client, _ := linkTestServer(t, map[string]Handler{
		"test.ping": func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			close(got)
			return nil, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Notify(ctx, "test.ping", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case <-got:
	case <-ctx.Done():
		t.Fatalf("notification never dispatched")
	}
}

func TestPeerHandlerError(t *testing.T) {
	client, _ := linkTestServer(t, map[string]Handler{
		"test.fail": func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			return nil, &RPCError{Code: CodeAuthDenied, Message: "denied"}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Call(ctx, "test.fail", nil, nil)
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeAuthDenied || rpcErr.Message != "denied" {
		t.Fatalf("expected auth-denied RPCError, got %T %v", err, err)
	}
}

// TestPeerStartReadLoopIdempotent guards the double-start protection. Without
// the startOnce guard a second StartReadLoop launches a second reader on the
// same WebSocket — gorilla allows exactly one concurrent reader, so frames get
// stolen/corrupted and the link dies mid-roundtrip.
func TestPeerStartReadLoopIdempotent(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		p := NewPeerUnserved(conn)
		p.Handle("test.echo", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			var in map[string]int
			_ = json.Unmarshal(params, &in)
			return map[string]any{"got": in["n"]}, nil
		})
		p.StartReadLoop()
		p.StartReadLoop() // must be a no-op
		p.StartReadLoop() // idempotent even after the loop is live
		p.Wait()
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client := NewPeer(conn)
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Several roundtrips: a stray second reader would start racing here.
	for i := 0; i < 10; i++ {
		var out struct {
			Got int `json:"got"`
		}
		if err := client.Call(ctx, "test.echo", map[string]int{"n": i}, &out); err != nil {
			t.Fatalf("call %d after repeated StartReadLoop: %v", i, err)
		}
		if out.Got != i {
			t.Fatalf("call %d echoed %d", i, out.Got)
		}
	}
}

// TestPeerHandlersFirstNoMethodNotFound pins the production ordering: handlers
// registered on an UNSERVED peer are live for the very first inbound frame once
// StartReadLoop runs — no "method not found" window.
func TestPeerHandlersFirstNoMethodNotFound(t *testing.T) {
	up := websocket.Upgrader{}
	var firstErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Production order: construct unserved → register → start.
		peer := NewPeerUnserved(conn)
		peer.Handle("test.echo", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			return map[string]any{"ok": true}, nil
		})
		peer.StartReadLoop()
		peer.Wait()
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// The client dials and CALLS immediately — the frame is in flight while the
	// server is still registering (unserved peer). This is the window that used
	// to answer method-not-found.
	client := NewPeer(conn)
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	if err := client.Call(ctx, "test.echo", nil, &out); err != nil {
		firstErr = err
	}
	if firstErr != nil {
		t.Fatalf("first call after handlers-first init: %v", firstErr)
	}
	if out["ok"] != true {
		t.Fatalf("unexpected echo result: %+v", out)
	}
}
