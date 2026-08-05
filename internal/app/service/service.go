// Package service holds the use cases. It orchestrates the domain and the
// driven ports and knows nothing about HTTP, SQL or WebSockets.
package service

import (
	"strings"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// normalise mirrors what the domain constructors do to a name or title, so an
// idempotency check compares like with like: posting "  Work  " twice is the
// same content, not a conflict.
func normalise(s string) string { return strings.TrimSpace(s) }

// Deps is everything the services are given. Wiring happens once, in the
// composition root (cmd/api/main.go).
type Deps struct {
	Boards  port.BoardRepository
	Columns port.ColumnRepository
	Cards   port.CardRepository
	OpLog   port.OpLogRepository
	Tx      port.TxManager
	Events  port.EventPublisher
	Clock   port.Clock
	IDs     port.IDGen
}

// compactColumns turns an ordered slice into positions 0..n-1. Every reorder
// path ends here, which is why the invariant holds without anyone remembering
// to enforce it.
func compactColumns(cols []domain.Column) []port.Position {
	out := make([]port.Position, len(cols))
	for i, c := range cols {
		out[i] = port.Position{ID: c.ID, Position: i}
	}
	return out
}

func compactCards(cards []domain.Card) []port.Position {
	out := make([]port.Position, len(cards))
	for i, c := range cards {
		out[i] = port.Position{ID: c.ID, Position: i}
	}
	return out
}

// indexOfCard returns the position of id in an ordered slice, or -1.
func indexOfCard(cards []domain.Card, id string) int {
	for i, c := range cards {
		if c.ID == id {
			return i
		}
	}
	return -1
}
