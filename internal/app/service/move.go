package service

import (
	"context"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Move is the most delicate operation in the API, and the one most of the real
// logic lives in. Everything it does happens inside one transaction, because a
// half-applied move — a card that left its column but never arrived — is worse
// than a refused one.
//
// The invariant it must never break: after any move, positions in both the
// source and the target column are 0..n-1, with no gaps and no ties.
func (s *Card) Move(ctx context.Context, cmd port.MoveCardCmd) (*domain.Card, error) {
	if cmd.ToIndex < 0 {
		return nil, domain.Invalid("to_index", "must not be negative")
	}

	var (
		moved      *domain.Card
		fromColumn string
		boardID    string
		changed    bool
	)

	err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		card, err := s.d.Cards.Get(ctx, cmd.ID)
		if err != nil {
			return err
		}
		source, err := s.d.Columns.Get(ctx, card.ColumnID)
		if err != nil {
			return err
		}
		target, err := s.d.Columns.Get(ctx, cmd.ToColumnID)
		if err != nil {
			return err
		}
		// A card cannot leave its board. Allowing it would mean a board
		// silently gaining cards it never created.
		if target.BoardID != source.BoardID {
			return domain.Invalid("to_column_id", "belongs to a different board")
		}

		fromColumn, boardID = source.ID, source.BoardID

		if source.ID == target.ID {
			return s.reorderWithin(ctx, card, cmd.ToIndex, &moved, &changed)
		}
		return s.moveAcross(ctx, card, source, target, cmd.ToIndex, &moved, &changed)
	})
	if err != nil {
		return nil, err
	}

	if changed {
		s.d.Events.Publish(ctx, domain.Event{
			Type:    domain.EventCardMoved,
			BoardID: boardID,
			At:      s.d.Clock.Now(),
			OpID:    cmd.OpID,
			Payload: domain.CardMovedPayload{
				Card:         moved,
				FromColumnID: fromColumn,
				ToColumnID:   moved.ColumnID,
			},
		})
	}
	return moved, nil
}

// reorderWithin handles a move inside one column. Moving a card to the index
// it already occupies is a 200 that changes nothing — including no event, so a
// client dragging a card one pixel does not wake every other device.
func (s *Card) reorderWithin(ctx context.Context, card *domain.Card, toIndex int, moved **domain.Card, changed *bool) error {
	cards, err := s.d.Cards.ListByColumn(ctx, card.ColumnID)
	if err != nil {
		return err
	}
	from := indexOfCard(cards, card.ID)
	if from < 0 {
		return domain.ErrNotFound
	}
	// An index past the end clamps to the end rather than failing: the UI
	// drops cards below the last one constantly (02-testing.md §4.4).
	to := domain.ClampIndex(toIndex, len(cards)-1)
	if from == to {
		*moved = card
		return nil
	}

	ordered := domain.MoveWithin(cards, from, to)
	if err := s.d.Cards.SetPositions(ctx, compactCards(ordered)); err != nil {
		return err
	}
	card.Position = to
	*moved, *changed = card, true
	return nil
}

// moveAcross handles a move between two columns of the same board: the target
// must have room, the source compacts, and the target opens a slot.
func (s *Card) moveAcross(ctx context.Context, card *domain.Card, source, target *domain.Column, toIndex int, moved **domain.Card, changed *bool) error {
	targetCards, err := s.d.Cards.ListByColumn(ctx, target.ID)
	if err != nil {
		return err
	}
	// The WIP check happens before anything is written. Returning 409 from
	// here rolls back nothing, because nothing has been done — which is the
	// point: a 409 that left the card half-moved would be worse than having
	// no rule at all.
	if err := target.CanAccept(len(targetCards)); err != nil {
		return err
	}

	sourceCards, err := s.d.Cards.ListByColumn(ctx, source.ID)
	if err != nil {
		return err
	}
	from := indexOfCard(sourceCards, card.ID)
	if from < 0 {
		return domain.ErrNotFound
	}

	to := domain.ClampIndex(toIndex, len(targetCards))
	card.ColumnID = target.ID
	card.Position = to
	card.UpdatedAt = s.d.Clock.Now()
	if err := s.d.Cards.Update(ctx, card); err != nil {
		return err
	}

	if err := s.d.Cards.SetPositions(ctx, compactCards(domain.RemoveAt(sourceCards, from))); err != nil {
		return err
	}
	if err := s.d.Cards.SetPositions(ctx, compactCards(domain.InsertAt(targetCards, *card, to))); err != nil {
		return err
	}

	*moved, *changed = card, true
	return nil
}
