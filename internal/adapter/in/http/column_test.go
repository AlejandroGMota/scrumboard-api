package http_test

import (
	"net/http"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
	"github.com/google/go-cmp/cmp"
)

func TestCreateColumn(t *testing.T) {
	t.Parallel()

	t.Run("C1: places the new column last", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		var created struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
			WIPLimit *int   `json:"wip_limit"`
		}
		env.Client.POST("/api/boards/"+board.ID+"/columns", map[string]any{"name": "Blocked"}).
			ExpectStatus(http.StatusCreated).
			JSON(&created)

		if created.Position != 3 {
			t.Errorf("Position = %d, want 3 (max + 1)", created.Position)
		}
		if created.WIPLimit != nil {
			t.Errorf("WIPLimit = %v, want null", *created.WIPLimit)
		}
	})

	t.Run("C1: with a wip limit", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		var created struct {
			WIPLimit *int `json:"wip_limit"`
		}
		env.Client.POST("/api/boards/"+board.ID+"/columns", map[string]any{"name": "Blocked", "wip_limit": 3}).
			ExpectStatus(http.StatusCreated).
			JSON(&created)

		if created.WIPLimit == nil || *created.WIPLimit != 3 {
			t.Errorf("WIPLimit = %v, want 3", created.WIPLimit)
		}
	})

	t.Run("C4: an invalid wip limit is a 400", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		res := env.Client.POST("/api/boards/"+board.ID+"/columns", map[string]any{"name": "Blocked", "wip_limit": 0}).
			ExpectStatus(http.StatusBadRequest)
		if code := res.ErrorCode(); code != "invalid" {
			t.Errorf("code = %q, want invalid", code)
		}
	})

	t.Run("C5: a nonexistent board is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.POST("/api/boards/0192-nope/columns", map[string]any{"name": "Todo"}).
			ExpectStatus(http.StatusNotFound)
	})

	t.Run("C8: publishes column.created", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.Events.Reset()

		env.Client.POST("/api/boards/"+board.ID+"/columns", map[string]any{"name": "Blocked"}).
			ExpectStatus(http.StatusCreated)

		if n := len(env.Events.OfType(domain.EventColumnCreated)); n != 1 {
			t.Errorf("%d column.created events, want 1", n)
		}
	})
}

func TestUpdateColumn(t *testing.T) {
	t.Parallel()

	// Decided in 00-scope.md §8: forbidding this would mean emptying a column
	// before being allowed to configure it.
	t.Run("C1: the wip limit may drop below the current card count", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.SeedCards(t, board.Todo, "one", "two", "three")

		env.Client.PATCH("/api/columns/"+board.Todo, map[string]any{"wip_limit": 1}).
			ExpectStatus(http.StatusOK)

		// The column sits over limit …
		if got := len(env.CardTitlesIn(t, board.Todo)); got != 3 {
			t.Errorf("%d cards, want the 3 that were already there", got)
		}
		// … and the next insertion is what gets refused.
		res := env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "four"}).
			ExpectStatus(http.StatusConflict)
		if code := res.ErrorCode(); code != "wip_exceeded" {
			t.Errorf("code = %q, want wip_exceeded", code)
		}
	})

	t.Run("C1: null clears the limit", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		limited := env.SeedColumn(t, board.ID, "Limited", ptr(1))

		var updated struct {
			WIPLimit *int `json:"wip_limit"`
		}
		env.Client.PATCH("/api/columns/"+limited, `{"wip_limit": null}`).
			ExpectStatus(http.StatusOK).
			JSON(&updated)

		if updated.WIPLimit != nil {
			t.Fatalf("WIPLimit = %v, want null", *updated.WIPLimit)
		}
		// Now unlimited.
		env.SeedCards(t, limited, "one", "two", "three")
	})

	t.Run("C1: an absent field is left alone", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		limited := env.SeedColumn(t, board.ID, "Limited", ptr(2))

		var updated struct {
			Name     string `json:"name"`
			WIPLimit *int   `json:"wip_limit"`
		}
		env.Client.PATCH("/api/columns/"+limited, `{"name": "Renamed"}`).
			ExpectStatus(http.StatusOK).
			JSON(&updated)

		if updated.Name != "Renamed" {
			t.Errorf("Name = %q, want Renamed", updated.Name)
		}
		if updated.WIPLimit == nil || *updated.WIPLimit != 2 {
			t.Errorf("WIPLimit = %v, want it untouched at 2: absent must not mean null", updated.WIPLimit)
		}
	})

	t.Run("C5: a nonexistent column is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.PATCH("/api/columns/0192-nope", map[string]any{"name": "x"}).
			ExpectStatus(http.StatusNotFound)
	})
}

