package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

func TestBoardCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("generates an id when the client sends none", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b, created, err := r.Boards.Create(ctx, port.CreateBoardCmd{Name: "Work"})
		if err != nil || !created {
			t.Fatalf("Create() = %v, %v; want created", created, err)
		}
		if b.ID != "id-1" {
			t.Errorf("ID = %q, want the generated id-1", b.ID)
		}
	})

	t.Run("keeps a client-generated id", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b, _, err := r.Boards.Create(ctx, port.CreateBoardCmd{ID: "0192-abc", Name: "Work"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b.ID != "0192-abc" {
			t.Errorf("ID = %q, want the client's id", b.ID)
		}
	})

	t.Run("the same id with the same content does not duplicate", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		if _, _, err := r.Boards.Create(ctx, port.CreateBoardCmd{ID: "b1", Name: "Work"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		b, created, err := r.Boards.Create(ctx, port.CreateBoardCmd{ID: "b1", Name: "  Work  "})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if created {
			t.Error("created = true on a repeat; the handler would answer 201 twice")
		}
		if b.Name != "Work" {
			t.Errorf("Name = %q, want the stored one", b.Name)
		}
		boards, _ := r.Boards.List(ctx)
		if len(boards) != 1 {
			t.Errorf("%d boards stored, want 1", len(boards))
		}
		if n := len(r.Events.OfType(domain.EventBoardCreated)); n != 1 {
			t.Errorf("%d board.created events, want exactly 1", n)
		}
	})

	t.Run("the same id with different content is a conflict", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		if _, _, err := r.Boards.Create(ctx, port.CreateBoardCmd{ID: "b1", Name: "Work"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, _, err := r.Boards.Create(ctx, port.CreateBoardCmd{ID: "b1", Name: "Personal"}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want %v", err, domain.ErrConflict)
		}
	})

	t.Run("publishes board.created with the op id", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b, _, _ := r.Boards.Create(ctx, port.CreateBoardCmd{Name: "Work", OpID: "op-1"})

		events := r.Events.OfType(domain.EventBoardCreated)
		if len(events) != 1 {
			t.Fatalf("%d events, want 1", len(events))
		}
		if events[0].OpID != "op-1" {
			t.Errorf("OpID = %q, want op-1: without it the originating device cannot ignore its own echo", events[0].OpID)
		}
		if events[0].BoardID != b.ID {
			t.Errorf("BoardID = %q, want %q", events[0].BoardID, b.ID)
		}
	})

	t.Run("an invalid name never reaches the repository", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		if _, _, err := r.Boards.Create(ctx, port.CreateBoardCmd{Name: "   "}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
		boards, _ := r.Boards.List(ctx)
		if len(boards) != 0 {
			t.Errorf("%d boards stored, want 0", len(boards))
		}
		if len(r.Events.Events()) != 0 {
			t.Error("a refused create must publish nothing")
		}
	})
}

func TestBoardUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("renaming to the same name publishes nothing", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		r.Events.Reset()

		if _, err := r.Boards.Update(ctx, port.UpdateBoardCmd{ID: b.ID, Name: ptr("Work")}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.Events.Events()) != 0 {
			t.Error("a no-op update must not wake the other devices")
		}
	})

	t.Run("a rename stamps updated_at from the clock", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		r.Clock.Advance(5_000_000_000) // five seconds

		updated, err := r.Boards.Update(ctx, port.UpdateBoardCmd{ID: b.ID, Name: ptr("Personal")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated.UpdatedAt.Equal(r.Clock.Now()) {
			t.Errorf("UpdatedAt = %v, want the clock's %v", updated.UpdatedAt, r.Clock.Now())
		}
		if !updated.CreatedAt.Before(updated.UpdatedAt) {
			t.Error("created_at should predate updated_at after a rename")
		}
	})

	t.Run("a nonexistent board is not found", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		if _, err := r.Boards.Update(ctx, port.UpdateBoardCmd{ID: "nope", Name: ptr("x")}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})
}

func TestBoardDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("soft-deletes and cascades to columns and cards", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		col := r.column(t, b.ID, "Todo", nil)
		card := r.card(t, col.ID, "Write the tests")

		if err := r.Boards.Delete(ctx, port.DeleteBoardCmd{ID: b.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := r.Boards.Get(ctx, b.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("reading a deleted board = %v, want %v", err, domain.ErrNotFound)
		}
		// The rows are still there — that is the point of a soft delete.
		dead, err := r.Store.Cards().GetAny(ctx, card.ID)
		if err != nil {
			t.Fatalf("the card row should still exist: %v", err)
		}
		if !dead.Deleted() {
			t.Error("the cascade did not reach the card")
		}
		deadCol, _ := r.Store.Columns().GetAny(ctx, col.ID)
		if !deadCol.Deleted() {
			t.Error("the cascade did not reach the column")
		}
	})

	t.Run("deleting twice is a no-op with one event", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		b := r.board(t, "Work")
		r.Events.Reset()

		if err := r.Boards.Delete(ctx, port.DeleteBoardCmd{ID: b.ID}); err != nil {
			t.Fatalf("first delete: %v", err)
		}
		if err := r.Boards.Delete(ctx, port.DeleteBoardCmd{ID: b.ID}); err != nil {
			t.Fatalf("second delete must be idempotent, got %v", err)
		}
		if n := len(r.Events.OfType(domain.EventBoardDeleted)); n != 1 {
			t.Errorf("%d board.deleted events, want 1", n)
		}
	})

	t.Run("deleting something that never existed is not found", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		if err := r.Boards.Delete(ctx, port.DeleteBoardCmd{ID: "nope"}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
		}
	})
}

func TestBoardGetNestsColumnsAndCardsInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := newRig(t)
	b := r.board(t, "Work")
	todo := r.column(t, b.ID, "Todo", nil)
	r.column(t, b.ID, "Doing", nil)
	r.card(t, todo.ID, "first")
	r.card(t, todo.ID, "second")

	full, err := r.Boards.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(full.Columns) != 2 {
		t.Fatalf("%d columns, want 2", len(full.Columns))
	}
	if full.Columns[0].Name != "Todo" || full.Columns[1].Name != "Doing" {
		t.Errorf("columns out of order: %q, %q", full.Columns[0].Name, full.Columns[1].Name)
	}
	if len(full.Columns[0].Cards) != 2 {
		t.Fatalf("%d cards in Todo, want 2", len(full.Columns[0].Cards))
	}
	if full.Columns[0].Cards[0].Title != "first" {
		t.Errorf("cards out of order: %q first", full.Columns[0].Cards[0].Title)
	}
}
