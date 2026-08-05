package domain

import (
	"strings"
	"time"
)

// MaxNameLength bounds board and column names. Not a business rule, a guard
// against a client posting a megabyte into a TEXT column.
const MaxNameLength = 120

// Board is the top-level container. Deletes are soft: with deferred sync,
// removing the row makes "this was deleted" indistinguishable from "this has
// not arrived yet" (00-scope.md §5).
type Board struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time

	// Columns is populated only by the read that paints the screen
	// (GET /api/boards/{id}). Every other read leaves it nil.
	Columns []Column
}

// NewBoard validates and builds a board. The id comes from the caller because
// clients generate their own ids (00-scope.md §5); the service supplies one
// when the client sends none.
func NewBoard(id, name string, now time.Time) (*Board, error) {
	name, err := validName(name)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, Invalid("id", "must not be empty")
	}
	return &Board{
		ID:        id,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Rename applies a new name and stamps the update. Returns false when the name
// is unchanged, so callers can skip a write and an event.
func (b *Board) Rename(name string, now time.Time) (bool, error) {
	name, err := validName(name)
	if err != nil {
		return false, err
	}
	if name == b.Name {
		return false, nil
	}
	b.Name = name
	b.UpdatedAt = now
	return true, nil
}

// Deleted reports whether the board has been soft-deleted.
func (b *Board) Deleted() bool { return b.DeletedAt != nil }

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Invalid("name", "must not be empty")
	}
	if len([]rune(name)) > MaxNameLength {
		return "", Invalid("name", "must be at most 120 characters")
	}
	return name, nil
}
