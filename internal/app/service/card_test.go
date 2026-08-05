package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/google/go-cmp/cmp"
)

// If any test in this file deserves to have been written first, it is this one:
// the WIP limit is the project's only real business rule.
func TestCardCreateRespectsTheWIPLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("a full column refuses the next card", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Doing", ptr(2))
		r.card(t, col.ID, "one")
		r.card(t, col.ID, "two")

		_, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ColumnID: col.ID, Title: "three"})
		if !errors.Is(err, domain.ErrWIPExceeded) {
			t.Fatalf("err = %v, want %v", err, domain.ErrWIPExceeded)
		}
		if diff := cmp.Diff([]string{"one", "two"}, r.titlesIn(t, col.ID)); diff != "" {
			t.Errorf("the refused card must not be stored (-want +got):\n%s", diff)
		}
	})

	t.Run("a column with no limit never refuses", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Inbox", nil)
		for i := 0; i < 25; i++ {
			if _, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ColumnID: col.ID, Title: "card"}); err != nil {
				t.Fatalf("card %d: %v", i, err)
			}
		}
	})

	t.Run("deleting a card frees a slot", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Doing", ptr(1))
		first := r.card(t, col.ID, "one")

		if _, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ColumnID: col.ID, Title: "two"}); !errors.Is(err, domain.ErrWIPExceeded) {
			t.Fatalf("err = %v, want %v", err, domain.ErrWIPExceeded)
		}
		if err := r.Cards.Delete(ctx, port.DeleteCardCmd{ID: first.ID}); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		if _, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ColumnID: col.ID, Title: "two"}); err != nil {
			t.Fatalf("a soft-deleted card must not keep occupying its slot, got %v", err)
		}
	})
}

func TestCardCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("appends to the end of the column", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		r.card(t, col.ID, "first")
		r.card(t, col.ID, "second")

		if diff := cmp.Diff([]string{"first", "second"}, r.titlesIn(t, col.ID)); diff != "" {
			t.Errorf("order mismatch (-want +got):\n%s", diff)
		}
		r.assertPositionsCompact(t, col.ID)
	})

	t.Run("a nonexistent column is not found", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		_, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ColumnID: "nope", Title: "x"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})

	t.Run("the same id with the same content does not duplicate", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)

		cmd := port.CreateCardCmd{ID: "k1", ColumnID: col.ID, Title: "Write the tests", Label: "blue"}
		if _, _, err := r.Cards.Create(ctx, cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, created, err := r.Cards.Create(ctx, cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if created {
			t.Error("created = true on a repeat")
		}
		if got := len(r.titlesIn(t, col.ID)); got != 1 {
			t.Errorf("%d cards, want 1", got)
		}
	})

	t.Run("the same id with a different title is a conflict", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)

		if _, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ID: "k1", ColumnID: col.ID, Title: "One"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, _, err := r.Cards.Create(ctx, port.CreateCardCmd{ID: "k1", ColumnID: col.ID, Title: "Two"})
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want %v", err, domain.ErrConflict)
		}
	})
}

func TestCardDeleteCompactsTheColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	b := r.board(t, "Work")
	col := r.column(t, b.ID, "Todo", nil)
	r.card(t, col.ID, "first")
	middle := r.card(t, col.ID, "second")
	r.card(t, col.ID, "third")

	if err := r.Cards.Delete(ctx, port.DeleteCardCmd{ID: middle.ID}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff([]string{"first", "third"}, r.titlesIn(t, col.ID)); diff != "" {
		t.Errorf("order mismatch (-want +got):\n%s", diff)
	}
	r.assertPositionsCompact(t, col.ID)
}

func TestCardUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("patches the fields it carries and stamps updated_at", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "Old title")
		r.Clock.Advance(5_000_000_000)

		updated, err := r.Cards.Update(ctx, port.UpdateCardCmd{
			ID:    card.ID,
			Patch: domain.CardPatch{Title: ptr("New title"), Label: ptr("green")},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Title != "New title" || updated.Label != "green" {
			t.Errorf("card = %+v, want the patched fields", updated)
		}
		if !updated.UpdatedAt.Equal(r.Clock.Now()) {
			t.Errorf("UpdatedAt = %v, want the clock's %v", updated.UpdatedAt, r.Clock.Now())
		}
	})

	t.Run("an unknown label is refused and stored nowhere", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "Title")

		_, err := r.Cards.Update(ctx, port.UpdateCardCmd{ID: card.ID, Patch: domain.CardPatch{Label: ptr("chartreuse")}})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
		stored, _ := r.Store.Cards().Get(ctx, card.ID)
		if stored.Label != domain.LabelNone {
			t.Errorf("Label = %q, want it unchanged", stored.Label)
		}
	})

	t.Run("a deleted card is not found", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "Title")
		if err := r.Cards.Delete(ctx, port.DeleteCardCmd{ID: card.ID}); err != nil {
			t.Fatalf("deleting: %v", err)
		}

		_, err := r.Cards.Update(ctx, port.UpdateCardCmd{ID: card.ID, Patch: domain.CardPatch{Title: ptr("x")}})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})
}
