package ws

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testToken = "test-token-0123456789"

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newServer boots the handshake handler over a real HTTP server, because the
// upgrade is the part being tested and httptest.NewServer is what makes it a
// real one.
func newServer(t *testing.T) (*Hub, string) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	hub := NewHub(quietLogger())
	go hub.Run(ctx)

	ts := httptest.NewServer(NewHandler(hub, testToken, quietLogger()))
	t.Cleanup(func() {
		ts.Close()
		cancel()
	})

	return hub, "ws" + strings.TrimPrefix(ts.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url+"?token="+testToken, nil)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// waitFor polls until cond holds or the deadline passes. Connecting is
// asynchronous on the server side, so "the hub has one client" becomes true
// shortly after Dial returns rather than at the same instant.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readMessage(t *testing.T, conn *websocket.Conn) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	return string(data)
}

func TestHandshake(t *testing.T) {
	t.Parallel()

	t.Run("a valid token establishes the connection", func(t *testing.T) {
		t.Parallel()

		hub, url := newServer(t)
		dial(t, url)
		waitFor(t, "the client to register", func() bool { return hub.Count() == 1 })
	})

	// Rejected before the upgrade, so the client gets a status code it can
	// read rather than an immediate close frame it cannot.
	t.Run("no token is rejected", func(t *testing.T) {
		t.Parallel()

		_, url := newServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		conn, res, err := websocket.Dial(ctx, url, nil)
		if err == nil {
			conn.Close(websocket.StatusNormalClosure, "")
			t.Fatal("the connection was accepted without a token")
		}
		if res == nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %v, want 401", res)
		}
	})

	t.Run("an invalid token is rejected", func(t *testing.T) {
		t.Parallel()

		hub, url := newServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		conn, res, err := websocket.Dial(ctx, url+"?token=wrong", nil)
		if err == nil {
			conn.Close(websocket.StatusNormalClosure, "")
			t.Fatal("the connection was accepted with the wrong token")
		}
		if res == nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %v, want 401", res)
		}
		if hub.Count() != 0 {
			t.Errorf("%d clients registered, want 0", hub.Count())
		}
	})

	t.Run("the token may also arrive as a header", func(t *testing.T) {
		t.Parallel()

		// Browsers cannot set this, but curl and the tests can, and the
		// handshake accepting both keeps the API consistent.
		hub, url := newServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
		})
		if err != nil {
			t.Fatalf("dialling with a header: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		waitFor(t, "the client to register", func() bool { return hub.Count() == 1 })
	})
}

func TestFanOutReachesEveryClient(t *testing.T) {
	t.Parallel()

	hub, url := newServer(t)
	first := dial(t, url)
	second := dial(t, url)
	waitFor(t, "both clients to register", func() bool { return hub.Count() == 2 })

	hub.Broadcast([]byte(`{"type":"card.created"}`))

	if got := readMessage(t, first); got != `{"type":"card.created"}` {
		t.Errorf("first client got %q", got)
	}
	if got := readMessage(t, second); got != `{"type":"card.created"}` {
		t.Errorf("second client got %q", got)
	}
}

// Decided in 00-scope.md §8: the hub stays simpler, the client filters its own
// events by op_id, and a client whose HTTP response was lost still converges.
func TestTheOriginatorReceivesItsOwnEvent(t *testing.T) {
	t.Parallel()

	hub, url := newServer(t)
	conn := dial(t, url)
	waitFor(t, "the client to register", func() bool { return hub.Count() == 1 })

	hub.Broadcast([]byte(`{"type":"card.created","op_id":"op-1"}`))

	got := readMessage(t, conn)
	if !strings.Contains(got, `"op_id":"op-1"`) {
		t.Errorf("message = %q, want the op id echoed back so the originator can filter it", got)
	}
}

