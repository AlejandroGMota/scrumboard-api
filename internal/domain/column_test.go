package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func TestNewColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		id       string
		boardID  string
		colName  string
		wipLimit *int
		wantErr  error
	}{
		{name: "no limit", id: "c1", boardID: "b1", colName: "Doing"},
		{name: "with a limit", id: "c1", boardID: "b1", colName: "Doing", wipLimit: ptr(3)},
		{name: "limit of one", id: "c1", boardID: "b1", colName: "Doing", wipLimit: ptr(1)},
		{name: "limit of zero", id: "c1", boardID: "b1", colName: "Doing", wipLimit: ptr(0), wantErr: domain.ErrInvalid},
		{name: "negative limit", id: "c1", boardID: "b1", colName: "Doing", wipLimit: ptr(-1), wantErr: domain.ErrInvalid},
		{name: "empty name", id: "c1", boardID: "b1", colName: "", wantErr: domain.ErrInvalid},
		{name: "empty board id", id: "c1", boardID: "", colName: "Doing", wantErr: domain.ErrInvalid},
		{name: "empty id", id: "", boardID: "b1", colName: "Doing", wantErr: domain.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := domain.NewColumn(tt.id, tt.boardID, tt.colName, tt.wipLimit, 0, t0)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.BoardID != tt.boardID {
				t.Errorf("BoardID = %q, want %q", c.BoardID, tt.boardID)
			}
		})
	}
}

// CanAccept is the only real business rule in the project, so it gets the
// most thorough table in the domain.
func TestColumnCanAccept(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wipLimit  *int
		liveCards int
		wantErr   error
	}{
		{name: "no limit, empty", wipLimit: nil, liveCards: 0},
		{name: "no limit, a hundred cards", wipLimit: nil, liveCards: 100},
		{name: "under the limit", wipLimit: ptr(3), liveCards: 2},
		{name: "at the limit", wipLimit: ptr(3), liveCards: 3, wantErr: domain.ErrWIPExceeded},
		{name: "over the limit after it was lowered", wipLimit: ptr(2), liveCards: 5, wantErr: domain.ErrWIPExceeded},
		{name: "limit of one, empty", wipLimit: ptr(1), liveCards: 0},
		{name: "limit of one, full", wipLimit: ptr(1), liveCards: 1, wantErr: domain.ErrWIPExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := domain.NewColumn("c1", "b1", "Doing", tt.wipLimit, 0, t0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := c.CanAccept(tt.liveCards); !errors.Is(got, tt.wantErr) {
				t.Errorf("CanAccept(%d) = %v, want %v", tt.liveCards, got, tt.wantErr)
			}
		})
	}
}

func TestColumnApply(t *testing.T) {
	t.Parallel()

	later := t0.Add(5 * time.Second)

	t.Run("lowering the limit below the card count is allowed", func(t *testing.T) {
		t.Parallel()

		// Decided in 00-scope.md §8: forbidding this would mean emptying a
		// column before being allowed to configure it.
		c, _ := domain.NewColumn("c1", "b1", "Doing", ptr(5), 0, t0)
		changed, err := c.Apply(domain.ColumnPatch{WIPLimit: ptr(ptr(2)), SetWIPLimit: true}, later)
		if err != nil || !changed {
			t.Fatalf("Apply() = %v, %v; want true, nil", changed, err)
		}
		if *c.WIPLimit != 2 {
			t.Fatalf("WIPLimit = %d, want 2", *c.WIPLimit)
		}
		// The column now sits over limit, and stays there until cards leave.
		if err := c.CanAccept(4); !errors.Is(err, domain.ErrWIPExceeded) {
			t.Errorf("CanAccept(4) = %v, want %v", err, domain.ErrWIPExceeded)
		}
	})

	t.Run("clearing the limit", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewColumn("c1", "b1", "Doing", ptr(2), 0, t0)
		var none *int
		changed, err := c.Apply(domain.ColumnPatch{WIPLimit: &none, SetWIPLimit: true}, later)
		if err != nil || !changed {
			t.Fatalf("Apply() = %v, %v; want true, nil", changed, err)
		}
		if c.WIPLimit != nil {
			t.Fatalf("WIPLimit = %v, want nil", c.WIPLimit)
		}
		if err := c.CanAccept(999); err != nil {
			t.Errorf("CanAccept(999) with no limit = %v, want nil", err)
		}
	})

	t.Run("an unset field is left alone", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewColumn("c1", "b1", "Doing", ptr(2), 0, t0)
		changed, err := c.Apply(domain.ColumnPatch{Name: ptr("In progress")}, later)
		if err != nil || !changed {
			t.Fatalf("Apply() = %v, %v; want true, nil", changed, err)
		}
		if c.WIPLimit == nil || *c.WIPLimit != 2 {
			t.Errorf("WIPLimit = %v, want it untouched at 2", c.WIPLimit)
		}
		if !c.UpdatedAt.Equal(later) {
			t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, later)
		}
	})

	t.Run("a patch that changes nothing does not stamp updated_at", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewColumn("c1", "b1", "Doing", ptr(2), 0, t0)
		changed, err := c.Apply(domain.ColumnPatch{Name: ptr("Doing"), WIPLimit: ptr(ptr(2)), SetWIPLimit: true}, later)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed {
			t.Error("changed = true, want false")
		}
		if !c.UpdatedAt.Equal(t0) {
			t.Errorf("UpdatedAt = %v, want it untouched at %v", c.UpdatedAt, t0)
		}
	})

	t.Run("an invalid limit is refused and changes nothing", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewColumn("c1", "b1", "Doing", ptr(2), 0, t0)
		_, err := c.Apply(domain.ColumnPatch{WIPLimit: ptr(ptr(0)), SetWIPLimit: true}, later)
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
		if *c.WIPLimit != 2 {
			t.Errorf("WIPLimit = %d, want it untouched at 2", *c.WIPLimit)
		}
	})
}
