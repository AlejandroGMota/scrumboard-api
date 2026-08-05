package testutil

import (
	"fmt"
	"net/http"
	"testing"
)

// Board is the sample board most endpoint tests start from: three columns,
// created through the API rather than inserted behind its back, so the
// fixture exercises the same code the tests are about.
type Board struct {
	ID      string
	Todo    string
	Doing   string
	Done    string
	Columns []string
}

// SeedBoard creates a board named "Work" with Todo, Doing and Done.
func (e *Env) SeedBoard(t *testing.T) Board {
	t.Helper()

	var board struct {
		ID string `json:"id"`
	}
	e.Client.POST("/api/boards", map[string]any{"name": "Work"}).
		ExpectStatus(http.StatusCreated).
		JSON(&board)

	b := Board{ID: board.ID}
	for _, name := range []string{"Todo", "Doing", "Done"} {
		id := e.SeedColumn(t, board.ID, name, nil)
		b.Columns = append(b.Columns, id)
	}
	b.Todo, b.Doing, b.Done = b.Columns[0], b.Columns[1], b.Columns[2]
	return b
}

// SeedColumn adds a column to a board and returns its id.
func (e *Env) SeedColumn(t *testing.T, boardID, name string, wipLimit *int) string {
	t.Helper()

	body := map[string]any{"name": name}
	if wipLimit != nil {
		body["wip_limit"] = *wipLimit
	}

	var column struct {
		ID string `json:"id"`
	}
	e.Client.POST("/api/boards/"+boardID+"/columns", body).
		ExpectStatus(http.StatusCreated).
		JSON(&column)
	return column.ID
}

// SeedCard adds a card to a column and returns its id.
func (e *Env) SeedCard(t *testing.T, columnID, title string) string {
	t.Helper()

	var card struct {
		ID string `json:"id"`
	}
	e.Client.POST("/api/columns/"+columnID+"/cards", map[string]any{"title": title}).
		ExpectStatus(http.StatusCreated).
		JSON(&card)
	return card.ID
}

// SeedCards adds several cards in order and returns their ids.
func (e *Env) SeedCards(t *testing.T, columnID string, titles ...string) []string {
	t.Helper()

	ids := make([]string, 0, len(titles))
	for _, title := range titles {
		ids = append(ids, e.SeedCard(t, columnID, title))
	}
	return ids
}

// CardTitlesIn reads a column's cards straight from the database, in position
// order. Assertions about ordering go through here rather than through the API
// on purpose: the response could be right while the stored positions are not
// (02-testing.md §7.1).
func (e *Env) CardTitlesIn(t *testing.T, columnID string) []string {
	t.Helper()

	rows, err := e.DB.Query(
		`SELECT title FROM cards WHERE column_id = ? AND deleted_at IS NULL ORDER BY position`, columnID)
	if err != nil {
		t.Fatalf("reading card titles: %v", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out = append(out, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading card titles: %v", err)
	}
	return out
}

// AssertPositionsCompact pins the invariant that survives every move: the live
// cards of a column occupy positions 0..n-1, with no gaps and no ties. Read
// from the database, never from the response.
func (e *Env) AssertPositionsCompact(t *testing.T, columnID string) {
	t.Helper()

	rows, err := e.DB.Query(
		`SELECT id, position FROM cards WHERE column_id = ? AND deleted_at IS NULL ORDER BY position, id`, columnID)
	if err != nil {
		t.Fatalf("reading positions: %v", err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		var (
			id  string
			pos int
		)
		if err := rows.Scan(&id, &pos); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		if pos != i {
			t.Errorf("column %s: card %s is at position %d, want %d (positions must be 0..n-1, no gaps, no ties)", columnID, id, pos, i)
		}
		i++
	}
}

// ColumnOrder reads a board's live column ids in position order.
func (e *Env) ColumnOrder(t *testing.T, boardID string) []string {
	t.Helper()

	rows, err := e.DB.Query(
		`SELECT id FROM columns WHERE board_id = ? AND deleted_at IS NULL ORDER BY position`, boardID)
	if err != nil {
		t.Fatalf("reading column order: %v", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// CountRows is the workhorse of the "and nothing changed" half of C2 and C4.
func (e *Env) CountRows(t *testing.T, table string) int {
	t.Helper()

	var n int
	if err := e.DB.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}
