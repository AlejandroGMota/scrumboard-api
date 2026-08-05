package http_test

import (
	"net/http"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
	"github.com/google/go-cmp/cmp"
)

// The project's only real business rule, and the test that deserved to be
// written first.
func TestCreateCardRespectsTheWIPLimit(t *testing.T) {
	t.Parallel()

	t.Run("C6: a full column is a 409", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		limited := env.SeedColumn(t, board.ID, "Doing", ptr(2))
		env.SeedCards(t, limited, "one", "two")

		res := env.Client.POST("/api/columns/"+limited+"/cards", map[string]any{"title": "three"}).
			ExpectStatus(http.StatusConflict)
		if code := res.ErrorCode(); code != "wip_exceeded" {
			t.Errorf("code = %q, want wip_exceeded", code)
		}
		if diff := cmp.Diff([]string{"one", "two"}, env.CardTitlesIn(t, limited)); diff != "" {
			t.Errorf("the refused card must not be stored (-want +got):\n%s", diff)
		}
	})

	t.Run("a column with no limit never refuses", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		for i := 0; i < 12; i++ {
			env.SeedCard(t, board.Todo, "card")
		}
	})
}

func TestCreateCard(t *testing.T) {
	t.Parallel()

	t.Run("C1: appends to the column and reads back", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		var created struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Label       string `json:"label"`
			Position    int    `json:"position"`
		}
		env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{
			"title":       "  Write the tests  ",
			"description": "before the handler",
			"label":       "blue",
		}).ExpectStatus(http.StatusCreated).JSON(&created)

		if created.Title != "Write the tests" {
			t.Errorf("Title = %q, want it trimmed", created.Title)
		}
		if created.Position != 0 {
			t.Errorf("Position = %d, want 0", created.Position)
		}

		env.SeedCard(t, board.Todo, "second")
		if diff := cmp.Diff([]string{"Write the tests", "second"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("C4: an empty title is a 400, an empty description is fine", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "   "}).
			ExpectStatus(http.StatusBadRequest)
		env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "ok", "description": ""}).
			ExpectStatus(http.StatusCreated)

		if diff := cmp.Diff([]string{"ok"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("stored cards mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("C4: a label outside the palette is a 400", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		res := env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "x", "label": "chartreuse"}).
			ExpectStatus(http.StatusBadRequest)
		if code := res.ErrorCode(); code != "invalid" {
			t.Errorf("code = %q, want invalid", code)
		}
	})

	t.Run("C5: a nonexistent column is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		env.Client.POST("/api/columns/0192-nope/cards", map[string]any{"title": "x"}).
			ExpectStatus(http.StatusNotFound)
	})

	t.Run("C7: the same id and content answers 200 and does not duplicate", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		body := map[string]any{"id": "0192-card", "title": "Write the tests"}

		env.Client.POST("/api/columns/"+board.Todo+"/cards", body).ExpectStatus(http.StatusCreated)
		env.Client.POST("/api/columns/"+board.Todo+"/cards", body).ExpectStatus(http.StatusOK)

		if got := env.CardTitlesIn(t, board.Todo); len(got) != 1 {
			t.Errorf("%d cards, want 1", len(got))
		}
	})

	t.Run("C8: publishes card.created once", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		env.Events.Reset()

		env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{"title": "x"}).
			ExpectStatus(http.StatusCreated)

		if n := len(env.Events.OfType(domain.EventCardCreated)); n != 1 {
			t.Errorf("%d card.created events, want 1", n)
		}
	})
}

