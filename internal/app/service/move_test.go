package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/google/go-cmp/cmp"
)

// board with two columns: "Todo" holding a, b, c and "Doing" holding x, y.
func moveRig(t *testing.T, doingLimit *int) (r *rig, todo, doing *domain.Column, cards map[string]*domain.Card) {
	t.Helper()

	r = newRig(t)
	b := r.board(t, "Work")
	todo = r.column(t, b.ID, "Todo", nil)
	doing = r.column(t, b.ID, "Doing", doingLimit)

	cards = map[string]*domain.Card{}
	for _, title := range []string{"a", "b", "c"} {
		cards[title] = r.card(t, todo.ID, title)
	}
	for _, title := range []string{"x", "y"} {
		cards[title] = r.card(t, doing.ID, title)
	}
	return r, todo, doing, cards
}

// Both directions, because the classic reordering bug is an off-by-one that
// only shows up one way (02-testing.md §4.4).
func TestMoveWithinTheSameColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name  string
		card  string
		index int
		want  []string
	}{
		{name: "first card down one", card: "a", index: 1, want: []string{"b", "a", "c"}},
		{name: "first card to the end", card: "a", index: 2, want: []string{"b", "c", "a"}},
		{name: "last card up one", card: "c", index: 1, want: []string{"a", "c", "b"}},
		{name: "last card to the front", card: "c", index: 0, want: []string{"c", "a", "b"}},
		{name: "middle card to the front", card: "b", index: 0, want: []string{"b", "a", "c"}},
		{name: "middle card to the end", card: "b", index: 2, want: []string{"a", "c", "b"}},
		{name: "past the end clamps", card: "a", index: 99, want: []string{"b", "c", "a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, todo, _, cards := moveRig(t, nil)
			if _, err := r.Cards.Move(ctx, port.MoveCardCmd{
				ID: cards[tt.card].ID, ToColumnID: todo.ID, ToIndex: tt.index,
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, r.titlesIn(t, todo.ID)); diff != "" {
				t.Errorf("order mismatch (-want +got):\n%s", diff)
			}
			r.assertPositionsCompact(t, todo.ID)
		})
	}
}

func TestMoveToTheSamePositionChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r, todo, _, cards := moveRig(t, nil)
	r.Events.Reset()

	moved, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["b"].ID, ToColumnID: todo.ID, ToIndex: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if moved.Position != 1 {
		t.Errorf("Position = %d, want 1", moved.Position)
	}
	if diff := cmp.Diff([]string{"a", "b", "c"}, r.titlesIn(t, todo.ID)); diff != "" {
		t.Errorf("order mismatch (-want +got):\n%s", diff)
	}
	if len(r.Events.Events()) != 0 {
		t.Error("a move that changes nothing must not wake the other devices")
	}
}

func TestMoveAcrossColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("the source compacts and the target opens a slot", func(t *testing.T) {
		t.Parallel()

		r, todo, doing, cards := moveRig(t, nil)

		moved, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["b"].ID, ToColumnID: doing.ID, ToIndex: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if moved.ColumnID != doing.ID || moved.Position != 1 {
			t.Errorf("card at %s:%d, want %s:1", moved.ColumnID, moved.Position, doing.ID)
		}
		if diff := cmp.Diff([]string{"a", "c"}, r.titlesIn(t, todo.ID)); diff != "" {
			t.Errorf("source mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"x", "b", "y"}, r.titlesIn(t, doing.ID)); diff != "" {
			t.Errorf("target mismatch (-want +got):\n%s", diff)
		}
		r.assertPositionsCompact(t, todo.ID)
		r.assertPositionsCompact(t, doing.ID)
	})

	t.Run("to the front and to the end", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			index int
			want  []string
		}{
			{name: "front", index: 0, want: []string{"a", "x", "y"}},
			{name: "end", index: 2, want: []string{"x", "y", "a"}},
			{name: "past the end clamps", index: 99, want: []string{"x", "y", "a"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r, _, doing, cards := moveRig(t, nil)
				if _, err := r.Cards.Move(ctx, port.MoveCardCmd{
					ID: cards["a"].ID, ToColumnID: doing.ID, ToIndex: tt.index,
				}); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if diff := cmp.Diff(tt.want, r.titlesIn(t, doing.ID)); diff != "" {
					t.Errorf("target mismatch (-want +got):\n%s", diff)
				}
				r.assertPositionsCompact(t, doing.ID)
			})
		}
	})

	t.Run("stamps updated_at on the moved card only", func(t *testing.T) {
		t.Parallel()

		r, todo, doing, cards := moveRig(t, nil)
		r.Clock.Advance(5_000_000_000)

		if _, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["b"].ID, ToColumnID: doing.ID, ToIndex: 0}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		moved, _ := r.Store.Cards().Get(ctx, cards["b"].ID)
		if !moved.UpdatedAt.Equal(r.Clock.Now()) {
			t.Errorf("moved card UpdatedAt = %v, want %v", moved.UpdatedAt, r.Clock.Now())
		}
		// The siblings only shifted position. Bumping their timestamps would
		// make a pending offline edit to one of them lose a conflict it should
		// win (see port.CardRepository.SetPositions).
		sibling, _ := r.Store.Cards().Get(ctx, cards["a"].ID)
		if !sibling.UpdatedAt.Equal(cards["a"].UpdatedAt) {
			t.Errorf("sibling UpdatedAt = %v, want it untouched at %v", sibling.UpdatedAt, cards["a"].UpdatedAt)
		}
		_ = todo
	})
}

