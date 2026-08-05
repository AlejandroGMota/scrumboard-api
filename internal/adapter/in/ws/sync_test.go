package ws_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
	"github.com/coder/websocket"
)

// The point of the WebSocket, tested end to end through the real stack: a
// change made over HTTP on one device reaches another device's socket.
//
// Everything else in the ws package is unit-level. This is the one that would
// notice if the publisher, the encoder or the route wiring came apart.

func connect(t *testing.T, env *testutil.Env) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, env.WSURL, nil)
	if err != nil {
		t.Fatalf("dialling the board's socket: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })

	// The handler registers the client after the upgrade returns, so a
	// broadcast sent immediately could miss it.
	deadline := time.Now().Add(3 * time.Second)
	for env.Hub.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return conn
}

func readEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading an event: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("decoding an event: %v\nraw: %s", err, data)
	}
	return event
}

func TestAnHTTPChangeReachesAConnectedDevice(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	conn := connect(t, env)

	env.Client.WithOpID("op-42").
		POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "from the laptop", "label": "blue"}).
		ExpectStatus(http.StatusCreated)

	event := readEvent(t, conn)

	if event["type"] != "card.created" {
		t.Errorf("type = %v, want card.created", event["type"])
	}
	if event["board_id"] != board.ID {
		t.Errorf("board_id = %v, want %v", event["board_id"], board.ID)
	}
	// The op id is what lets the originating device recognise its own echo.
	if event["op_id"] != "op-42" {
		t.Errorf("op_id = %v, want op-42", event["op_id"])
	}

	payload, ok := event["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload is %T, want an object", event["payload"])
	}
	if payload["title"] != "from the laptop" {
		t.Errorf("payload title = %v, want the card that was created", payload["title"])
	}
	// The event carries the same card shape the REST API returns, because both
	// come out of the same encoder.
	if payload["label"] != "blue" || payload["column_id"] != board.Todo {
		t.Errorf("payload = %v, want the full card", payload)
	}
}

func TestAMoveIsBroadcastWithBothColumns(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	card := env.SeedCard(t, board.Todo, "a")
	conn := connect(t, env)

	env.Client.PUT("/api/cards/"+card+"/move", map[string]any{
		"to_column_id": board.Doing, "to_index": 0,
	}).ExpectStatus(http.StatusOK)

	event := readEvent(t, conn)
	if event["type"] != "card.moved" {
		t.Fatalf("type = %v, want card.moved", event["type"])
	}

	payload := event["payload"].(map[string]any)
	// Without the source column the other device would have to guess which
	// column to remove the card from, and guessing is how a card ends up
	// drawn twice.
	if payload["from_column_id"] != board.Todo || payload["to_column_id"] != board.Doing {
		t.Errorf("payload = %v, want both columns named", payload)
	}
}

// One aggregated event for a whole batch, not one per operation, or a phone
// coming back online gives every other device an event storm.
func TestASyncBatchBroadcastsOneEvent(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	conn := connect(t, env)

	now := env.Clock.Now().Format(time.RFC3339Nano)
	ops := make([]map[string]any, 0, 4)
	for i, title := range []string{"one", "two", "three", "four"} {
		ops = append(ops, map[string]any{
			"op_id": string(rune('a'+i)) + "-op",
			"type":  "card.create",
			"at":    now,
			"payload": map[string]any{
				"column_id": board.Todo,
				"title":     title,
			},
		})
	}
	env.Client.POST("/api/sync", map[string]any{"operations": ops}).ExpectStatus(http.StatusOK)

	event := readEvent(t, conn)
	if event["type"] != "sync.applied" {
		t.Fatalf("type = %v, want sync.applied", event["type"])
	}
	payload := event["payload"].(map[string]any)
	if payload["applied"] != float64(4) {
		t.Errorf("applied = %v, want 4", payload["applied"])
	}

	// And nothing else arrives.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, extra, err := conn.Read(ctx); err == nil {
		t.Errorf("a second event arrived: %s", extra)
	}
}

func TestTwoDevicesBothReceiveTheChange(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)

	laptop := connect(t, env)
	phone := connect(t, env)
	if env.Hub.Count() != 2 {
		t.Fatalf("%d clients connected, want 2", env.Hub.Count())
	}

	env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "sync me"}).
		ExpectStatus(http.StatusCreated)

	for name, conn := range map[string]*websocket.Conn{"laptop": laptop, "phone": phone} {
		event := readEvent(t, conn)
		if event["type"] != "card.created" {
			t.Errorf("%s got %v, want card.created", name, event["type"])
		}
	}
}
