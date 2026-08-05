// Package port declares the seams of the application: what it offers
// (driving ports) and what it needs (driven ports). Nothing in here knows that
// SQLite, HTTP or WebSockets exist.
package port

import (
	"context"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// TxManager runs fn inside a transaction. Repositories pick the transaction up
// from the context, so a service can compose several writes without ever
// naming a *sql.Tx.
type TxManager interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Position is one row's place in its group. Reordering rewrites the whole
// group with these (00-scope.md §5).
type Position struct {
	ID       string
	Position int
}

// BoardRepository stores boards. Every "live" read excludes soft-deleted rows;
// the GetAny variants exist for idempotency checks, which have to be able to
// see a row that was already deleted.
type BoardRepository interface {
	Create(ctx context.Context, b *domain.Board) error
	Get(ctx context.Context, id string) (*domain.Board, error)
	GetAny(ctx context.Context, id string) (*domain.Board, error)
	// GetWithChildren returns the board with its live columns and cards
	// nested and ordered by position. This is the call that paints the
	// screen, so the ordering is part of the contract.
	GetWithChildren(ctx context.Context, id string) (*domain.Board, error)
	List(ctx context.Context) ([]domain.Board, error)
	Update(ctx context.Context, b *domain.Board) error
	// SoftDelete stamps deleted_at on the board and cascades to its columns
	// and cards (02-testing.md §4.2).
	SoftDelete(ctx context.Context, id string, at time.Time) error
}

// ColumnRepository stores columns.
type ColumnRepository interface {
	Create(ctx context.Context, c *domain.Column) error
	Get(ctx context.Context, id string) (*domain.Column, error)
	GetAny(ctx context.Context, id string) (*domain.Column, error)
	// ListByBoard returns live columns ordered by position.
	ListByBoard(ctx context.Context, boardID string) ([]domain.Column, error)
	Update(ctx context.Context, c *domain.Column) error
	// SetPositions rewrites positions for a group.
	//
	// It deliberately does NOT touch updated_at. Positions are recalculated
	// server-side and excluded from last-write-wins (00-scope.md §6.5);
	// bumping a sibling's timestamp because its neighbour moved would make a
	// pending offline edit to that sibling lose a conflict it should win.
	SetPositions(ctx context.Context, positions []Position) error
	SoftDelete(ctx context.Context, id string, at time.Time) error
}

// CardRepository stores cards.
type CardRepository interface {
	Create(ctx context.Context, c *domain.Card) error
	Get(ctx context.Context, id string) (*domain.Card, error)
	GetAny(ctx context.Context, id string) (*domain.Card, error)
	// ListByColumn returns live cards ordered by position.
	ListByColumn(ctx context.Context, columnID string) ([]domain.Card, error)
	// CountByColumn counts live cards. This is what the WIP rule reads.
	CountByColumn(ctx context.Context, columnID string) (int, error)
	Update(ctx context.Context, c *domain.Card) error
	// SetPositions: same contract, and same silence about updated_at, as
	// ColumnRepository.SetPositions.
	SetPositions(ctx context.Context, positions []Position) error
	SoftDelete(ctx context.Context, id string, at time.Time) error
}

// OpLogRepository is the idempotency ledger for the offline queue. A phone
// that lost signal mid-POST resends the whole batch; this is what makes that
// harmless (01-structure.md §1.5).
type OpLogRepository interface {
	Applied(ctx context.Context, opID string) (bool, error)
	Record(ctx context.Context, opID string, at time.Time) error
}