func TestDeleteColumn(t *testing.T) {
	t.Parallel()

	// Cascading a delete onto cards is the kind of action one regrets at
	// eleven at night (00-scope.md §8).
	t.Run("C6: a column with cards is a 409", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.SeedCard(t, board.Todo, "still here")

		res := env.Client.DELETE("/api/columns/" + board.Todo).ExpectStatus(http.StatusConflict)
		if code := res.ErrorCode(); code != "column_not_empty" {
			t.Errorf("code = %q, want column_not_empty", code)
		}
		if got := env.ColumnOrder(t, board.ID); len(got) != 3 {
			t.Errorf("%d columns, want the refused delete to have changed nothing", len(got))
		}
	})

	t.Run("C1: a column whose cards are all deleted can go", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "gone soon")

		env.Client.DELETE("/api/cards/" + card).ExpectStatus(http.StatusNoContent)
		env.Client.DELETE("/api/columns/" + board.Todo).ExpectStatus(http.StatusNoContent)

		if got := env.ColumnOrder(t, board.ID); len(got) != 2 {
			t.Errorf("%d live columns, want 2", len(got))
		}
	})

	t.Run("C7: deleting twice is still a 204", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		env.Client.DELETE("/api/columns/" + board.Done).ExpectStatus(http.StatusNoContent)
		env.Client.DELETE("/api/columns/" + board.Done).ExpectStatus(http.StatusNoContent)
	})

	t.Run("the survivors compact", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		env.Client.DELETE("/api/columns/" + board.Doing).ExpectStatus(http.StatusNoContent)

		if diff := cmp.Diff([]string{board.Todo, board.Done}, env.ColumnOrder(t, board.ID)); diff != "" {
			t.Errorf("column order mismatch (-want +got):\n%s", diff)
		}
		var positions []int
		rows, err := env.DB.Query(`SELECT position FROM columns WHERE board_id = ? AND deleted_at IS NULL ORDER BY position`, board.ID)
		if err != nil {
			t.Fatalf("reading positions: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p int
			if err := rows.Scan(&p); err != nil {
				t.Fatalf("scanning: %v", err)
			}
			positions = append(positions, p)
		}
		if diff := cmp.Diff([]int{0, 1}, positions); diff != "" {
			t.Errorf("positions mismatch (-want +got):\n%s", diff)
		}
	})
}

// The easiest endpoint in the API to leave half-finished, and the one that
// corrupts ordering when it is.
func TestReorderColumns(t *testing.T) {
	t.Parallel()

	t.Run("C1: reverses the order, asserted from the database", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		reversed := []string{board.Done, board.Doing, board.Todo}

		env.Client.PUT("/api/boards/"+board.ID+"/columns/order", map[string]any{"ids": reversed}).
			ExpectStatus(http.StatusOK)

		if diff := cmp.Diff(reversed, env.ColumnOrder(t, board.ID)); diff != "" {
			t.Errorf("column order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("C4: a malformed list is a 400 and changes nothing", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			ids  func(b testutil.Board, foreign string) []string
		}{
			{name: "incomplete", ids: func(b testutil.Board, _ string) []string { return []string{b.Todo, b.Doing} }},
			{name: "with a duplicate", ids: func(b testutil.Board, _ string) []string { return []string{b.Todo, b.Doing, b.Doing} }},
			{name: "with an unknown id", ids: func(b testutil.Board, _ string) []string { return []string{b.Todo, b.Doing, "0192-nope"} }},
			{name: "from another board", ids: func(b testutil.Board, foreign string) []string { return []string{b.Todo, b.Doing, foreign} }},
			{name: "empty", ids: func(testutil.Board, string) []string { return []string{} }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				env := testutil.NewEnv(t)
				board := env.SeedBoard(t)
				other := env.SeedBoard(t)
				before := env.ColumnOrder(t, board.ID)

				env.Client.PUT("/api/boards/"+board.ID+"/columns/order", map[string]any{"ids": tt.ids(board, other.Todo)}).
					ExpectStatus(http.StatusBadRequest)

				if diff := cmp.Diff(before, env.ColumnOrder(t, board.ID)); diff != "" {
					t.Errorf("a refused reorder must change nothing (-before +after):\n%s", diff)
				}
			})
		}
	})

	t.Run("C5: a nonexistent board is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.PUT("/api/boards/0192-nope/columns/order", map[string]any{"ids": []string{}}).
			ExpectStatus(http.StatusNotFound)
	})

	t.Run("C7: reordering into the order it already has is a 200", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		same := []string{board.Todo, board.Doing, board.Done}

		env.Client.PUT("/api/boards/"+board.ID+"/columns/order", map[string]any{"ids": same}).
			ExpectStatus(http.StatusOK)

		if diff := cmp.Diff(same, env.ColumnOrder(t, board.ID)); diff != "" {
			t.Errorf("column order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("C8: publishes columns.ordered with the new order", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.Events.Reset()
		reversed := []string{board.Done, board.Doing, board.Todo}

		env.Client.PUT("/api/boards/"+board.ID+"/columns/order", map[string]any{"ids": reversed}).
			ExpectStatus(http.StatusOK)

		events := env.Events.OfType(domain.EventColumnsOrdered)
		if len(events) != 1 {
			t.Fatalf("%d columns.ordered events, want 1", len(events))
		}
		payload, ok := events[0].Payload.(domain.ColumnsOrderedPayload)
		if !ok {
			t.Fatalf("payload is %T, want ColumnsOrderedPayload", events[0].Payload)
		}
		if diff := cmp.Diff(reversed, payload.IDs); diff != "" {
			t.Errorf("event ids mismatch (-want +got):\n%s", diff)
		}
	})
}

func ptr[T any](v T) *T { return &v }
