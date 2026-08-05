package http_test

import (
	"net/http"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
	"github.com/google/go-cmp/cmp"
)

// C1 for the list endpoint, plus the classic Go trap: a nil slice serialises
// to null and breaks the frontend.
func TestListBoardsReturnsAnEmptyArrayNotNull(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	res := env.Client.GET("/api/boards").ExpectStatus(http.StatusOK)

	if got := string(res.Body); got != "[]\n" {
		t.Errorf("body = %q, want %q", got, "[]\n")
	}
}

func TestCreateBoard(t *testing.T) {
	t.Parallel()

	t.Run("C1: creates and reads back what was written", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		var created struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		env.Client.POST("/api/boards", map[string]any{"name": "  Work  "}).
			ExpectStatus(http.StatusCreated).
			JSON(&created)

		if created.Name != "Work" {
			t.Errorf("Name = %q, want it trimmed to %q", created.Name, "Work")
		}

		var read struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Columns []any  `json:"columns"`
		}
		env.Client.GET("/api/boards/" + created.ID).
			ExpectStatus(http.StatusOK).
			JSON(&read)

		if read.ID != created.ID || read.Name != "Work" {
			t.Errorf("read back %+v, want the board that was written", read)
		}
	})

	t.Run("C4: an empty name is a 400 and stores nothing", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		res := env.Client.POST("/api/boards", map[string]any{"name": "   "}).
			ExpectStatus(http.StatusBadRequest)

		if code := res.ErrorCode(); code != "invalid" {
			t.Errorf("code = %q, want invalid", code)
		}
		if n := env.CountRows(t, "boards"); n != 0 {
			t.Errorf("%d boards stored, want 0", n)
		}
	})

	t.Run("C4: a malformed body is a 400", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)

		tests := []struct {
			name string
			body string
		}{
			{name: "not json", body: `{`},
			{name: "empty", body: ``},
			{name: "an unknown field", body: `{"name":"Work","colour":"blue"}`},
			{name: "the wrong type", body: `{"name": 42}`},
			{name: "two documents", body: `{"name":"Work"}{"name":"Other"}`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				env.Client.POST("/api/boards", tt.body).ExpectStatus(http.StatusBadRequest)
			})
		}
	})

	t.Run("C7: the same id and content answers 200 and does not duplicate", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		body := map[string]any{"id": "0192-fixed-id", "name": "Work"}

		env.Client.POST("/api/boards", body).ExpectStatus(http.StatusCreated)
		env.Client.POST("/api/boards", body).ExpectStatus(http.StatusOK)

		if n := env.CountRows(t, "boards"); n != 1 {
			t.Errorf("%d boards stored, want 1", n)
		}
	})

	t.Run("C7: the same id with different content is a 409", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.POST("/api/boards", map[string]any{"id": "0192-fixed-id", "name": "Work"}).
			ExpectStatus(http.StatusCreated)

		res := env.Client.POST("/api/boards", map[string]any{"id": "0192-fixed-id", "name": "Personal"}).
			ExpectStatus(http.StatusConflict)
		if code := res.ErrorCode(); code != "conflict" {
			t.Errorf("code = %q, want conflict", code)
		}
	})

	t.Run("C8: publishes board.created once, with the op id", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.WithOpID("op-42").POST("/api/boards", map[string]any{"name": "Work"}).
			ExpectStatus(http.StatusCreated)

		events := env.Events.OfType(domain.EventBoardCreated)
		if len(events) != 1 {
			t.Fatalf("%d board.created events, want exactly 1", len(events))
		}
		if events[0].OpID != "op-42" {
			t.Errorf("OpID = %q, want op-42: the originating device filters its own echo by this", events[0].OpID)
		}
	})
}

