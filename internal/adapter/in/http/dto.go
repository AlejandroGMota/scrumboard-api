package http

import (
	"encoding/json"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Domain types are never serialised directly. The wire format is a contract
// with the frontend and with the offline queue; the domain is free to change
// shape without breaking either, and these structs are where the two meet.
//
// Timestamps are RFC 3339 in UTC (00-scope.md §11 question 5). Go's
// time.Time marshals to exactly that.

// There are two board shapes and two column shapes, rather than one of each
// with omitempty. The difference matters: omitempty drops an *empty* slice as
// readily as a missing one, so an empty column would arrive with no "cards"
// key at all and the frontend would iterate undefined. Splitting the types
// makes "this response nests its children" a decision rather than an accident.

type boardDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// nestedBoardDTO is what GET /api/boards/{id} returns: the whole screen.
type nestedBoardDTO struct {
	boardDTO
	Columns []nestedColumnDTO `json:"columns"`
}

type columnDTO struct {
	ID        string    `json:"id"`
	BoardID   string    `json:"board_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	WIPLimit  *int      `json:"wip_limit"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// nestedColumnDTO always carries a cards array, empty or not.
type nestedColumnDTO struct {
	columnDTO
	Cards []cardDTO `json:"cards"`
}

type cardDTO struct {
	ID          string    `json:"id"`
	ColumnID    string    `json:"column_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Label       string    `json:"label"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toBoard(b *domain.Board) boardDTO {
	return boardDTO{
		ID:        b.ID,
		Name:      b.Name,
		CreatedAt: b.CreatedAt,
		UpdatedAt: b.UpdatedAt,
	}
}

// toNestedBoard renders the board and everything under it. Every slice it
// produces is non-nil, all the way down.
func toNestedBoard(b *domain.Board) nestedBoardDTO {
	dto := nestedBoardDTO{
		boardDTO: toBoard(b),
		Columns:  make([]nestedColumnDTO, 0, len(b.Columns)),
	}
	for i := range b.Columns {
		dto.Columns = append(dto.Columns, toNestedColumn(&b.Columns[i]))
	}
	return dto
}

// toBoards always returns a slice, never nil. A nil slice serialises to null
// and breaks the frontend — the classic Go trap (02-testing.md §4.2).
func toBoards(bs []domain.Board) []boardDTO {
	out := make([]boardDTO, 0, len(bs))
	for i := range bs {
		out = append(out, toBoard(&bs[i]))
	}
	return out
}

func toColumn(c *domain.Column) columnDTO {
	return columnDTO{
		ID:        c.ID,
		BoardID:   c.BoardID,
		Name:      c.Name,
		Position:  c.Position,
		WIPLimit:  c.WIPLimit,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

func toNestedColumn(c *domain.Column) nestedColumnDTO {
	dto := nestedColumnDTO{
		columnDTO: toColumn(c),
		Cards:     make([]cardDTO, 0, len(c.Cards)),
	}
	for i := range c.Cards {
		dto.Cards = append(dto.Cards, toCard(&c.Cards[i]))
	}
	return dto
}

func toColumns(cs []domain.Column) []columnDTO {
	out := make([]columnDTO, 0, len(cs))
	for i := range cs {
		out = append(out, toColumn(&cs[i]))
	}
	return out
}

func toCard(c *domain.Card) cardDTO {
	return cardDTO{
		ID:          c.ID,
		ColumnID:    c.ColumnID,
		Title:       c.Title,
		Description: c.Description,
		Label:       c.Label,
		Position:    c.Position,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// --- requests -------------------------------------------------------------

type createBoardRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type updateBoardRequest struct {
	Name *string `json:"name"`
}

type createColumnRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	WIPLimit *int   `json:"wip_limit"`
}

// updateColumnRequest has to tell three cases apart: the field is absent
// (leave it alone), the field is null (clear the limit), the field is a number
// (set it). A *int collapses the first two, so the raw message is kept and
// interpreted below.
type updateColumnRequest struct {
	Name     *string         `json:"name"`
	WIPLimit json.RawMessage `json:"wip_limit"`
}

func (r updateColumnRequest) patch() (domain.ColumnPatch, error) {
	p := domain.ColumnPatch{Name: r.Name}
	if len(r.WIPLimit) == 0 {
		return p, nil
	}
	p.SetWIPLimit = true
	if string(r.WIPLimit) == "null" {
		var none *int
		p.WIPLimit = &none
		return p, nil
	}
	var limit int
	if err := json.Unmarshal(r.WIPLimit, &limit); err != nil {
		return p, domain.Invalid("wip_limit", "must be a whole number or null")
	}
	l := &limit
	p.WIPLimit = &l
	return p, nil
}

type reorderColumnsRequest struct {
	IDs []string `json:"ids"`
}

type createCardRequest struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Label       string `json:"label"`
}

type updateCardRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Label       *string `json:"label"`
}

func (r updateCardRequest) patch() domain.CardPatch {
	return domain.CardPatch{Title: r.Title, Description: r.Description, Label: r.Label}
}

type moveCardRequest struct {
	ToColumnID string `json:"to_column_id"`
	ToIndex    int    `json:"to_index"`
}

// --- sync -----------------------------------------------------------------

type syncRequest struct {
	Operations []operationRequest `json:"operations"`
}

type operationRequest struct {
	OpID    string          `json:"op_id"`
	Type    string          `json:"type"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

type syncResponse struct {
	Results []opResultDTO `json:"results"`
}

type opResultDTO struct {
	OpID    string `json:"op_id"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// State is the server's version of the affected entity, sent back on
	// conflict and on failure so the client can correct itself without a
	// second round trip.
	State any `json:"state,omitempty"`
}

func toOpResults(rs []domain.OpResult) []opResultDTO {
	out := make([]opResultDTO, 0, len(rs))
	for _, r := range rs {
		out = append(out, opResultDTO{
			OpID:    r.OpID,
			Status:  string(r.Status),
			Code:    r.Code,
			Message: r.Message,
			State:   toState(r.State),
		})
	}
	return out
}

// toState maps whichever entity the sync service attached, keeping domain
// types off the wire here too.
func toState(state any) any {
	switch v := state.(type) {
	case *domain.Board:
		if v == nil {
			return nil
		}
		return toBoard(v)
	case *domain.Column:
		if v == nil {
			return nil
		}
		return toColumn(v)
	case *domain.Card:
		if v == nil {
			return nil
		}
		return toCard(v)
	case []domain.Column:
		return toColumns(v)
	default:
		return nil
	}
}

// --- events ---------------------------------------------------------------

// eventDTO is what travels over the WebSocket. It carries op_id so the client
// that caused the change can ignore its own echo (00-scope.md §8).
type eventDTO struct {
	Type    string    `json:"type"`
	BoardID string    `json:"board_id"`
	At      time.Time `json:"at"`
	OpID    string    `json:"op_id,omitempty"`
	Payload any       `json:"payload,omitempty"`
}

type columnsOrderedDTO struct {
	BoardID string   `json:"board_id"`
	IDs     []string `json:"ids"`
}

type cardMovedDTO struct {
	Card         cardDTO `json:"card"`
	FromColumnID string  `json:"from_column_id"`
	ToColumnID   string  `json:"to_column_id"`
}

type syncAppliedDTO struct {
	Applied int `json:"applied"`
	Failed  int `json:"failed"`
}

// EncodeEvent renders a domain event as the JSON the frontend receives. It is
// exported because the WebSocket adapter needs it and the wire format belongs
// here, next to everything else the client parses.
func EncodeEvent(e domain.Event) ([]byte, error) {
	dto := eventDTO{
		Type:    string(e.Type),
		BoardID: e.BoardID,
		At:      e.At,
		OpID:    e.OpID,
	}
	switch p := e.Payload.(type) {
	case *domain.Board:
		dto.Payload = toBoard(p)
	case *domain.Column:
		dto.Payload = toColumn(p)
	case *domain.Card:
		dto.Payload = toCard(p)
	case domain.CardMovedPayload:
		dto.Payload = cardMovedDTO{
			Card:         toCard(p.Card),
			FromColumnID: p.FromColumnID,
			ToColumnID:   p.ToColumnID,
		}
	case domain.ColumnsOrderedPayload:
		dto.Payload = columnsOrderedDTO{BoardID: p.BoardID, IDs: p.IDs}
	case domain.SyncAppliedPayload:
		dto.Payload = syncAppliedDTO{Applied: p.Applied, Failed: p.Failed}
	}
	return json.Marshal(dto)
}
