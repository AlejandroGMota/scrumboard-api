package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/google/go-cmp/cmp"
)

func TestColumnCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("places the new column last", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		first := r.column(t, b.ID, "Todo", nil)
		second := r.column(t, b.ID, "Doing", nil)
		third := r.column(t, b.ID, "Done", nil)

		if first.Position != 0 || second.Position != 1 || third.Position != 2 {
			t.Errorf("positions = %d, %d, %d; want 0, 1, 2", first.Position, second.Position, third.Position)
		}
	})

	t.Run("a nonexistent board is not found", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		_, _, err := r.Columns.Create(ctx, port.CreateColumnCmd{BoardID: "nope", Name: "Todo"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})

	t.Run("the same id with a different wip limit is a conflict", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		_, _, err := r.Columns.Create(ctx, port.CreateColumnCmd{ID: "c1", BoardID: b.ID, Name: "Todo", WIPLimit: ptr(3)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, _, err = r.Columns.Create(ctx, port.CreateColumnCmd{ID: "c1", BoardID: b.ID, Name: "Todo", WIPLimit: ptr(5)})
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want %v", err, domain.ErrConflict)
		}
	})
}

func TestColumnUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Decided in 00-scope.md §8. The column sits over limit until cards move
	// out; the next insertion is what gets refused.
	t.Run("the wip limit may drop below the current card count", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Doing", nil)
		r.card(t, col.ID, "one")
		r.card(t, col.ID, "two")
		r.card(t, col.ID, "three")

		updated, err := r.Columns.Update(ctx, port.UpdateColumnCmd{
			ID:    col.ID,
			Patch: domain.ColumnPatch{WIPLimit: ptr(ptr(1)), SetWIPLimit: true},
		})
		if err != nil {
			t.Fatalf("lowering the limit must be allowed, got %v", err)
		}
		if *updated.WIPLimit != 1 {
			t.Fatalf("WIPLimit = %d, want 1", *updated.WIPLimit)
		}
		// The three cards stay put …
		if got := len(r.titlesIn(t, col.ID)); got != 3 {
			t.Errorf("%d cards in the column, want the 3 that were already there", got)
		}
		// … and the next one is refused.
		_, _, err = r.Cards.Create(ctx, port.CreateCardCmd{ColumnID: col.ID, Title: "four"})
		if !errors.Is(err, domain.ErrWIPExceeded) {
			t.Errorf("err = %v, want %v", err, domain.ErrWIPExceeded)
		}
	})
}

func TestColumnDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("a column with live cards is refused", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		r.card(t, col.ID, "still here")

		err := r.Columns.Delete(ctx, port.DeleteColumnCmd{ID: col.ID})
		if !errors.Is(err, domain.ErrColumnNotEmpty) {
			t.Fatalf("err = %v, want %v", err, domain.ErrColumnNotEmpty)
		}
		if _, err := r.Store.Columns().Get(ctx, col.ID); err != nil {
			t.Errorf("the refused delete must leave the column alone, got %v", err)
		}
	})

	t.Run("a column whose cards are all deleted can go", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "gone soon")

		if err := r.Cards.Delete(ctx, port.DeleteCardCmd{ID: card.ID}); err != nil {
			t.Fatalf("deleting the card: %v", err)
		}
		if err := r.Columns.Delete(ctx, port.DeleteColumnCmd{ID: col.ID}); err != nil {
			t.Fatalf("the column should now be deletable, got %v", err)
		}
	})

	t.Run("the surviving columns compact", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		todo := r.column(t, b.ID, "Todo", nil)
		doing := r.column(t, b.ID, "Doing", nil)
		done := r.column(t, b.ID, "Done", nil)

		if err := r.Columns.Delete(ctx, port.DeleteColumnCmd{ID: doing.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cols, _ := r.Store.Columns().ListByBoard(ctx, b.ID)
		if len(cols) != 2 {
			t.Fatalf("%d live columns, want 2", len(cols))
		}
		if cols[0].ID != todo.ID || cols[0].Position != 0 {
			t.Errorf("first column = %s at %d, want %s at 0", cols[0].ID, cols[0].Position, todo.ID)
		}
		if cols[1].ID != done.ID || cols[1].Position != 1 {
			t.Errorf("second column = %s at %d, want %s at 1 (a gap left behind is a tie waiting to happen)", cols[1].ID, cols[1].Position, done.ID)
		}
	})
}