func TestDisconnectRemovesTheClientAndDoesNotBlockTheOther(t *testing.T) {
	t.Parallel()

	hub, url := newServer(t)
	staying := dial(t, url)
	leaving := dial(t, url)
	waitFor(t, "both clients to register", func() bool { return hub.Count() == 2 })

	if err := leaving.Close(websocket.StatusNormalClosure, "goodbye"); err != nil {
		t.Fatalf("closing: %v", err)
	}
	waitFor(t, "the hub to notice the disconnect", func() bool { return hub.Count() == 1 })

	// The survivor still works, which is the half that actually matters.
	hub.Broadcast([]byte(`{"type":"still here"}`))
	if got := readMessage(t, staying); got != `{"type":"still here"}` {
		t.Errorf("the surviving client got %q", got)
	}
}

// idleConn returns a real WebSocket connection with no pumps attached to it,
// so the send buffer of a Client built around it stays exactly as full as the
// test puts it.
//
// The alternative — filling a live connection's buffers through the handler —
// means writing megabytes and hoping, and a test that depends on kernel buffer
// sizes is a test that fails on somebody else's machine.
func idleConn(t *testing.T) *websocket.Conn {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow() //nolint:errcheck // the test is tearing down
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	return conn
}

// A client that stops reading must not block fan-out. The policy is: buffer a
// little, then drop it.
func TestASlowClientIsDroppedRatherThanBlockingTheFanOut(t *testing.T) {
	t.Parallel()

	hub := NewHub(quietLogger())

	slow := newClient(idleConn(t), quietLogger())
	healthy := newClient(idleConn(t), quietLogger())
	hub.clients[slow] = struct{}{}
	hub.clients[healthy] = struct{}{}

	// The slow client's buffer is already full: it has not read a thing.
	for i := 0; i < sendBuffer; i++ {
		slow.send <- []byte("backlog")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.Broadcast([]byte(`{"type":"card.created"}`))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a client that stopped reading")
	}

	if hub.Count() != 1 {
		t.Errorf("%d clients left, want 1: the slow one should have been dropped", hub.Count())
	}
	select {
	case got := <-healthy.send:
		if string(got) != `{"type":"card.created"}` {
			t.Errorf("the healthy client got %q", got)
		}
	default:
		t.Error("the healthy client received nothing")
	}
}

// A hub with an unguarded map of clients works perfectly until the day it does
// not. Run with -race; this is the test that would notice.
func TestConcurrentConnectDisconnectAndBroadcast(t *testing.T) {
	t.Parallel()

	hub, url := newServer(t)

	var wg sync.WaitGroup

	// Ten clients, each connecting, reading a little and leaving.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			conn, _, err := websocket.Dial(ctx, url+"?token="+testToken, nil)
			if err != nil {
				return // the server may already be shutting down
			}
			defer conn.Close(websocket.StatusNormalClosure, "")

			readCtx, readCancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer readCancel()
			//nolint:errcheck // a timeout here is an expected outcome
			conn.Read(readCtx)
		}(i)
	}

	// Events publishing throughout, as they would be during a sync batch.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				hub.Broadcast([]byte(fmt.Sprintf(`{"type":"card.created","n":%d}`, i*20+j)))
			}
		}(i)
	}

	wg.Wait()
}

func TestRunClosesEveryClientOnShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	hub := NewHub(quietLogger())
	go hub.Run(ctx)

	ts := httptest.NewServer(NewHandler(hub, testToken, quietLogger()))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()

	conn, _, err := websocket.Dial(dialCtx, url+"?token="+testToken, nil)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	waitFor(t, "the client to register", func() bool { return hub.Count() == 1 })

	cancel()
	waitFor(t, "the hub to empty", func() bool { return hub.Count() == 0 })

	// The browser learns the server went away, rather than holding a
	// connection to a process that has stopped answering.
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	if _, _, err := conn.Read(readCtx); err == nil {
		t.Error("the client's connection survived the shutdown")
	}
}
