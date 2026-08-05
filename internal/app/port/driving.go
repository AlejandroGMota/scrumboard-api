package port

import (
	"context"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Every command carries an optional OpID: the client-generated id of the
// request that caused the change. It travels back out on the broadcast event
// so the originating device can ignore its own echo (00-scope.md §8), and it
// is the same id the offline queue uses for idempotency.

type CreateBoardCmd struct {
	ID   string // may be empty: the server generates one
	Name string
	OpID string
}

type UpdateBoardCmd struct {
	ID   string
	Name *string
	OpID string
}

type DeleteBoardCmd struct {
	ID   string
	OpID string
}

type CreateColumnCmd struct {
	ID       string
	BoardID  string
	Name     string
	WIPLimit *int
	OpID     string
}

type UpdateColumnCmd struct {
	ID    string
	Patch domain.ColumnPatch
	OpID  string
}

type DeleteColumnCmd struct {
	ID   string
	OpID string
}

type ReorderColumnsCmd struct {
	BoardID string
	IDs     []string
	OpID    string
}

type CreateCardCmd struct {
	ID          string
	ColumnID    string
	Title       string
	Description string
	Label       string
	OpID        string
}

type UpdateCardCmd struct {
	ID    string
	Patch domain.CardPatch
	OpID  string
}

type DeleteCardCmd struct {
	ID   string
	OpID string
}

type MoveCardCmd struct {
	ID         string
	ToColumnID string
	ToIndex    int
	OpID       string
}

// BoardService is the inbound port for boards.
//
// Create returns created=false when the call was a repeat of one already
// applied with identical content: the handler answers 200 instead of 201 and
// nothing is duplicated (02-testing.md §4.2).
type BoardService interface {
	List(ctx context.Context) ([]domain.Board, error)
	Get(ctx context.Context, id string) (*domain.Board, error)
	Create(ctx context.Context, cmd CreateBoardCmd) (b *domain.Board, created bool, err error)
	Update(ctx context.Context, cmd UpdateBoardCmd) (*domain.Board, error)
	Delete(ctx context.Context, cmd DeleteBoardCmd) error
}

// ColumnService is the inbound port for columns.
type ColumnService interface {
	Create(ctx context.Context, cmd CreateColumnCmd) (c *domain.Column, created bool, err error)
	Update(ctx context.Context, cmd UpdateColumnCmd) (*domain.Column, error)
	Delete(ctx context.Context, cmd DeleteColumnCmd) error
	Reorder(ctx context.Context, cmd ReorderColumnsCmd) ([]domain.Column, error)
}

// CardService is the inbound port for cards. Move is the delicate one.
type CardService interface {
	Create(ctx context.Context, cmd CreateCardCmd) (c *domain.Card, created bool, err error)
	Update(ctx context.Context, cmd UpdateCardCmd) (*domain.Card, error)
	Delete(ctx context.Context, cmd DeleteCardCmd) error
	Move(ctx context.Context, cmd MoveCardCmd) (*domain.Card, error)
}

// SyncService applies a batch queued while a client was offline. It never
// fails as a whole: each operation reports its own outcome.
type SyncService interface {
	Apply(ctx context.Context, ops []domain.Operation) ([]domain.OpResult, error)
}