func TestUpdateCard(t *testing.T) {
	t.Parallel()

	t.Run("C1: patches only what it carries", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		var created struct {
			ID string `json:"id"`
		}
		env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{
			"title": "Old", "description": "keep me", "label": "blue",
		}).ExpectStatus(http.StatusCreated).JSON(&created)

		var updated struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Label       string `json:"label"`
		}
		env.Client.PATCH("/api/cards/"+created.ID, map[string]any{"title": "New"}).
			ExpectStatus(http.StatusOK).
			JSON(&updated)

		if updated.Title != "New" || updated.Description != "keep me" || updated.Label != "blue" {
			t.Errorf("card = %+v, want only the title changed", updated)
		}
	})

	t.Run("C4: an unknown label is a 400 and changes nothing", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "Title")

		env.Client.PATCH("/api/cards/"+card, map[string]any{"title": "New", "label": "chartreuse"}).
			ExpectStatus(http.StatusBadRequest)

		if diff := cmp.Diff([]string{"Title"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("a refused patch must apply nothing at all (-want +got):\n%s", diff)
		}
	})

	t.Run("C5: a deleted card is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "gone")

		env.Client.DELETE("/api/cards/" + card).ExpectStatus(http.StatusNoContent)
		env.Client.PATCH("/api/cards/"+card, map[string]any{"title": "back"}).
			ExpectStatus(http.StatusNotFound)
	})
}

func TestDeleteCard(t *testing.T) {
	t.Parallel()

	t.Run("C1: compacts the column it leaves behind", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		ids := env.SeedCards(t, board.Todo, "first", "second", "third")

		env.Client.DELETE("/api/cards/" + ids[1]).ExpectStatus(http.StatusNoContent)

		if diff := cmp.Diff([]string{"first", "third"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("order mismatch (-want +got):\n%s", diff)
		}
		env.AssertPositionsCompact(t, board.Todo)
	})

	t.Run("C7: deleting twice is still a 204", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "gone")

		env.Client.DELETE("/api/cards/" + card).ExpectStatus(http.StatusNoContent)
		env.Client.DELETE("/api/cards/" + card).ExpectStatus(http.StatusNoContent)
	})
}

// The most delicate endpoint in the API.
func TestMoveCardWithinAColumn(t *testing.T) {
	t.Parallel()

	// Both directions: the classic reordering bug is an off-by-one that only
	// shows up moving one way.
	tests := []struct {
		name  string
		card  int
		index int
		want  []string
	}{
		{name: "first down one", card: 0, index: 1, want: []string{"b", "a", "c"}},
		{name: "first to the end", card: 0, index: 2, want: []string{"b", "c", "a"}},
		{name: "last up one", card: 2, index: 1, want: []string{"a", "c", "b"}},
		{name: "last to the front", card: 2, index: 0, want: []string{"c", "a", "b"}},
		{name: "past the end clamps", card: 0, index: 99, want: []string{"b", "c", "a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := testutil.NewEnv(t)
			board := env.SeedBoard(t)
			ids := env.SeedCards(t, board.Todo, "a", "b", "c")

			env.Client.PUT("/api/cards/"+ids[tt.card]+"/move", map[string]any{
				"to_column_id": board.Todo, "to_index": tt.index,
			}).ExpectStatus(http.StatusOK)

			if diff := cmp.Diff(tt.want, env.CardTitlesIn(t, board.Todo)); diff != "" {
				t.Errorf("order mismatch (-want +got):\n%s", diff)
			}
			env.AssertPositionsCompact(t, board.Todo)
		})
	}
}