// A 409 that also left the card half-moved would be worse than not having the
// rule at all, so this asserts both halves.
func TestMoveIntoAFullColumnLeavesTheCardWhereItWas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r, todo, doing, cards := moveRig(t, ptr(2)) // "Doing" already holds x and y
	r.Events.Reset()

	_, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["b"].ID, ToColumnID: doing.ID, ToIndex: 0})
	if !errors.Is(err, domain.ErrWIPExceeded) {
		t.Fatalf("err = %v, want %v", err, domain.ErrWIPExceeded)
	}

	if diff := cmp.Diff([]string{"a", "b", "c"}, r.titlesIn(t, todo.ID)); diff != "" {
		t.Errorf("the source column must be untouched (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"x", "y"}, r.titlesIn(t, doing.ID)); diff != "" {
		t.Errorf("the target column must be untouched (-want +got):\n%s", diff)
	}
	stored, _ := r.Store.Cards().Get(ctx, cards["b"].ID)
	if stored.ColumnID != todo.ID || stored.Position != 1 {
		t.Errorf("the card is at %s:%d, want exactly where it started (%s:1)", stored.ColumnID, stored.Position, todo.ID)
	}
	if len(r.Events.Events()) != 0 {
		t.Error("a refused move must publish nothing")
	}
}

func TestMoveRejections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("a negative index is invalid", func(t *testing.T) {
		t.Parallel()

		r, todo, _, cards := moveRig(t, nil)
		_, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["a"].ID, ToColumnID: todo.ID, ToIndex: -1})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
	})

	t.Run("a column on another board is invalid", func(t *testing.T) {
		t.Parallel()

		r, _, _, cards := moveRig(t, nil)
		other := r.board(t, "Personal")
		elsewhere := r.column(t, other.ID, "Elsewhere", nil)

		_, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["a"].ID, ToColumnID: elsewhere.ID, ToIndex: 0})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v: a card must not leave its board", err, domain.ErrInvalid)
		}
	})

	t.Run("a nonexistent card is not found", func(t *testing.T) {
		t.Parallel()

		r, todo, _, _ := moveRig(t, nil)
		_, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: "nope", ToColumnID: todo.ID, ToIndex: 0})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})

	t.Run("a nonexistent target column is not found", func(t *testing.T) {
		t.Parallel()

		r, _, _, cards := moveRig(t, nil)
		_, err := r.Cards.Move(ctx, port.MoveCardCmd{ID: cards["a"].ID, ToColumnID: "nope", ToIndex: 0})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})
}

func TestMovePublishesCardMovedWithBothColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r, todo, doing, cards := moveRig(t, nil)
	r.Events.Reset()

	if _, err := r.Cards.Move(ctx, port.MoveCardCmd{
		ID: cards["a"].ID, ToColumnID: doing.ID, ToIndex: 0, OpID: "op-7",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := r.Events.OfType(domain.EventCardMoved)
	if len(events) != 1 {
		t.Fatalf("%d card.moved events, want exactly 1", len(events))
	}
	payload, ok := events[0].Payload.(domain.CardMovedPayload)
	if !ok {
		t.Fatalf("payload is %T, want CardMovedPayload", events[0].Payload)
	}
	if payload.FromColumnID != todo.ID || payload.ToColumnID != doing.ID {
		t.Errorf("from/to = %s/%s, want %s/%s", payload.FromColumnID, payload.ToColumnID, todo.ID, doing.ID)
	}
	if events[0].OpID != "op-7" {
		t.Errorf("OpID = %q, want op-7", events[0].OpID)
	}
}
