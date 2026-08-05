package http_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
	"github.com/google/go-cmp/cmp"
)

// op builds one operation of a sync batch.
func op(opID, opType string, at time.Time, payload map[string]any) map[string]any {
	return map[string]any{
		"op_id":   opID,
		"type":    opType,
		"at":      at.Format(time.RFC3339Nano),
		"payload": payload,
	}
}

func batch(ops ...map[string]any) map[string]any {
	return map[string]any{"operations": ops}
}

type syncResult struct {
	OpID    string `json:"op_id"`
	Status  string `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// State is whichever entity the operation touched: an object for most
	// operations, an array for column.reorder.
	State any `json:"state"`
}

// stateObject reads the state of a result that carries a single entity.
func (r syncResult) stateObject(t *testing.T) map[string]any {
	t.Helper()

	obj, ok := r.State.(map[string]any)
	if !ok {
		t.Fatalf("state is %T, want an object\nresult: %+v", r.State, r)
	}
	return obj
}

func postBatch(t *testing.T, env *testutil.Env, body any) []syncResult {
	t.Helper()

	var out struct {
		Results []syncResult `json:"results"`
	}
	env.Client.POST("/api/sync", body).ExpectStatus(http.StatusOK).JSON(&out)
	return out.Results
}

func statusesOf(results []syncResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Status
	}
	return out
}

func TestSyncEmptyBatch(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	results := postBatch(t, env, batch())

	if len(results) != 0 {
		t.Errorf("%d results, want 0", len(results))
	}
	if n := len(env.Events.Events()); n != 0 {
		t.Errorf("%d events, want 0: an empty batch changes nothing", n)
	}
	if n := env.CountRows(t, "boards"); n != 0 {
		t.Errorf("%d boards, want 0", n)
	}
}

func TestSyncAppliesInOrder(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	now := env.Clock.Now()

	results := postBatch(t, env, batch(
		op("op-1", "board.create", now, map[string]any{"id": "b1", "name": "Work"}),
		op("op-2", "column.create", now, map[string]any{"id": "c1", "board_id": "b1", "name": "Todo"}),
		op("op-3", "card.create", now, map[string]any{"id": "k1", "column_id": "c1", "title": "Write the tests"}),
	))

	if diff := cmp.Diff([]string{"applied", "applied", "applied"}, statusesOf(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}

	// The order matters: the column could not exist before the board.
	var got struct {
		Columns []struct {
			Cards []struct {
				Title string `json:"title"`
			} `json:"cards"`
		} `json:"columns"`
	}
	env.Client.GET("/api/boards/b1").ExpectStatus(http.StatusOK).JSON(&got)
	if len(got.Columns) != 1 || len(got.Columns[0].Cards) != 1 {
		t.Fatalf("board = %+v, want one column holding one card", got)
	}
}

// The real scenario: the phone lost signal mid-POST and sends everything
// again. This is *the* offline test.
func TestSyncResendingTheWholeBatchAppliesNothingTwice(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	now := env.Clock.Now()
	body := batch(
		op("op-1", "board.create", now, map[string]any{"id": "b1", "name": "Work"}),
		op("op-2", "column.create", now, map[string]any{"id": "c1", "board_id": "b1", "name": "Todo"}),
		op("op-3", "card.create", now, map[string]any{"id": "k1", "column_id": "c1", "title": "one"}),
	)

	postBatch(t, env, body)
	results := postBatch(t, env, body)

	if diff := cmp.Diff([]string{"duplicate", "duplicate", "duplicate"}, statusesOf(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}
	if n := env.CountRows(t, "cards"); n != 1 {
		t.Errorf("%d cards, want 1: the resend duplicated something", n)
	}
	if n := env.CountRows(t, "boards"); n != 1 {
		t.Errorf("%d boards, want 1", n)
	}
}

func TestSyncPartiallyRepeatedBatch(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	now := env.Clock.Now()

	first := []map[string]any{
		op("op-1", "board.create", now, map[string]any{"id": "b1", "name": "Work"}),
		op("op-2", "column.create", now, map[string]any{"id": "c1", "board_id": "b1", "name": "Todo"}),
	}
	postBatch(t, env, map[string]any{"operations": first})

	second := append(append([]map[string]any{}, first...),
		op("op-3", "card.create", now, map[string]any{"id": "k1", "column_id": "c1", "title": "one"}),
		op("op-4", "card.create", now, map[string]any{"id": "k2", "column_id": "c1", "title": "two"}),
	)
	results := postBatch(t, env, map[string]any{"operations": second})

	want := []string{"duplicate", "duplicate", "applied", "applied"}
	if diff := cmp.Diff(want, statusesOf(results)); diff != "" {
		t.Errorf("statuses mismatch (-want +got):\n%s", diff)
	}
}

// A batch of twenty changes collapsing over one bad entry would leave the user
// stuck on their phone.
func TestSyncOneBadOperationDoesNotStopTheRest(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	full := env.SeedColumn(t, board.ID, "Full", ptr(1))
	env.SeedCard(t, full, "occupant")
	now := env.Clock.Now()

	results := postBatch(t, env, batch(
		op("op-1", "card.create", now, map[string]any{"column_id": board.Todo, "title": "before"}),
		op("op-2", "card.create", now, map[string]any{"column_id": full, "title": "refused"}),
		op("op-3", "card.create", now, map[string]any{"column_id": board.Todo, "title": "after"}),
	))

	if diff := cmp.Diff([]string{"applied", "failed", "applied"}, statusesOf(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}
	if results[1].Code != "wip_exceeded" {
		t.Errorf("Code = %q, want wip_exceeded — the client has to know why", results[1].Code)
	}
	if diff := cmp.Diff([]string{"before", "after"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
		t.Errorf("the surrounding operations must still land (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"occupant"}, env.CardTitlesIn(t, full)); diff != "" {
		t.Errorf("the refused operation must leave nothing behind (-want +got):\n%s", diff)
	}
}

// Where the fake clock earns its keep.
func TestSyncConflictResolution(t *testing.T) {
	t.Parallel()

	t.Run("the server wins when its copy is newer, and sends it back", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "original")

		// The client made its edit while offline, one second in.
		offlineAt := env.Clock.Now().Add(1 * time.Second)

		// The server was edited from another device five seconds in.
		env.Clock.Advance(5 * time.Second)
		env.Client.PATCH("/api/cards/"+card, map[string]any{"title": "from the other device"}).
			ExpectStatus(http.StatusOK)

		results := postBatch(t, env, batch(
			op("op-1", "card.update", offlineAt, map[string]any{"id": card, "title": "from the phone"}),
		))

		if results[0].Status != "conflict" {
			t.Fatalf("status = %q, want conflict\nresult: %+v", results[0].Status, results[0])
		}
		if results[0].stateObject(t)["title"] != "from the other device" {
			t.Errorf("state = %v, want the server's version so the client can correct itself", results[0].State)
		}

		if diff := cmp.Diff([]string{"from the other device"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("the server's version must survive (-want +got):\n%s", diff)
		}
	})

	t.Run("the client wins when its change is newer", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "original")

		results := postBatch(t, env, batch(
			op("op-1", "card.update", env.Clock.Now().Add(10*time.Second),
				map[string]any{"id": card, "title": "from the phone"}),
		))

		if results[0].Status != "applied" {
			t.Fatalf("status = %q, want applied", results[0].Status)
		}
		if diff := cmp.Diff([]string{"from the phone"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("stored mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestSyncOnADeletedCardDoesNotResurrectIt(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	card := env.SeedCard(t, board.Todo, "gone")
	env.Client.DELETE("/api/cards/" + card).ExpectStatus(http.StatusNoContent)

	results := postBatch(t, env, batch(
		op("op-1", "card.update", env.Clock.Now().Add(time.Second),
			map[string]any{"id": card, "title": "back from the dead"}),
	))

	if results[0].Status != "failed" || results[0].Code != "deleted" {
		t.Fatalf("result = %+v, want failed/deleted", results[0])
	}
	if got := env.CardTitlesIn(t, board.Todo); len(got) != 0 {
		t.Errorf("live cards = %v, want none", got)
	}
}

// A malformed op id is a bug in the client, not a business case: the whole
// batch is refused rather than answered 200.
func TestSyncMalformedBatchIsA400(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name string
		body any
	}{
		{
			name: "empty op id",
			body: batch(op("", "board.create", now, map[string]any{"id": "b1", "name": "Work"})),
		},
		{
			name: "op id with whitespace",
			body: batch(op("op 1", "board.create", now, map[string]any{"id": "b1", "name": "Work"})),
		},
		{
			name: "unknown type",
			body: batch(op("op-1", "card.teleport", now, map[string]any{"id": "k1"})),
		},
		{
			name: "payload that does not match the type",
			body: batch(op("op-1", "card.move", now, map[string]any{"id": "k1", "nonsense": true})),
		},
		{
			name: "a bad op id anywhere in the batch",
			body: batch(
				op("op-1", "board.create", now, map[string]any{"id": "b1", "name": "Work"}),
				op("", "board.create", now, map[string]any{"id": "b2", "name": "Other"}),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := testutil.NewEnv(t)
			env.Client.POST("/api/sync", tt.body).ExpectStatus(http.StatusBadRequest)

			if n := env.CountRows(t, "boards"); n != 0 {
				t.Errorf("%d boards created, want 0: a rejected batch applies nothing", n)
			}
			if n := env.CountRows(t, "op_log"); n != 0 {
				t.Errorf("%d op log entries, want 0", n)
			}
		})
	}
}

// One aggregated event, or the other devices get an event storm.
func TestSyncPublishesOneAggregatedEvent(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	env.Events.Reset()
	now := env.Clock.Now()

	ops := make([]map[string]any, 0, 5)
	for i, title := range []string{"one", "two", "three", "four", "five"} {
		ops = append(ops, op(
			string(rune('a'+i))+"-op", "card.create", now,
			map[string]any{"column_id": board.Todo, "title": title},
		))
	}
	postBatch(t, env, map[string]any{"operations": ops})

	events := env.Events.Events()
	if len(events) != 1 {
		t.Fatalf("%d events for a batch of 5, want exactly 1", len(events))
	}
	if events[0].Type != domain.EventSyncApplied {
		t.Errorf("type = %q, want %q", events[0].Type, domain.EventSyncApplied)
	}
	payload, ok := events[0].Payload.(domain.SyncAppliedPayload)
	if !ok || payload.Applied != 5 {
		t.Errorf("payload = %+v, want 5 applied", events[0].Payload)
	}
}

func TestSyncMovesAndReorders(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	ids := env.SeedCards(t, board.Todo, "a", "b", "c")
	now := env.Clock.Now()

	results := postBatch(t, env, batch(
		op("op-1", "card.move", now, map[string]any{
			"id": ids[0], "to_column_id": board.Doing, "to_index": 0,
		}),
		op("op-2", "column.reorder", now, map[string]any{
			"board_id": board.ID, "ids": []string{board.Done, board.Doing, board.Todo},
		}),
	))

	if diff := cmp.Diff([]string{"applied", "applied"}, statusesOf(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"b", "c"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
		t.Errorf("source mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a"}, env.CardTitlesIn(t, board.Doing)); diff != "" {
		t.Errorf("target mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{board.Done, board.Doing, board.Todo}, env.ColumnOrder(t, board.ID)); diff != "" {
		t.Errorf("column order mismatch (-want +got):\n%s", diff)
	}
	env.AssertPositionsCompact(t, board.Todo)
	env.AssertPositionsCompact(t, board.Doing)
}
