package domain

import "time"

// Column belongs to a board and orders its cards by Position. Like boards and
// cards, it is deleted softly: the offline queue needs to tell a deletion from
// an absence.
type Column struct {
	ID        string
	BoardID   string
	Name      string
	Position  int
	WIPLimit  *int // nil = no limit
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time

	// Cards is populated only by the board read. Nil everywhere else.
	Cards []Card
}

// NewColumn validates and builds a column. Position is assigned by the service
// (max + 1 within the board), not here: the column cannot see its siblings.
func NewColumn(id, boardID, name string, wipLimit *int, position int, now time.Time) (*Column, error) {
	name, err := validName(name)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, Invalid("id", "must not be empty")
	}
	if boardID == "" {
		return nil, Invalid("board_id", "must not be empty")
	}
	if err := validWIPLimit(wipLimit); err != nil {
		return nil, err
	}
	return &Column{
		ID:        id,
		BoardID:   boardID,
		Name:      name,
		Position:  position,
		WIPLimit:  wipLimit,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ColumnPatch is a partial update. A nil field means "leave it alone", which is
// why WIPLimit needs two levels: unset, versus set to null to clear the limit.
type ColumnPatch struct {
	Name        *string
	WIPLimit    **int
	SetWIPLimit bool
}

// Apply mutates the column with whatever the patch carries. Returns false when
// nothing actually changed. Like Card.Apply, it validates every field before
// writing any of them.
func (c *Column) Apply(p ColumnPatch, now time.Time) (bool, error) {
	next := *c
	changed := false

	if p.Name != nil {
		name, err := validName(*p.Name)
		if err != nil {
			return false, err
		}
		if name != next.Name {
			next.Name, changed = name, true
		}
	}
	if p.SetWIPLimit {
		var limit *int
		if p.WIPLimit != nil {
			limit = *p.WIPLimit
		}
		if err := validWIPLimit(limit); err != nil {
			return false, err
		}
		if !sameLimit(next.WIPLimit, limit) {
			next.WIPLimit, changed = limit, true
		}
	}

	if !changed {
		return false, nil
	}
	next.UpdatedAt = now
	*c = next
	return true, nil
}

// CanAccept reports whether a column holding liveCards can take one more.
//
// Note what this does NOT do: it never looks at whether the column is already
// over its limit. Lowering wip_limit below the current card count is allowed
// (00-scope.md §8) — the column simply sits over limit until cards move out,
// and every further insertion is refused until it is back under.
func (c *Column) CanAccept(liveCards int) error {
	if c.WIPLimit == nil {
		return nil
	}
	if liveCards >= *c.WIPLimit {
		return ErrWIPExceeded
	}
	return nil
}

// Deleted reports whether the column has been soft-deleted.
func (c *Column) Deleted() bool { return c.DeletedAt != nil }

func validWIPLimit(l *int) error {
	if l == nil {
		return nil
	}
	if *l < 1 {
		return Invalid("wip_limit", "must be at least 1, or null for no limit")
	}
	return nil
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
