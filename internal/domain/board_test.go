package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

var t0 = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func TestNewBoard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		boardName string
		want      string // expected stored name
		wantErr   error
	}{
		{name: "trims the name", id: "b1", boardName: "  Work  ", want: "Work"},
		{name: "plain name", id: "b1", boardName: "Work", want: "Work"},
		{name: "empty name", id: "b1", boardName: "", wantErr: domain.ErrInvalid},
		{name: "whitespace-only name", id: "b1", boardName: "   ", wantErr: domain.ErrInvalid},
		{name: "empty id", id: "", boardName: "Work", wantErr: domain.ErrInvalid},
		{name: "name too long", id: "b1", boardName: strings.Repeat("a", 121), wantErr: domain.ErrInvalid},
		{name: "name at the limit", id: "b1", boardName: strings.Repeat("a", 120), want: strings.Repeat("a", 120)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, err := domain.NewBoard(tt.id, tt.boardName, t0)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b.Name != tt.want {
				t.Errorf("Name = %q, want %q", b.Name, tt.want)
			}
			if !b.CreatedAt.Equal(t0) || !b.UpdatedAt.Equal(t0) {
				t.Errorf("timestamps = %v/%v, want both %v", b.CreatedAt, b.UpdatedAt, t0)
			}
			if b.Deleted() {
				t.Error("a new board must not be deleted")
			}
		})
	}
}

func TestBoardRename(t *testing.T) {
	t.Parallel()

	later := t0.Add(5 * time.Second)

	t.Run("changes the name and stamps the update", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		changed, err := b.Rename("Personal", later)
		if err != nil || !changed {
			t.Fatalf("Rename() = %v, %v; want true, nil", changed, err)
		}
		if b.Name != "Personal" {
			t.Errorf("Name = %q, want %q", b.Name, "Personal")
		}
		if !b.UpdatedAt.Equal(later) {
			t.Errorf("UpdatedAt = %v, want %v", b.UpdatedAt, later)
		}
	})

	t.Run("the same name changes nothing", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		changed, err := b.Rename("  Work  ", later)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed {
			t.Error("changed = true, want false: renaming to the same name must not touch updated_at")
		}
		if !b.UpdatedAt.Equal(t0) {
			t.Errorf("UpdatedAt = %v, want it untouched at %v", b.UpdatedAt, t0)
		}
	})

	t.Run("an invalid name is refused", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		if _, err := b.Rename("  ", later); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
		if b.Name != "Work" {
			t.Errorf("a refused rename must leave the name alone, got %q", b.Name)
		}
	})
}

func TestFieldErrorCarriesTheField(t *testing.T) {
	t.Parallel()

	_, err := domain.NewBoard("b1", "", t0)

	var fe *domain.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a *FieldError", err)
	}
	if fe.Field != "name" {
		t.Errorf("Field = %q, want %q", fe.Field, "name")
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Error("a FieldError must also satisfy errors.Is(err, ErrInvalid)")
	}
}

func TestSetLabelNames(t *testing.T) {
	t.Parallel()

	later := t0.Add(5 * time.Second)

	t.Run("names a colour and stamps the update", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		changed, err := b.SetLabelNames(map[string]string{"blue": "Admin"}, later)
		if err != nil || !changed {
			t.Fatalf("SetLabelNames() = %v, %v; want true, nil", changed, err)
		}
		if b.LabelNames["blue"] != "Admin" {
			t.Errorf("LabelNames[blue] = %q, want Admin", b.LabelNames["blue"])
		}
		if !b.UpdatedAt.Equal(later) {
			t.Errorf("UpdatedAt = %v, want %v", b.UpdatedAt, later)
		}
	})

	t.Run("the same legend changes nothing", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		b.SetLabelNames(map[string]string{"blue": "Admin"}, later)

		changed, err := b.SetLabelNames(map[string]string{"blue": "Admin"}, later.Add(time.Second))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed {
			t.Error("changed = true, want false: an identical legend must not touch updated_at")
		}
		if !b.UpdatedAt.Equal(later) {
			t.Errorf("UpdatedAt = %v, want it untouched at %v", b.UpdatedAt, later)
		}
	})

	t.Run("an empty meaning un-names the colour", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		b.SetLabelNames(map[string]string{"blue": "Admin"}, later)

		changed, err := b.SetLabelNames(map[string]string{"blue": "  "}, later.Add(time.Second))
		if err != nil || !changed {
			t.Fatalf("SetLabelNames() = %v, %v; want true, nil", changed, err)
		}
		if _, named := b.LabelNames["blue"]; named {
			t.Error("blue is still named; an empty meaning must remove it")
		}
	})

	t.Run("replaces the whole legend rather than merging", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		b.SetLabelNames(map[string]string{"blue": "Admin", "red": "Urgent"}, later)

		b.SetLabelNames(map[string]string{"blue": "Admin"}, later.Add(time.Second))
		if _, named := b.LabelNames["red"]; named {
			t.Error("red survived a legend that did not mention it; replace must drop it")
		}
	})

	t.Run("an unknown colour is refused", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		if _, err := b.SetLabelNames(map[string]string{"turquoise": "x"}, later); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
	})

	t.Run("naming the empty label is refused", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		if _, err := b.SetLabelNames(map[string]string{domain.LabelNone: "x"}, later); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
	})

	t.Run("a meaning over the length limit is refused", func(t *testing.T) {
		t.Parallel()

		b, _ := domain.NewBoard("b1", "Work", t0)
		long := strings.Repeat("x", domain.MaxLabelNameLength+1)
		if _, err := b.SetLabelNames(map[string]string{"blue": long}, later); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("err = %v, want %v", err, domain.ErrInvalid)
		}
	})
}
