package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/google/go-cmp/cmp"
)

func statuses(results []domain.OpResult) []domain.OpStatus {
	out := make([]domain.OpStatus, len(results))
	for i, r := range results {
		out[i] = r.Status
	}
	return out
}

func TestSyncEmptyBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	results, err := r.Sync.Apply(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("%d results, want 0", len(results))
	}
	if len(r.Events.Events()) != 0 {
		t.Error("an empty batch must publish nothing")
	}
}

func TestSyncAppliesInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	now := r.Clock.Now()

	ops := []domain.Operation{
		{OpID: "op-1", Type: domain.OpBoardCreate, At: now, Payload: domain.BoardCreatePayload{ID: "b1", Name: "Work"}},
		{OpID: "op-2", Type: domain.OpColumnCreate, At: now, Payload: domain.ColumnCreatePayload{ID: "c1", BoardID: "b1", Name: "Todo"}},
		{OpID: "op-3", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k1", ColumnID: "c1", Title: "Write the tests"}},
	}

	results, err := r.Sync.Apply(ctx, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.OpStatus{domain.OpApplied, domain.OpApplied, domain.OpApplied}
	if diff := cmp.Diff(want, statuses(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}

	// The order matters: the column could not have been created before the
	// board, nor the card before the column.
	board, err := r.Boards.Get(ctx, "b1")
	if err != nil {
		t.Fatalf("the board should exist: %v", err)
	}
	if len(board.Columns) != 1 || len(board.Columns[0].Cards) != 1 {
		t.Fatalf("board = %+v, want one column holding one card", board)
	}
}

// The real scenario: the phone lost signal mid-POST and sends everything
// again. This is *the* offline test.
func TestSyncResendingTheWholeBatchAppliesNothingTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	now := r.Clock.Now()
	ops := []domain.Operation{
		{OpID: "op-1", Type: domain.OpBoardCreate, At: now, Payload: domain.BoardCreatePayload{ID: "b1", Name: "Work"}},
		{OpID: "op-2", Type: domain.OpColumnCreate, At: now, Payload: domain.ColumnCreatePayload{ID: "c1", BoardID: "b1", Name: "Todo"}},
		{OpID: "op-3", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k1", ColumnID: "c1", Title: "Write the tests"}},
	}

	if _, err := r.Sync.Apply(ctx, ops); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	results, err := r.Sync.Apply(ctx, ops)
	if err != nil {
		t.Fatalf("resent batch: %v", err)
	}

	want := []domain.OpStatus{domain.OpDuplicate, domain.OpDuplicate, domain.OpDuplicate}
	if diff := cmp.Diff(want, statuses(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}
	board, _ := r.Boards.Get(ctx, "b1")
	if len(board.Columns) != 1 || len(board.Columns[0].Cards) != 1 {
		t.Errorf("board = %+v, want the same single column and card as before", board)
	}
}

func TestSyncPartiallyRepeatedBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	now := r.Clock.Now()
	first := []domain.Operation{
		{OpID: "op-1", Type: domain.OpBoardCreate, At: now, Payload: domain.BoardCreatePayload{ID: "b1", Name: "Work"}},
		{OpID: "op-2", Type: domain.OpColumnCreate, At: now, Payload: domain.ColumnCreatePayload{ID: "c1", BoardID: "b1", Name: "Todo"}},
	}
	if _, err := r.Sync.Apply(ctx, first); err != nil {
		t.Fatalf("first batch: %v", err)
	}

	second := append(first,
		domain.Operation{OpID: "op-3", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k1", ColumnID: "c1", Title: "one"}},
		domain.Operation{OpID: "op-4", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k2", ColumnID: "c1", Title: "two"}},
	)
	results, err := r.Sync.Apply(ctx, second)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}

	want := []domain.OpStatus{domain.OpDuplicate, domain.OpDuplicate, domain.OpApplied, domain.OpApplied}
	if diff := cmp.Diff(want, statuses(results)); diff != "" {
		t.Errorf("statuses mismatch (-want +got):\n%s", diff)
	}
}

// A batch of twenty changes collapsing because of one bad entry would leave
// the user stuck on their phone.
func TestSyncOneBadOperationDoesNotStopTheRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	b := r.board(t, "Work")
	full := r.column(t, b.ID, "Doing", ptr(1))
	roomy := r.column(t, b.ID, "Todo", nil)
	r.card(t, full.ID, "occupant")
	now := r.Clock.Now()

	results, err := r.Sync.Apply(ctx, []domain.Operation{
		{OpID: "op-1", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k1", ColumnID: roomy.ID, Title: "before"}},
		{OpID: "op-2", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k2", ColumnID: full.ID, Title: "refused"}},
		{OpID: "op-3", Type: domain.OpCardCreate, At: now, Payload: domain.CardCreatePayload{ID: "k3", ColumnID: roomy.ID, Title: "after"}},
	})
	if err != nil {
		t.Fatalf("the batch itself must not fail: %v", err)
	}

	want := []domain.OpStatus{domain.OpApplied, domain.OpFailed, domain.OpApplied}
	if diff := cmp.Diff(want, statuses(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}
	if results[1].Code != "wip_exceeded" {
		t.Errorf("Code = %q, want wip_exceeded — the client has to know why", results[1].Code)
	}
	if diff := cmp.Diff([]string{"before", "after"}, r.titlesIn(t, roomy.ID)); diff != "" {
		t.Errorf("the surrounding operations must still land (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"occupant"}, r.titlesIn(t, full.ID)); diff != "" {
		t.Errorf("the refused operation must leave nothing behind (-want +got):\n%s", diff)
	}
}

// The regression that only appears on a device that was actually offline: a
// record created and then edited without a connection, synced later.
//
// If an applied operation were stamped with server-now rather than with the
// operation's own timestamp, the create would land with updated_at set to the
// moment of the sync — and the edit that follows it, made earlier by the
// clock, would be refused as stale. The client would lose against itself.
func TestSyncAppliesAnOfflineSessionInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	b := r.board(t, "Work")
	col := r.column(t, b.ID, "Todo", nil)

	// The user worked offline this morning …
	createdAt := r.Clock.Now().Add(1 * time.Minute)
	editedAt := r.Clock.Now().Add(5 * time.Minute)
	movedAt := r.Clock.Now().Add(6 * time.Minute)

	// … and the queue only drains hours later.
	r.Clock.Advance(6 * time.Hour)

	results, err := r.Sync.Apply(ctx, []domain.Operation{
		{OpID: "op-1", Type: domain.OpCardCreate, At: createdAt,
			Payload: domain.CardCreatePayload{ID: "k1", ColumnID: col.ID, Title: "written on the train"}},
		{OpID: "op-2", Type: domain.OpCardUpdate, At: editedAt,
			Payload: domain.CardUpdatePayload{ID: "k1", Patch: domain.CardPatch{Title: ptr("edited on the train")}}},
		{OpID: "op-3", Type: domain.OpCardMove, At: movedAt,
			Payload: domain.CardMovePayload{ID: "k1", ToColumnID: col.ID, ToIndex: 0}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []domain.OpStatus{domain.OpApplied, domain.OpApplied, domain.OpApplied}
	if diff := cmp.Diff(want, statuses(results)); diff != "" {
		t.Fatalf("statuses mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"edited on the train"}, r.titlesIn(t, col.ID)); diff != "" {
		t.Errorf("stored mismatch (-want +got):\n%s", diff)
	}

	// The card carries when the user edited it, not when the queue drained.
	stored, _ := r.Store.Cards().Get(ctx, "k1")
	if !stored.UpdatedAt.Equal(editedAt) {
		t.Errorf("UpdatedAt = %v, want the moment of the edit (%v)", stored.UpdatedAt, editedAt)
	}
}

// Where the fake clock earns its keep.
func TestSyncConflictResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("the server wins when its copy is newer", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "original")

		// The client made its edit while offline, at T+1s …
		offlineAt := r.Clock.Now().Add(1 * time.Second)
		// … but the server was edited from another device at T+5s.
		r.Clock.Advance(5 * time.Second)
		if _, err := r.Cards.Update(ctx, port.UpdateCardCmd{ID: card.ID, Patch: domain.CardPatch{Title: ptr("from the other device")}}); err != nil {
			t.Fatalf("server-side edit: %v", err)
		}

		results, err := r.Sync.Apply(ctx, []domain.Operation{{
			OpID: "op-1", Type: domain.OpCardUpdate, At: offlineAt,
			Payload: domain.CardUpdatePayload{ID: card.ID, Patch: domain.CardPatch{Title: ptr("from the phone")}},
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results[0].Status != domain.OpConflict {
			t.Fatalf("Status = %q, want %q", results[0].Status, domain.OpConflict)
		}

		stored, _ := r.Store.Cards().Get(ctx, card.ID)
		if stored.Title != "from the other device" {
			t.Errorf("Title = %q, want the server's version to survive", stored.Title)
		}
		// The response carries the current state so the client can correct
		// itself without a second round trip.
		current, ok := results[0].State.(*domain.Card)
		if !ok || current.Title != "from the other device" {
			t.Errorf("State = %+v, want the server's card", results[0].State)
		}
	})

	t.Run("the client wins when its change is newer", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "original")

		results, err := r.Sync.Apply(ctx, []domain.Operation{{
			OpID: "op-1", Type: domain.OpCardUpdate, At: r.Clock.Now().Add(10 * time.Second),
			Payload: domain.CardUpdatePayload{ID: card.ID, Patch: domain.CardPatch{Title: ptr("from the phone")}},
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results[0].Status != domain.OpApplied {
			t.Fatalf("Status = %q, want %q", results[0].Status, domain.OpApplied)
		}
		stored, _ := r.Store.Cards().Get(ctx, card.ID)
		if stored.Title != "from the phone" {
			t.Errorf("Title = %q, want the client's version", stored.Title)
		}
	})
}

func TestSyncOnADeletedCardDoesNotResurrectIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	b := r.board(t, "Work")
	col := r.column(t, b.ID, "Todo", nil)
	card := r.card(t, col.ID, "gone")
	if err := r.Cards.Delete(ctx, port.DeleteCardCmd{ID: card.ID}); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	results, err := r.Sync.Apply(ctx, []domain.Operation{{
		OpID: "op-1", Type: domain.OpCardUpdate, At: r.Clock.Now().Add(time.Second),
		Payload: domain.CardUpdatePayload{ID: card.ID, Patch: domain.CardPatch{Title: ptr("back from the dead")}},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].Status != domain.OpFailed || results[0].Code != "deleted" {
		t.Fatalf("result = %+v, want failed/deleted", results[0])
	}
	stored, _ := r.Store.Cards().GetAny(ctx, card.ID)
	if !stored.Deleted() || stored.Title != "gone" {
		t.Errorf("card = %+v, want it still deleted and unchanged", stored)
	}
}

func TestSyncMalformedBatchFailsWhole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name string
		ops  []domain.Operation
	}{
		{
			name: "empty op id",
			ops: []domain.Operation{
				{OpID: "", Type: domain.OpBoardCreate, Payload: domain.BoardCreatePayload{ID: "b1", Name: "Work"}},
			},
		},
		{
			name: "op id with whitespace",
			ops: []domain.Operation{
				{OpID: "op 1", Type: domain.OpBoardCreate, Payload: domain.BoardCreatePayload{ID: "b1", Name: "Work"}},
			},
		},
		{
			name: "unknown type",
			ops: []domain.Operation{
				{OpID: "op-1", Type: "card.teleport", Payload: domain.CardCreatePayload{}},
			},
		},
		{
			name: "a bad op id anywhere in the batch",
			ops: []domain.Operation{
				{OpID: "op-1", Type: domain.OpBoardCreate, Payload: domain.BoardCreatePayload{ID: "b1", Name: "Work"}},
				{OpID: "", Type: domain.OpBoardCreate, Payload: domain.BoardCreatePayload{ID: "b2", Name: "Other"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			_, err := r.Sync.Apply(ctx, tt.ops)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want %v: a malformed batch is a client bug, not a business case", err, domain.ErrInvalid)
			}
			boards, _ := r.Boards.List(ctx)
			if len(boards) != 0 {
				t.Errorf("%d boards created, want 0: a rejected batch applies nothing", len(boards))
			}
		})
	}
}

// One aggregated event, or the other devices get an event storm.
func TestSyncPublishesOneAggregatedEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	b := r.board(t, "Work")
	col := r.column(t, b.ID, "Todo", nil)
	r.Events.Reset()
	now := r.Clock.Now()

	ops := make([]domain.Operation, 0, 5)
	for i, title := range []string{"one", "two", "three", "four", "five"} {
		ops = append(ops, domain.Operation{
			OpID: string(rune('a'+i)) + "-op", Type: domain.OpCardCreate, At: now,
			Payload: domain.CardCreatePayload{ColumnID: col.ID, Title: title},
		})
	}
	if _, err := r.Sync.Apply(ctx, ops); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := r.Events.Events()
	if len(events) != 1 {
		t.Fatalf("%d events for a batch of 5, want exactly 1", len(events))
	}
	if events[0].Type != domain.EventSyncApplied {
		t.Errorf("Type = %q, want %q", events[0].Type, domain.EventSyncApplied)
	}
	if events[0].BoardID != b.ID {
		t.Errorf("BoardID = %q, want %q", events[0].BoardID, b.ID)
	}
	payload, ok := events[0].Payload.(domain.SyncAppliedPayload)
	if !ok || payload.Applied != 5 {
		t.Errorf("payload = %+v, want 5 applied", events[0].Payload)
	}
}
