package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	// MaxTitleLength bounds a card title. Same reasoning as MaxNameLength.
	MaxTitleLength = 500
	// MaxDescriptionLength bounds the description. Plain text in v1
	// (00-scope.md §11): markdown means a renderer, sanitising and an editor.
	MaxDescriptionLength = 50000
)

// Labels is the fixed palette (00-scope.md §11 question 2). Free-text hex
// would drift into forty near-identical greens, so the set is closed here and
// the frontend reads it from GET /api/labels.
var Labels = []string{"red", "orange", "yellow", "green", "blue", "purple"}

// LabelNone is the absence of a label, and the zero value of the field.
const LabelNone = ""

// Card is the unit that moves. Position orders it inside its column.
type Card struct {
	ID          string
	ColumnID    string
	Title       string
	Description string
	Label       string
	Position    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// NewCard validates and builds a card. Position is assigned by the service.
func NewCard(id, columnID, title, description, label string, position int, now time.Time) (*Card, error) {
	if id == "" {
		return nil, Invalid("id", "must not be empty")
	}
	if columnID == "" {
		return nil, Invalid("column_id", "must not be empty")
	}
	title, err := validTitle(title)
	if err != nil {
		return nil, err
	}
	if err := validDescription(description); err != nil {
		return nil, err
	}
	if err := ValidLabel(label); err != nil {
		return nil, err
	}
	return &Card{
		ID:          id,
		ColumnID:    columnID,
		Title:       title,
		Description: description,
		Label:       label,
		Position:    position,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// CardPatch is a partial update: nil means "leave it alone".
type CardPatch struct {
	Title       *string
	Description *string
	Label       *string
}

// Apply mutates the card with whatever the patch carries. Returns false when
// nothing changed, so the service can skip the write and the event.
//
// Every field is validated before any field is written: a patch that fails
// halfway through must leave the card exactly as it was, or a 400 quietly
// becomes a partial update.
func (c *Card) Apply(p CardPatch, now time.Time) (bool, error) {
	next := *c
	changed := false

	if p.Title != nil {
		title, err := validTitle(*p.Title)
		if err != nil {
			return false, err
		}
		if title != next.Title {
			next.Title, changed = title, true
		}
	}
	if p.Description != nil {
		if err := validDescription(*p.Description); err != nil {
			return false, err
		}
		if *p.Description != next.Description {
			next.Description, changed = *p.Description, true
		}
	}
	if p.Label != nil {
		if err := ValidLabel(*p.Label); err != nil {
			return false, err
		}
		if *p.Label != next.Label {
			next.Label, changed = *p.Label, true
		}
	}

	if !changed {
		return false, nil
	}
	next.UpdatedAt = now
	*c = next
	return true, nil
}

// Deleted reports whether the card has been soft-deleted.
func (c *Card) Deleted() bool { return c.DeletedAt != nil }

// ValidLabel checks a label against the closed palette. The empty string means
// "no label" and is always valid.
func ValidLabel(label string) error {
	if label == LabelNone {
		return nil
	}
	for _, l := range Labels {
		if l == label {
			return nil
		}
	}
	return Invalid("label", "must be one of: "+strings.Join(Labels, ", "))
}

func validTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", Invalid("title", "must not be empty")
	}
	if len([]rune(title)) > MaxTitleLength {
		return "", Invalid("title", fmt.Sprintf("must be at most %d characters", MaxTitleLength))
	}
	return title, nil
}

func validDescription(d string) error {
	if len([]rune(d)) > MaxDescriptionLength {
		return Invalid("description", fmt.Sprintf("must be at most %d characters", MaxDescriptionLength))
	}
	return nil
}

// --- position rules -------------------------------------------------------
//
// Positions are always 0..n-1 within a group: no gaps, no ties. Reordering
// rewrites the whole group inside one transaction (00-scope.md §5). At this
// data volume nothing more sophisticated pays for itself, and the invariant is
// worth more than the writes it costs.

// ClampIndex forces a target index into [0, n]. A destination past the end of
// the column becomes the end rather than a 400: it is kinder, and the UI drops
// cards past the last one all the time (02-testing.md §4.4).
func ClampIndex(idx, n int) int {
	if idx < 0 {
		return 0
	}
	if idx > n {
		return n
	}
	return idx
}

// MoveWithin returns the order after moving the element at `from` to index
// `to`, both within the same slice. It is the whole of the same-column reorder
// rule, and the reason it is a pure function is that the classic off-by-one
// only shows up moving in one direction.
func MoveWithin[T any](items []T, from, to int) []T {
	if from < 0 || from >= len(items) {
		return items
	}
	to = ClampIndex(to, len(items)-1)
	if from == to {
		return items
	}
	out := make([]T, 0, len(items))
	moved := items[from]
	for i, it := range items {
		if i == from {
			continue
		}
		out = append(out, it)
	}
	rest := make([]T, 0, len(items))
	rest = append(rest, out[:to]...)
	rest = append(rest, moved)
	rest = append(rest, out[to:]...)
	return rest
}

// InsertAt returns the order after inserting an element at index `to`, used
// when a card arrives from another column.
func InsertAt[T any](items []T, item T, to int) []T {
	to = ClampIndex(to, len(items))
	out := make([]T, 0, len(items)+1)
	out = append(out, items[:to]...)
	out = append(out, item)
	out = append(out, items[to:]...)
	return out
}

// RemoveAt returns the order with the element at `from` taken out, which is
// how the source column compacts after a card leaves it.
func RemoveAt[T any](items []T, from int) []T {
	if from < 0 || from >= len(items) {
		return items
	}
	out := make([]T, 0, len(items)-1)
	out = append(out, items[:from]...)
	out = append(out, items[from+1:]...)
	return out
}
