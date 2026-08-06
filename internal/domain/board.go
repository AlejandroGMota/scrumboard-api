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

	// LabelNames gives the six palette colours a meaning, per board: what
	// "blue" stands for on THIS board. Keyed by the colour, so an unnamed
	// colour is simply absent rather than an empty string.
	//
	// It lives on the board rather than globally because the vocabulary is
	// the board's: a life board's blue is an area of life, a project board's
	// blue is a kind of work, and nothing useful is served by forcing them to
	// agree.
	LabelNames map[string]string

	// Columns is populated only by the read that paints the screen
	// (GET /api/boards/{id}). Every other read leaves it nil.
	Columns []Column
}

// MaxLabelNameLength bounds one meaning. A legend is a glance, not a note.
const MaxLabelNameLength = 32

// SetLabelNames replaces the whole legend, validating every entry.
//
// Replace rather than merge: a legend is small and always edited as a whole,
// and merging would make "remove this meaning" impossible to express.
func (b *Board) SetLabelNames(names map[string]string, now time.Time) (bool, error) {
	cleaned := map[string]string{}
	for colour, meaning := range names {
		if err := ValidLabel(colour); err != nil {
			return false, err
		}
		if colour == LabelNone {
			return false, Invalid("label_names", "the empty label cannot be named")
		}
		meaning = strings.TrimSpace(meaning)
		if meaning == "" {
			// An empty meaning is how a colour goes back to being unnamed.
			continue
		}
		if len([]rune(meaning)) > MaxLabelNameLength {
			return false, Invalid("label_names", "each meaning must be at most 32 characters")
		}
		cleaned[colour] = meaning
	}

	if sameNames(b.LabelNames, cleaned) {
		return false, nil
	}
	b.LabelNames = cleaned
	b.UpdatedAt = now
	return true, nil
}

func sameNames(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
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
