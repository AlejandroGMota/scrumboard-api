package service

import (
	"context"
	"errors"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Board implements port.BoardService.
type Board struct{ d Deps }

var _ port.BoardService = (*Board)(nil)

func NewBoard(d Deps) *Board { return &Board{d: d} }

func (s *Board) List(ctx context.Context) ([]domain.Board, error) {
	return s.d.Boards.List(ctx)
}

// Get returns the board with its columns and cards nested and ordered. This is
// the call that paints the screen.
func (s *Board) Get(ctx context.Context, id string) (*domain.Board, error) {
	return s.d.Boards.GetWithChildren(ctx, id)
}

// Create is idempotent on a client-supplied id: the same id with the same
// content returns the existing board and created=false, so the handler answers
// 200 instead of 201. The same id with different content is a conflict — two
// devices generating the same UUID means something is wrong, and silently
// overwriting one of them would hide it (02-testing.md §4.2).
func (s *Board) Create(ctx context.Context, cmd port.CreateBoardCmd) (*domain.Board, bool, error) {
	if cmd.ID != "" {
		existing, err := s.d.Boards.GetAny(ctx, cmd.ID)
		switch {
		case err == nil:
			if existing.Deleted() || existing.Name != normalise(cmd.Name) {
				return nil, false, domain.ErrConflict
			}
			return existing, false, nil
		case !errors.Is(err, domain.ErrNotFound):
			return nil, false, err
		}
	}

	id := cmd.ID
	if id == "" {
		id = s.d.IDs.New()
	}
	b, err := domain.NewBoard(id, cmd.Name, s.d.Clock.Now())
	if err != nil {
		return nil, false, err
	}
	if err := s.d.Boards.Create(ctx, b); err != nil {
		return nil, false, err
	}
	s.publish(ctx, domain.EventBoardCreated, b, cmd.OpID)
	return b, true, nil
}

func (s *Board) Update(ctx context.Context, cmd port.UpdateBoardCmd) (*domain.Board, error) {
	b, err := s.d.Boards.Get(ctx, cmd.ID)
	if err != nil {
		return nil, err
	}
	if cmd.Name == nil {
		return b, nil
	}
	changed, err := b.Rename(*cmd.Name, s.d.Clock.Now())
	if err != nil {
		return nil, err
	}
	if !changed {
		return b, nil
	}
	if err := s.d.Boards.Update(ctx, b); err != nil {
		return nil, err
	}
	s.publish(ctx, domain.EventBoardUpdated, b, cmd.OpID)
	return b, nil
}

// Delete is soft and idempotent: deleting an already-deleted board is a 204
// with no second event. Deleting one that never existed is still a 404 — the
// client is telling us about something we have never heard of, which is worth
// knowing.
func (s *Board) Delete(ctx context.Context, cmd port.DeleteBoardCmd) error {
	b, err := s.d.Boards.GetAny(ctx, cmd.ID)
	if err != nil {
		return err
	}
	if b.Deleted() {
		return nil
	}
	now := s.d.Clock.Now()
	if err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		return s.d.Boards.SoftDelete(ctx, cmd.ID, now)
	}); err != nil {
		return err
	}
	b.DeletedAt = &now
	s.publish(ctx, domain.EventBoardDeleted, b, cmd.OpID)
	return nil
}

func (s *Board) publish(ctx context.Context, t domain.EventType, b *domain.Board, opID string) {
	s.d.Events.Publish(ctx, domain.Event{
		Type:    t,
		BoardID: b.ID,
		At:      s.d.Clock.Now(),
		OpID:    opID,
		Payload: b,
	})
}