func TestMoveCardAcrossColumns(t *testing.T) {
	t.Parallel()

	t.Run("C1: the source compacts and the target opens a slot", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		todo := env.SeedCards(t, board.Todo, "a", "b", "c")
		env.SeedCards(t, board.Doing, "x", "y")

		var moved struct {
			ColumnID string `json:"column_id"`
			Position int    `json:"position"`
		}
		env.Client.PUT("/api/cards/"+todo[1]+"/move", map[string]any{
			"to_column_id": board.Doing, "to_index": 1,
		}).ExpectStatus(http.StatusOK).JSON(&moved)

		if moved.ColumnID != board.Doing || moved.Position != 1 {
			t.Errorf("card at %s:%d, want %s:1", moved.ColumnID, moved.Position, board.Doing)
		}
		if diff := cmp.Diff([]string{"a", "c"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("source mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"x", "b", "y"}, env.CardTitlesIn(t, board.Doing)); diff != "" {
			t.Errorf("target mismatch (-want +got):\n%s", diff)
		}
		env.AssertPositionsCompact(t, board.Todo)
		env.AssertPositionsCompact(t, board.Doing)
	})

	// A 409 that also left the card half-moved would be worse than not having
	// the rule at all.
	t.Run("C6: a full target is a 409 and the card stays exactly where it was", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		todo := env.SeedCards(t, board.Todo, "a", "b", "c")
		full := env.SeedColumn(t, board.ID, "Full", ptr(2))
		env.SeedCards(t, full, "x", "y")
		env.Events.Reset()

		res := env.Client.PUT("/api/cards/"+todo[1]+"/move", map[string]any{
			"to_column_id": full, "to_index": 0,
		}).ExpectStatus(http.StatusConflict)

		if code := res.ErrorCode(); code != "wip_exceeded" {
			t.Errorf("code = %q, want wip_exceeded", code)
		}
		if diff := cmp.Diff([]string{"a", "b", "c"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("the source must be untouched (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"x", "y"}, env.CardTitlesIn(t, full)); diff != "" {
			t.Errorf("the target must be untouched (-want +got):\n%s", diff)
		}
		if n := len(env.Events.Events()); n != 0 {
			t.Errorf("%d events published, want 0 on a refused move", n)
		}
	})

	t.Run("C7: moving to the position it already occupies is a 200 with nothing changed", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		ids := env.SeedCards(t, board.Todo, "a", "b", "c")
		env.Events.Reset()

		env.Client.PUT("/api/cards/"+ids[1]+"/move", map[string]any{
			"to_column_id": board.Todo, "to_index": 1,
		}).ExpectStatus(http.StatusOK)

		if diff := cmp.Diff([]string{"a", "b", "c"}, env.CardTitlesIn(t, board.Todo)); diff != "" {
			t.Errorf("order mismatch (-want +got):\n%s", diff)
		}
		if n := len(env.Events.Events()); n != 0 {
			t.Errorf("%d events published, want 0", n)
		}
	})

	t.Run("C4: a negative index is a 400", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "a")

		env.Client.PUT("/api/cards/"+card+"/move", map[string]any{
			"to_column_id": board.Todo, "to_index": -1,
		}).ExpectStatus(http.StatusBadRequest)
	})

	t.Run("C4: a column on another board is a 400", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		other := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "a")

		res := env.Client.PUT("/api/cards/"+card+"/move", map[string]any{
			"to_column_id": other.Todo, "to_index": 0,
		}).ExpectStatus(http.StatusBadRequest)
		if code := res.ErrorCode(); code != "invalid" {
			t.Errorf("code = %q, want invalid: a card must not leave its board", code)
		}
	})

	t.Run("C5: a nonexistent card is a 404", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)

		env.Client.PUT("/api/cards/0192-nope/move", map[string]any{
			"to_column_id": board.Todo, "to_index": 0,
		}).ExpectStatus(http.StatusNotFound)
	})

	t.Run("C8: publishes card.moved with both columns", func(t *testing.T) {
		t.Parallel()

		env := testutil.NewEnv(t)
		board := env.SeedBoard(t)
		card := env.SeedCard(t, board.Todo, "a")
		env.Events.Reset()

		env.Client.WithOpID("op-9").PUT("/api/cards/"+card+"/move", map[string]any{
			"to_column_id": board.Doing, "to_index": 0,
		}).ExpectStatus(http.StatusOK)

		events := env.Events.OfType(domain.EventCardMoved)
		if len(events) != 1 {
			t.Fatalf("%d card.moved events, want 1", len(events))
		}
		payload, ok := events[0].Payload.(domain.CardMovedPayload)
		if !ok {
			t.Fatalf("payload is %T, want CardMovedPayload", events[0].Payload)
		}
		if payload.FromColumnID != board.Todo || payload.ToColumnID != board.Doing {
			t.Errorf("from/to = %s/%s, want %s/%s", payload.FromColumnID, payload.ToColumnID, board.Todo, board.Doing)
		}
		if events[0].OpID != "op-9" {
			t.Errorf("OpID = %q, want op-9", events[0].OpID)
		}
	})
}

func TestCardResponseShape(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	board := env.SeedBoard(t)

	res := env.Client.POST("/api/columns/"+board.Todo+"/cards", map[string]any{
		"title": "Write the tests", "description": "before the handler", "label": "blue",
	}).ExpectStatus(http.StatusCreated)

	testutil.AssertGolden(t, "card_created", res.Body)
}
