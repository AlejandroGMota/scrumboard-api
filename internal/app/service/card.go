package service

import (
	"context"
	"errors"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Card implements port.CardService. The move half lives in move.go.
type Card struct{ d Deps }

var _ port.CardService = (*Card)(nil)

func NewCard(d Deps) *Card { return &Card{d: d} }

// Create appends the card to its column, after checking the WIP limit. That
// check is the project's only real business rule, and it is enforced inside
// the same transaction as the insert: reading the count outside it would let
// two concurrent creates both see room for one.
func (s *Card) Create(ctx context.Context, cmd port.CreateCardCmd) (*domain.Card, bool, error) {
	col, err := s.d.Columns.Get(ctx, cmd.ColumnID)
	if err != nil {
		return nil, false, err
	}
	if cmd.ID != "" {
		existing, err := s.d.Cards.GetAny(ctx, cmd.ID)
		switch {
		case err == nil:
			if existing.Deleted() ||
				existing.ColumnID != cmd.ColumnID ||
				existing.Title != normalise(cmd.Title) ||
				existing.Description != cmd.Description ||
				existing.Label != cmd.Label {
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

	var created *domain.Card
	err = s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		siblings, err := s.d.Cards.ListByColumn(ctx, cmd.ColumnID)
		if err != nil {
			return err
		}
		if err := col.CanAccept(len(siblings)); err != nil {
			return err
		}
		c, err := domain.NewCard(id, cmd.ColumnID, cmd.Title, cmd.Description, cmd.Label, len(siblings), s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Cards.Create(ctx, c); err != nil {
			return err
		}
		created = c
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	s.publish(ctx, domain.EventCardCreated, created, col.BoardID, cmd.OpID)
	return created, true, nil
}

func (s *Card) Update(ctx context.Context, cmd port.UpdateCardCmd) (*domain.Card, error) {
	c, err := s.d.Cards.Get(ctx, cmd.ID)
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
	if err := s.d.Cards.Update(ctx, c); err != nil {
		return nil, err
	}
	boardID, err := s.boardOf(ctx, c.ColumnID)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, domain.EventCardUpdated, c, boardID, cmd.OpID)
	return c, nil
}

// Delete is soft, idempotent, and compacts the column it leaves behind: a gap
// in the positions would be invisible until the next move produced a tie.
func (s *Card) Delete(ctx context.Context, cmd port.DeleteCardCmd) error {
	c, err := s.d.Cards.GetAny(ctx, cmd.ID)
	if err != nil {
		return err
	}
	if c.Deleted() {
		return nil
	}
	boardID, err := s.boardOf(ctx, c.ColumnID)
	if err != nil {
		return err
	}

	now := s.d.Clock.Now()
	if err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.d.Cards.SoftDelete(ctx, cmd.ID, now); err != nil {
			return err
		}
		remaining, err := s.d.Cards.ListByColumn(ctx, c.ColumnID)
		if err != nil {
			return err
		}
		return s.d.Cards.SetPositions(ctx, compactCards(remaining))
	}); err != nil {
		return err
	}
	c.DeletedAt = &now
	s.publish(ctx, domain.EventCardDeleted, c, boardID, cmd.OpID)
	return nil
}

// boardOf resolves a column id to its board, including columns that have been
// soft-deleted: a card event still has to say which board it belongs to.
func (s *Card) boardOf(ctx context.Context, columnID string) (string, error) {
	col, err := s.d.Columns.GetAny(ctx, columnID)
	if err != nil {
		return "", err
	}
	return col.BoardID, nil
}

func (s *Card) publish(ctx context.Context, t domain.EventType, c *domain.Card, boardID, opID string) {
	s.d.Events.Publish(ctx, domain.Event{
		Type:    t,
		BoardID: boardID,
		At:      s.d.Clock.Now(),
		OpID:    opID,
		Payload: c,
	})
}
