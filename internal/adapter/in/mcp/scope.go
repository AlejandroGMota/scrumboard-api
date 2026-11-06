package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

// The slice of a board the tools need to find things by name or by id.
type boardView struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Columns []columnView `json:"columns"`
}

type columnView struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Cards []cardView `json:"cards"`
}

type cardView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Server) restricted() bool { return s.boards != nil }

func (s *Server) allows(boardID string) bool {
	return !s.restricted() || slices.Contains(s.boards, boardID)
}

// notFound is worded like the API's own 404, so a board outside the list is
// indistinguishable from one that does not exist.
func notFound(kind, id string) error { return fmt.Errorf("%s %s not found", kind, id) }

// visibleBoards returns the board list as the API sends it, filtered to the
// boards the tools may touch, plus the decoded views of the same entries.
func (s *Server) visibleBoards(ctx context.Context) ([]json.RawMessage, []boardView, error) {
	raw, err := s.caller.Call(ctx, "GET", "/api/boards", nil)
	if err != nil {
		return nil, nil, err
	}
	var all []json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, nil, err
	}
	var keep []json.RawMessage
	var views []boardView
	for _, b := range all {
		var v boardView
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, nil, err
		}
		if s.allows(v.ID) {
			keep = append(keep, b)
			views = append(views, v)
		}
	}
	return keep, views, nil
}

// board reads one board with its columns and cards.
func (s *Server) board(ctx context.Context, id string) (boardView, json.RawMessage, error) {
	if !s.allows(id) {
		return boardView{}, nil, notFound("board", id)
	}
	raw, err := s.caller.Call(ctx, "GET", "/api/boards/"+url.PathEscape(id), nil)
	if err != nil {
		return boardView{}, nil, err
	}
	var v boardView
	if err := json.Unmarshal(raw, &v); err != nil {
		return boardView{}, nil, err
	}
	return v, raw, nil
}

// scopeBoards reads every board in the list. A listed board that has since
// been deleted is skipped rather than failing every call.
func (s *Server) scopeBoards(ctx context.Context) ([]boardView, error) {
	var out []boardView
	for _, id := range s.boards {
		v, _, err := s.board(ctx, id)
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// checkColumn and checkCard keep the id-based tools inside the list. Without
// a list the API's own 404 is answer enough, and nothing is read up front.
func (s *Server) checkColumn(ctx context.Context, id string) error {
	if !s.restricted() {
		return nil
	}
	boards, err := s.scopeBoards(ctx)
	if err != nil {
		return err
	}
	for _, b := range boards {
		for _, c := range b.Columns {
			if c.ID == id {
				return nil
			}
		}
	}
	return notFound("column", id)
}

func (s *Server) checkCard(ctx context.Context, id string) error {
	if !s.restricted() {
		return nil
	}
	boards, err := s.scopeBoards(ctx)
	if err != nil {
		return err
	}
	for _, b := range boards {
		for _, c := range b.Columns {
			for _, card := range c.Cards {
				if card.ID == id {
					return nil
				}
			}
		}
	}
	return notFound("card", id)
}

// resolveBoard finds a board by the name a person would say out loud. An
// empty name means the default board, or the only one there is.
func (s *Server) resolveBoard(ctx context.Context, boardName string) (boardView, error) {
	var boardID string
	switch {
	case boardName != "":
		_, boards, err := s.visibleBoards(ctx)
		if err != nil {
			return boardView{}, err
		}
		b, err := pick("board", boardName, boards, func(b boardView) string { return b.Name })
		if err != nil {
			return boardView{}, err
		}
		boardID = b.ID
	case s.defaultBoard != "":
		boardID = s.defaultBoard
	default:
		_, boards, err := s.visibleBoards(ctx)
		if err != nil {
			return boardView{}, err
		}
		if len(boards) != 1 {
			return boardView{}, fmt.Errorf("no default board is configured, so name one; the boards are: %s",
				names(boards, func(b boardView) string { return b.Name }))
		}
		boardID = boards[0].ID
	}

	b, _, err := s.board(ctx, boardID)
	return b, err
}

// resolve finds a column by name on a board found by resolveBoard. An empty
// column name means the board's first column.
func (s *Server) resolve(ctx context.Context, boardName, columnName string) (boardView, columnView, error) {
	b, err := s.resolveBoard(ctx, boardName)
	if err != nil {
		return boardView{}, columnView{}, err
	}
	if columnName == "" {
		if len(b.Columns) == 0 {
			return boardView{}, columnView{}, fmt.Errorf("board %q has no columns", b.Name)
		}
		return b, b.Columns[0], nil
	}
	c, err := pick("column", columnName, b.Columns, func(c columnView) string { return c.Name })
	if err != nil {
		return boardView{}, columnView{}, err
	}
	return b, c, nil
}

// readableBoards reads every board the tools may touch, cards included.
func (s *Server) readableBoards(ctx context.Context) ([]boardView, error) {
	if s.restricted() {
		return s.scopeBoards(ctx)
	}
	_, list, err := s.visibleBoards(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]boardView, 0, len(list))
	for _, b := range list {
		v, _, err := s.board(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// pick matches a spoken name: an exact match after folding wins, then a
// unique partial one ("moby dick" finds "💡 Idea · Moby Dick"). Anything else
// is an error that lists the candidates, so Claude can ask instead of guess.
func pick[T any](kind, query string, items []T, name func(T) string) (T, error) {
	var zero T
	q := fold(query)
	var exact, partial []T
	for _, it := range items {
		n := fold(name(it))
		switch {
		case n == q:
			exact = append(exact, it)
		case strings.Contains(n, q):
			partial = append(partial, it)
		}
	}
	for _, found := range [][]T{exact, partial} {
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return zero, fmt.Errorf("%q matches more than one %s: %s", query, kind, names(found, name))
		}
	}
	return zero, fmt.Errorf("no %s matches %q; the %ss are: %s", kind, query, kind, names(items, name))
}

func names[T any](items []T, name func(T) string) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = fmt.Sprintf("%q", name(it))
	}
	return strings.Join(out, ", ")
}

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c",
)

// fold makes "Día a día" and "dia  a dia" the same name, and "Xeleva ·
// Activo" the same as "xeleva activo": anything but a letter or a digit
// separates words, so separators and emoji never have to be typed.
func fold(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, accents.Replace(strings.ToLower(s)))
	return strings.Join(strings.Fields(s), " ")
}
