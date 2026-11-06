package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	run func(ctx context.Context, s *Server, a args) (json.RawMessage, error)

	// unrestrictedOnly hides the tool when the server is limited to a list
	// of boards.
	unrestrictedOnly bool
}

func obj(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

type args map[string]any

func (a args) required(k string) (string, error) {
	v, ok := a[k].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("missing argument %q", k)
	}
	return v, nil
}

func (a args) optional(k string) string {
	v, _ := a[k].(string)
	return v
}

func marshal(v any) (json.RawMessage, error) { return json.Marshal(v) }

// newID gives a create its own id, which is what makes a retried call land on
// the API's "identical repeat" answer instead of creating the thing twice.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

var allTools = []tool{
	{
		Name:        "list_boards",
		Description: "List every board (id and name).",
		InputSchema: obj(nil, map[string]any{}),
		run: func(ctx context.Context, s *Server, _ args) (json.RawMessage, error) {
			boards, _, err := s.visibleBoards(ctx)
			if err != nil {
				return nil, err
			}
			if boards == nil {
				boards = []json.RawMessage{}
			}
			return marshal(boards)
		},
	},
	{
		Name:        "get_board",
		Description: "Read one board with its columns and cards, in board order. Use it to find column and card ids.",
		InputSchema: obj([]string{"board_id"}, map[string]any{"board_id": str("Board id")}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			id, err := a.required("board_id")
			if err != nil {
				return nil, err
			}
			_, raw, err := s.board(ctx, id)
			return raw, err
		},
	},
	{
		Name:        "list_labels",
		Description: "List the allowed label colours.",
		InputSchema: obj(nil, map[string]any{}),
		run: func(ctx context.Context, s *Server, _ args) (json.RawMessage, error) {
			return s.caller.Call(ctx, "GET", "/api/labels", nil)
		},
	},
	{
		Name: "add_card",
		Description: "Add a card by board and column name, the way the user says them. Both are optional: " +
			"without a board it goes to the default board, without a column to the board's first column. " +
			"Title up to 500 characters, description up to 50000 (plain text).",
		InputSchema: obj([]string{"title"}, map[string]any{
			"title":       str("Card title"),
			"board":       str("Board name; omit for the default board"),
			"column":      str("Column name; omit for the board's first column"),
			"description": str("Optional plain-text description"),
			"label":       str("Optional label colour from list_labels"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			title, err := a.required("title")
			if err != nil {
				return nil, err
			}
			b, c, err := s.resolve(ctx, a.optional("board"), a.optional("column"))
			if err != nil {
				return nil, err
			}
			card, err := createCard(ctx, s, c.ID, title, a.optional("description"), a.optional("label"))
			if err != nil {
				return nil, err
			}
			return marshal(map[string]any{"board": b.Name, "column": c.Name, "card": card})
		},
	},
	{
		Name:        "create_card",
		Description: "Create a card at the end of a column, by column id. Title up to 500 characters, description up to 50000 (plain text).",
		InputSchema: obj([]string{"column_id", "title"}, map[string]any{
			"column_id":   str("Column id"),
			"title":       str("Card title"),
			"description": str("Optional plain-text description"),
			"label":       str("Optional label colour from list_labels, or empty"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			col, err := a.required("column_id")
			if err != nil {
				return nil, err
			}
			title, err := a.required("title")
			if err != nil {
				return nil, err
			}
			if err := s.checkColumn(ctx, col); err != nil {
				return nil, err
			}
			return createCard(ctx, s, col, title, a.optional("description"), a.optional("label"))
		},
	},
	{
		Name:        "update_card",
		Description: "Change a card's title, description or label. Only the fields given are changed.",
		InputSchema: obj([]string{"card_id"}, map[string]any{
			"card_id":     str("Card id"),
			"title":       str("New title"),
			"description": str("New description (replaces the old one)"),
			"label":       str("New label colour, or empty string to clear"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			id, err := a.required("card_id")
			if err != nil {
				return nil, err
			}
			patch := map[string]string{}
			for _, k := range []string{"title", "description", "label"} {
				if v, ok := a[k].(string); ok {
					patch[k] = v
				}
			}
			if len(patch) == 0 {
				return nil, errors.New("nothing to update")
			}
			if err := s.checkCard(ctx, id); err != nil {
				return nil, err
			}
			return s.caller.Call(ctx, "PATCH", "/api/cards/"+url.PathEscape(id), patch)
		},
	},
	{
		Name:        "move_card",
		Description: "Move a card to a column and index (0 = top). Use it to change a card's status.",
		InputSchema: obj([]string{"card_id", "to_column_id"}, map[string]any{
			"card_id":      str("Card id"),
			"to_column_id": str("Destination column id"),
			"to_index":     map[string]any{"type": "integer", "description": "Position in the destination column, default 0"},
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			id, err := a.required("card_id")
			if err != nil {
				return nil, err
			}
			to, err := a.required("to_column_id")
			if err != nil {
				return nil, err
			}
			idx := 0
			if f, ok := a["to_index"].(float64); ok {
				idx = int(f)
			}
			if err := s.checkCard(ctx, id); err != nil {
				return nil, err
			}
			if err := s.checkColumn(ctx, to); err != nil {
				return nil, err
			}
			return s.caller.Call(ctx, "PUT", "/api/cards/"+url.PathEscape(id)+"/move",
				map[string]any{"to_column_id": to, "to_index": idx})
		},
	},
	{
		Name:        "delete_card",
		Description: "Delete a card (soft delete on the server).",
		InputSchema: obj([]string{"card_id"}, map[string]any{"card_id": str("Card id")}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			id, err := a.required("card_id")
			if err != nil {
				return nil, err
			}
			if err := s.checkCard(ctx, id); err != nil {
				return nil, err
			}
			return s.caller.Call(ctx, "DELETE", "/api/cards/"+url.PathEscape(id), nil)
		},
	},
	{
		Name: "clear_column",
		Description: "Delete every card in one column, by board and column name. Destructive: before calling it, " +
			"tell the user which column and how many cards it holds, and wait for them to confirm.",
		InputSchema: obj([]string{"column"}, map[string]any{
			"column": str("Column name"),
			"board":  str("Board name; omit for the default board"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			column, err := a.required("column")
			if err != nil {
				return nil, err
			}
			b, c, err := s.resolve(ctx, a.optional("board"), column)
			if err != nil {
				return nil, err
			}
			for i, card := range c.Cards {
				if _, err := s.caller.Call(ctx, "DELETE", "/api/cards/"+url.PathEscape(card.ID), nil); err != nil {
					return nil, fmt.Errorf("deleted %d of %d cards, then: %w", i, len(c.Cards), err)
				}
			}
			return marshal(map[string]any{"board": b.Name, "column": c.Name, "deleted": len(c.Cards)})
		},
	},
	{
		Name:        "create_column",
		Description: "Add a column at the right end of a board, by board name.",
		InputSchema: obj([]string{"name"}, map[string]any{
			"name":  str("Column name"),
			"board": str("Board name; omit for the default board"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			name, err := a.required("name")
			if err != nil {
				return nil, err
			}
			b, err := s.resolveBoard(ctx, a.optional("board"))
			if err != nil {
				return nil, err
			}
			id, err := newID()
			if err != nil {
				return nil, err
			}
			col, err := s.caller.Call(ctx, "POST", "/api/boards/"+url.PathEscape(b.ID)+"/columns",
				map[string]string{"id": id, "name": name})
			if err != nil {
				return nil, err
			}
			return marshal(map[string]any{"board": b.Name, "column": col})
		},
	},
	{
		Name:        "rename_column",
		Description: "Rename a column, found by board and column name. Its cards stay where they are.",
		InputSchema: obj([]string{"column", "name"}, map[string]any{
			"column": str("Current column name"),
			"name":   str("New column name"),
			"board":  str("Board name; omit for the default board"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			column, err := a.required("column")
			if err != nil {
				return nil, err
			}
			name, err := a.required("name")
			if err != nil {
				return nil, err
			}
			b, c, err := s.resolve(ctx, a.optional("board"), column)
			if err != nil {
				return nil, err
			}
			col, err := s.caller.Call(ctx, "PATCH", "/api/columns/"+url.PathEscape(c.ID), map[string]string{"name": name})
			if err != nil {
				return nil, err
			}
			return marshal(map[string]any{"board": b.Name, "column": col})
		},
	},
	{
		Name: "search_cards",
		Description: "Find cards whose title or description holds every word of the query, regardless of case " +
			"and accents. Answers board, column, card id, title and the first matching description line, " +
			fmt.Sprintf("at most %d cards. Use it instead of get_board to find one card on a large board.", maxMatches),
		InputSchema: obj([]string{"query"}, map[string]any{
			"query": str("Words to look for"),
			"board": str("Board name to search; omit to search every board"),
		}),
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			query, err := a.required("query")
			if err != nil {
				return nil, err
			}
			var boards []boardView
			if name := a.optional("board"); name != "" {
				b, err := s.resolveBoard(ctx, name)
				if err != nil {
					return nil, err
				}
				boards = []boardView{b}
			} else if boards, err = s.readableBoards(ctx); err != nil {
				return nil, err
			}
			matches, total, err := searchCards(boards, query)
			if err != nil {
				return nil, err
			}
			return marshal(map[string]any{"matches": matches, "total": total, "truncated": total > len(matches)})
		},
	},
	{
		Name:        "create_board",
		Description: "Create a board with its columns, left to right.",
		InputSchema: obj([]string{"name", "columns"}, map[string]any{
			"name": str("Board name"),
			"columns": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Column names, left to right",
			},
		}),
		unrestrictedOnly: true,
		run: func(ctx context.Context, s *Server, a args) (json.RawMessage, error) {
			name, err := a.required("name")
			if err != nil {
				return nil, err
			}
			list, _ := a["columns"].([]any)
			var columns []string
			for _, c := range list {
				if n, ok := c.(string); ok && n != "" {
					columns = append(columns, n)
				}
			}
			if len(columns) == 0 {
				return nil, errors.New(`"columns" needs at least one name`)
			}
			id, err := newID()
			if err != nil {
				return nil, err
			}
			if _, err := s.caller.Call(ctx, "POST", "/api/boards", map[string]string{"id": id, "name": name}); err != nil {
				return nil, err
			}
			for _, c := range columns {
				colID, err := newID()
				if err != nil {
					return nil, err
				}
				if _, err := s.caller.Call(ctx, "POST", "/api/boards/"+url.PathEscape(id)+"/columns",
					map[string]string{"id": colID, "name": c}); err != nil {
					return nil, err
				}
			}
			_, raw, err := s.board(ctx, id)
			return raw, err
		},
	},
}

func createCard(ctx context.Context, s *Server, columnID, title, description, label string) (json.RawMessage, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	return s.caller.Call(ctx, "POST", "/api/columns/"+url.PathEscape(columnID)+"/cards",
		map[string]string{"id": id, "title": title, "description": description, "label": label})
}

// maxMatches keeps a vague query from answering with half the board.
const maxMatches = 30

type match struct {
	Board   string `json:"board"`
	Column  string `json:"column"`
	CardID  string `json:"card_id"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt,omitempty"`
}

// searchCards returns the first maxMatches cards, in board order, whose title
// and description together hold every word of the query, and how many
// matched in all.
func searchCards(boards []boardView, query string) ([]match, int, error) {
	words := strings.Fields(fold(query))
	if len(words) == 0 {
		return nil, 0, fmt.Errorf("missing argument %q", "query")
	}
	out := []match{}
	total := 0
	for _, b := range boards {
		for _, c := range b.Columns {
			for _, card := range c.Cards {
				text := fold(card.Title + " " + card.Description)
				if !containsAll(text, words) {
					continue
				}
				total++
				if len(out) < maxMatches {
					out = append(out, match{Board: b.Name, Column: c.Name, CardID: card.ID, Title: card.Title,
						Excerpt: excerpt(card.Description, words)})
				}
			}
		}
	}
	return out, total, nil
}

func containsAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// excerpt is the first description line holding any of the words, cut to a
// length that keeps thirty matches readable.
func excerpt(description string, words []string) string {
	for _, line := range strings.Split(description, "\n") {
		f := fold(line)
		for _, w := range words {
			if strings.Contains(f, w) {
				line = strings.TrimSpace(line)
				if r := []rune(line); len(r) > 200 {
					line = string(r[:200]) + "…"
				}
				return line
			}
		}
	}
	return ""
}