// The call that paints the screen. The nesting and the ordering are part of
// the contract, not an implementation detail.
func TestGetBoardNestsColumnsAndCardsInOrder(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	env.SeedCards(t, board.Todo, "first", "second", "third")
	env.SeedCard(t, board.Doing, "elsewhere")

	var got struct {
		Columns []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Cards []struct {
				Title    string `json:"title"`
				Position int    `json:"position"`
			} `json:"cards"`
		} `json:"columns"`
	}
	env.Client.GET("/api/boards/" + board.ID).ExpectStatus(http.StatusOK).JSON(&got)

	if len(got.Columns) != 3 {
		t.Fatalf("%d columns, want 3", len(got.Columns))
	}
	names := []string{got.Columns[0].Name, got.Columns[1].Name, got.Columns[2].Name}
	if diff := cmp.Diff([]string{"Todo", "Doing", "Done"}, names); diff != "" {
		t.Errorf("column order mismatch (-want +got):\n%s", diff)
	}

	titles := make([]string, 0, 3)
	for _, c := range got.Columns[0].Cards {
		titles = append(titles, c.Title)
	}
	if diff := cmp.Diff([]string{"first", "second", "third"}, titles); diff != "" {
		t.Errorf("card order mismatch (-want +got):\n%s", diff)
	}
	if len(got.Columns[2].Cards) != 0 {
		t.Errorf("%d cards in the empty column, want 0", len(got.Columns[2].Cards))
	}
}

func TestGetBoard(t *testing.T) {
	t.Parallel()

	t.Run("C5: a well-formed id that does not exist is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		res := env.Client.GET("/api/boards/0192-nope").ExpectStatus(http.StatusNotFound)
		if code := res.ErrorCode(); code != "not_found" {
			t.Errorf("code = %q, want not_found", code)
		}
	})

	t.Run("a deleted board is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.Client.DELETE("/api/boards/" + board.ID).ExpectStatus(http.StatusNoContent)

		env.Client.GET("/api/boards/" + board.ID).ExpectStatus(http.StatusNotFound)
	})
}

func TestUpdateBoard(t *testing.T) {
	t.Parallel()

	t.Run("C1: renames and reads back", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		env.Client.PATCH("/api/boards/"+board.ID, map[string]any{"name": "Personal"}).
			ExpectStatus(http.StatusOK)

		var read struct {
			Name string `json:"name"`
		}
		env.Client.GET("/api/boards/" + board.ID).JSON(&read)
		if read.Name != "Personal" {
			t.Errorf("Name = %q, want Personal", read.Name)
		}
	})

	t.Run("C5: a nonexistent board is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.PATCH("/api/boards/0192-nope", map[string]any{"name": "x"}).
			ExpectStatus(http.StatusNotFound)
	})

	t.Run("C7: renaming to the same name is a 200 with no event", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.Events.Reset()

		env.Client.PATCH("/api/boards/"+board.ID, map[string]any{"name": "Work"}).
			ExpectStatus(http.StatusOK)

		if n := len(env.Events.Events()); n != 0 {
			t.Errorf("%d events published, want 0: a no-op update must not wake the other devices", n)
		}
	})
}

func TestDeleteBoard(t *testing.T) {
	t.Parallel()

	t.Run("C1: soft-deletes and cascades to columns and cards", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.SeedCard(t, board.Todo, "a card")

		env.Client.DELETE("/api/boards/" + board.ID).ExpectStatus(http.StatusNoContent)

		// The rows are still there: deletes are soft (00-scope.md §5).
		if n := env.CountRows(t, "cards"); n != 1 {
			t.Errorf("%d card rows, want the row to survive a soft delete", n)
		}
		var live int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM cards WHERE deleted_at IS NULL`).Scan(&live); err != nil {
			t.Fatalf("counting live cards: %v", err)
		}
		if live != 0 {
			t.Errorf("%d live cards, want 0: the cascade did not reach them", live)
		}
	})

	t.Run("C7: deleting twice is still a 204", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		env.Client.DELETE("/api/boards/" + board.ID).ExpectStatus(http.StatusNoContent)
		env.Client.DELETE("/api/boards/" + board.ID).ExpectStatus(http.StatusNoContent)
	})

	t.Run("C5: deleting something that never existed is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.DELETE("/api/boards/0192-nope").ExpectStatus(http.StatusNotFound)
	})
}

// The golden file for C1: it catches a renamed field or a dropped json tag,
// which no value assertion would notice but the frontend certainly would.
func TestBoardResponseShape(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)
	env.SeedCard(t, board.Todo, "Write the tests")

	// Ids and timestamps are deterministic here because the clock and the id
	// generator are fakes, so the golden file needs no regular expressions.
	res := env.Client.GET("/api/boards/" + board.ID).ExpectStatus(http.StatusOK)
	testutil.AssertGolden(t, "board_with_children", res.Body)
}
