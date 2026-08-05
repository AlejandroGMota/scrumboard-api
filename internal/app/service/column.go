package service

import (
	"context"
	"errors"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Column implements port.ColumnService.
type Column struct{ d Deps }

var _ port.ColumnService = (*Column)(nil)

func NewColumn(d Deps) *Column { return &Column{d: d} }

// Create places the new column last: position = max + 1, which with the
// compact invariant is simply the number of live columns.
func (s *Column) Create(ctx context.Context, cmd port.CreateColumnCmd) (*domain.Column, bool, error) {
	if _, err := s.d.Boards.Get(ctx, cmd.BoardID); err != nil {
		return nil, false, err
	}
	if cmd.ID != "" {
		existing, err := s.d.Columns.GetAny(ctx, cmd.ID)
		switch {
		case err == nil:
			if existing.Deleted() || existing.BoardID != cmd.BoardID || existing.Name != normalise(cmd.Name) || !sameLimit(existing.WIPLimit, cmd.WIPLimit) {
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

	var created *domain.Column
	err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		siblings, err := s.d.Columns.ListByBoard(ctx, cmd.BoardID)
		if err != nil {
			return err
		}
		c, err := domain.NewColumn(id, cmd.BoardID, cmd.Name, cmd.WIPLimit, len(siblings), s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Columns.Create(ctx, c); err != nil {
			return err
		}
		created = c
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	s.publish(ctx, domain.EventColumnCreated, created, cmd.OpID)
	return created, true, nil
}

func (s *Column) Update(ctx context.Context, cmd port.UpdateColumnCmd) (*domain.Column, error) {
	c, err := s.d.Columns.Get(ctx, cmd.ID)
	if err != nil {
		return nil, err
	}
	changed, err := c.Apply(cmd.Patch, s.d.Clock.Now())
	if err != nil {
		return nil, err
	}
	if !changed {
		return c, nil
	}
	if err := s.d.Columns.Update(ctx, c); err != nil {
		return nil, err
	}
	s.publish(ctx, domain.EventColumnUpdated, c, cmd.OpID)
	return c, nil
}

// Delete refuses a column that still holds live cards (00-scope.md §8): the UI
// moves or deletes them first. A column whose cards are all soft-deleted can
// go. Deleting twice is a no-op, and the surviving columns compact so no gap
// is left behind.
func (s *Column) Delete(ctx context.Context, cmd port.DeleteColumnCmd) error {
	c, err := s.d.Columns.GetAny(ctx, cmd.ID)
	if err != nil {
		return err
	}
	if c.Deleted() {
		return nil
	}
	n, err := s.d.Cards.CountByColumn(ctx, cmd.ID)
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.ErrColumnNotEmpty
	}

	now := s.d.Clock.Now()
	if err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.d.Columns.SoftDelete(ctx, cmd.ID, now); err != nil {
			return err
		}
		remaining, err := s.d.Columns.ListByBoard(ctx, c.BoardID)
		if err != nil {
			return err
		}
		return s.d.Columns.SetPositions(ctx, compactColumns(remaining))
	}); err != nil {
		return err
	}
	c.DeletedAt = &now
	s.publish(ctx, domain.EventColumnDeleted, c, cmd.OpID)
	return nil
}

// Reorder takes the complete list of ids in the desired order. Anything else
// is a 400: a partial list is how ordering gets silently corrupted, and it is
// the easiest endpoint in the API to leave half-finished (02-testing.md §4.3).
func (s *Column) Reorder(ctx context.Context, cmd port.ReorderColumnsCmd) ([]domain.Column, error) {
	if _, err := s.d.Boards.Get(ctx, cmd.BoardID); err != nil {
		return nil, err
	}

	var ordered []domain.Column
	err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.d.Columns.ListByBoard(ctx, cmd.BoardID)
		if err != nil {
			return err
		}
		byID := make(map[string]domain.Column, len(current))
		for _, c := range current {
			byID[c.ID] = c
		}

		if len(cmd.IDs) != len(current) {
			return domain.Invalid("ids", "must list every column of the board exactly once")
		}
		seen := make(map[string]bool, len(cmd.IDs))
		ordered = make([]domain.Column, 0, len(cmd.IDs))
		for _, id := range cmd.IDs {
			c, ok := byID[id]
			if !ok {
				return domain.Invalid("ids", "contains an id that is not a live column of this board: "+id)
			}
			if seen[id] {
				return domain.Invalid("ids", "contains a duplicate: "+id)
			}
			seen[id] = true
			c.Position = len(ordered)
			ordered = append(ordered, c)
		}
		return s.d.Columns.SetPositions(ctx, compactColumns(ordered))
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(ordered))
	for i, c := range ordered {
		ids[i] = c.ID
	}
	s.d.Events.Publish(ctx, domain.Event{
		Type:    domain.EventColumnsOrdered,
		BoardID: cmd.BoardID,
		At:      s.d.Clock.Now(),
		OpID:    cmd.OpID,
		Payload: domain.ColumnsOrderedPayload{BoardID: cmd.BoardID, IDs: ids},
	})
	return ordered, nil
}

func (s *Column) publish(ctx context.Context, t domain.EventType, c *domain.Column, opID string) {
	s.d.Events.Publish(ctx, domain.Event{
		Type:    t,
		BoardID: c.BoardID,
		At:      s.d.Clock.Now(),
		OpID:    opID,
		Payload: c,
	})
}

func sameLimit(a, b *int) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}
