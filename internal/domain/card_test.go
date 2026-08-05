package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/google/go-cmp/cmp"
)

func TestNewCard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		title     string
		desc      string
		label     string
		wantTitle string
		wantErr   error
	}{
		{name: "happy path", title: "Write the tests", desc: "before the handler", label: "blue", wantTitle: "Write the tests"},
		{name: "trims the title", title: "  Write  ", wantTitle: "Write"},
		{name: "empty description is valid", title: "Write", desc: "", wantTitle: "Write"},
		{name: "no label is valid", title: "Write", label: "", wantTitle: "Write"},
		{name: "empty title", title: "", wantErr: domain.ErrInvalid},
		{name: "whitespace-only title", title: "   \t ", wantErr: domain.ErrInvalid},
		{name: "title too long", title: strings.Repeat("a", 201), wantErr: domain.ErrInvalid},
		{name: "description too long", title: "Write", desc: strings.Repeat("a", 4001), wantErr: domain.ErrInvalid},
		{name: "unknown label", title: "Write", label: "chartreuse", wantErr: domain.ErrInvalid},
		{name: "label with the wrong case", title: "Write", label: "Blue", wantErr: domain.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := domain.NewCard("k1", "c1", tt.title, tt.desc, tt.label, 0, t0)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", c.Title, tt.wantTitle)
			}
		})
	}
}

func TestValidLabelCoversThePalette(t *testing.T) {
	t.Parallel()

	if len(domain.Labels) != 6 {
		t.Fatalf("the palette has %d colours, want the 6 fixed ones (00-scope.md §11)", len(domain.Labels))
	}
	for _, l := range domain.Labels {
		if err := domain.ValidLabel(l); err != nil {
			t.Errorf("ValidLabel(%q) = %v, want nil", l, err)
		}
	}
	if err := domain.ValidLabel(domain.LabelNone); err != nil {
		t.Errorf("ValidLabel(none) = %v, want nil", err)
	}
	if err := domain.ValidLabel("#00ff00"); !errors.Is(err, domain.ErrInvalid) {
		t.Error("free-text hex must be refused, or the palette is not a palette")
	}
}

func TestCardApply(t *testing.T) {
	t.Parallel()

	later := t0.Add(5 * time.Second)

	t.Run("patches only what it carries", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewCard("k1", "c1", "Old", "desc", "blue", 0, t0)
		changed, err := c.Apply(domain.CardPatch{Title: ptr("New")}, later)
		if err != nil || !changed {
			t.Fatalf("Apply() = %v, %v; want true, nil", changed, err)
		}
		if c.Title != "New" || c.Description != "desc" || c.Label != "blue" {
			t.Errorf("card = %+v, want only the title changed", c)
		}
		if !c.UpdatedAt.Equal(later) {
			t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, later)
		}
	})

	t.Run("clearing the label", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewCard("k1", "c1", "Card", "", "blue", 0, t0)
		changed, _ := c.Apply(domain.CardPatch{Label: ptr("")}, later)
		if !changed || c.Label != domain.LabelNone {
			t.Errorf("Label = %q, want it cleared", c.Label)
		}
	})

	t.Run("a no-op patch does not stamp updated_at", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewCard("k1", "c1", "Card", "", "blue", 0, t0)
		changed, err := c.Apply(domain.CardPatch{Title: ptr("Card"), Label: ptr("blue")}, later)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed || !c.UpdatedAt.Equal(t0) {
			t.Errorf("changed = %v, UpdatedAt = %v; want false and %v", changed, c.UpdatedAt, t0)
		}
	})

	t.Run("a refused patch leaves the card intact", func(t *testing.T) {
		t.Parallel()

		c, _ := domain.NewCard("k1", "c1", "Card", "", "blue", 0, t0)
		_, err := c.Apply(domain.CardPatch{Title: ptr("New"), Label: ptr("chartreuse")}, later)
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
		if c.Title != "Card" {
			t.Errorf("Title = %q: a patch that fails validation must apply nothing at all", c.Title)
		}
	})
}

// The reordering maths. Table-driven and exhaustive on purpose: the classic
// bug here is an off-by-one that only appears moving in one direction
// (02-testing.md §4.4).
func TestMoveWithin(t *testing.T) {
	t.Parallel()

	base := []string{"a", "b", "c", "d"}

	tests := []struct {
		name string
		from int
		to   int
		want []string
	}{
		{name: "down by one", from: 0, to: 1, want: []string{"b", "a", "c", "d"}},
		{name: "down to the end", from: 0, to: 3, want: []string{"b", "c", "d", "a"}},
		{name: "up by one", from: 3, to: 2, want: []string{"a", "b", "d", "c"}},
		{name: "up to the front", from: 3, to: 0, want: []string{"d", "a", "b", "c"}},
		{name: "middle down", from: 1, to: 2, want: []string{"a", "c", "b", "d"}},
		{name: "middle up", from: 2, to: 1, want: []string{"a", "c", "b", "d"}},
		{name: "to its own position", from: 2, to: 2, want: []string{"a", "b", "c", "d"}},
		{name: "past the end clamps", from: 0, to: 99, want: []string{"b", "c", "d", "a"}},
		{name: "negative clamps to the front", from: 2, to: -5, want: []string{"c", "a", "b", "d"}},
		{name: "out-of-range source is a no-op", from: 9, to: 0, want: []string{"a", "b", "c", "d"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items := append([]string(nil), base...)
			got := domain.MoveWithin(items, tt.from, tt.to)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("MoveWithin(%v, %d, %d) mismatch (-want +got):\n%s", base, tt.from, tt.to, diff)
			}
		})
	}
}

func TestInsertAtAndRemoveAt(t *testing.T) {
	t.Parallel()

	t.Run("insert", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			to   int
			want []string
		}{
			{name: "at the front", to: 0, want: []string{"x", "a", "b", "c"}},
			{name: "in the middle", to: 2, want: []string{"a", "b", "x", "c"}},
			{name: "at the end", to: 3, want: []string{"a", "b", "c", "x"}},
			{name: "past the end clamps", to: 99, want: []string{"a", "b", "c", "x"}},
			{name: "negative clamps", to: -1, want: []string{"x", "a", "b", "c"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got := domain.InsertAt([]string{"a", "b", "c"}, "x", tt.to)
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("InsertAt(..., %d) mismatch (-want +got):\n%s", tt.to, diff)
				}
			})
		}
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		got := domain.RemoveAt([]string{"a", "b", "c"}, 1)
		if diff := cmp.Diff([]string{"a", "c"}, got); diff != "" {
			t.Errorf("RemoveAt mismatch (-want +got):\n%s", diff)
		}
		// Out of range must not panic; the source column simply keeps its order.
		if diff := cmp.Diff([]string{"a", "b", "c"}, domain.RemoveAt([]string{"a", "b", "c"}, 7)); diff != "" {
			t.Errorf("RemoveAt out of range mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestClampIndex(t *testing.T) {
	t.Parallel()

	tests := []struct{ idx, n, want int }{
		{idx: -1, n: 3, want: 0},
		{idx: 0, n: 3, want: 0},
		{idx: 2, n: 3, want: 2},
		{idx: 3, n: 3, want: 3},
		{idx: 99, n: 3, want: 3},
	}
	for _, tt := range tests {
		if got := domain.ClampIndex(tt.idx, tt.n); got != tt.want {
			t.Errorf("ClampIndex(%d, %d) = %d, want %d", tt.idx, tt.n, got, tt.want)
		}
	}
}