// The reorder endpoint is the easiest one to leave half-finished and the one
// that corrupts ordering, so its validation gets a table of its own.
func TestColumnReorder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	setup := func(t *testing.T) (*rig, *domain.Board, []string) {
		t.Helper()

		r := newRig(t)
		b := r.board(t, "Work")
		ids := []string{
			r.column(t, b.ID, "Todo", nil).ID,
			r.column(t, b.ID, "Doing", nil).ID,
			r.column(t, b.ID, "Done", nil).ID,
		}
		return r, b, ids
	}

	t.Run("reverses the order and rewrites positions", func(t *testing.T) {
		t.Parallel()

		r, b, ids := setup(t)
		reversed := []string{ids[2], ids[1], ids[0]}

		ordered, err := r.Columns.Reorder(ctx, port.ReorderColumnsCmd{BoardID: b.ID, IDs: reversed})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := make([]string, len(ordered))
		for i, c := range ordered {
			got[i] = c.ID
		}
		if diff := cmp.Diff(reversed, got); diff != "" {
			t.Errorf("returned order mismatch (-want +got):\n%s", diff)
		}

		// Asserted by reading the store, not the response.
		stored, _ := r.Store.Columns().ListByBoard(ctx, b.ID)
		for i, c := range stored {
			if c.ID != reversed[i] || c.Position != i {
				t.Errorf("stored[%d] = %s at %d, want %s at %d", i, c.ID, c.Position, reversed[i], i)
			}
		}
	})

	t.Run("rejects a malformed list", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			ids  func(ids []string, other string) []string
		}{
			{name: "incomplete", ids: func(ids []string, _ string) []string { return ids[:2] }},
			{name: "with a duplicate", ids: func(ids []string, _ string) []string { return []string{ids[0], ids[1], ids[1]} }},
			{name: "with an unknown id", ids: func(ids []string, _ string) []string { return []string{ids[0], ids[1], "nope"} }},
			{name: "with an id from another board", ids: func(ids []string, other string) []string { return []string{ids[0], ids[1], other} }},
			{name: "empty", ids: func(ids []string, _ string) []string { return nil }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r, b, ids := setup(t)
				otherBoard := r.board(t, "Personal")
				foreign := r.column(t, otherBoard.ID, "Elsewhere", nil)

				_, err := r.Columns.Reorder(ctx, port.ReorderColumnsCmd{BoardID: b.ID, IDs: tt.ids(ids, foreign.ID)})
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
				}

				// A refused reorder changes nothing at all.
				stored, _ := r.Store.Columns().ListByBoard(ctx, b.ID)
				for i, c := range stored {
					if c.ID != ids[i] || c.Position != i {
						t.Fatalf("a refused reorder must leave the order untouched: stored[%d] = %s at %d", i, c.ID, c.Position)
					}
				}
			})
		}
	})

	t.Run("publishes one columns.ordered event with the new order", func(t *testing.T) {
		t.Parallel()

		r, b, ids := setup(t)
		r.Events.Reset()
		reversed := []string{ids[2], ids[1], ids[0]}

		if _, err := r.Columns.Reorder(ctx, port.ReorderColumnsCmd{BoardID: b.ID, IDs: reversed}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		events := r.Events.OfType(domain.EventColumnsOrdered)
		if len(events) != 1 {
			t.Fatalf("%d columns.ordered events, want 1", len(events))
		}
		payload, ok := events[0].Payload.(domain.ColumnsOrderedPayload)
		if !ok {
			t.Fatalf("payload is %T, want ColumnsOrderedPayload", events[0].Payload)
		}
		if diff := cmp.Diff(reversed, payload.IDs); diff != "" {
			t.Errorf("event ids mismatch (-want +got):\n%s", diff)
		}
	})
}
