package http

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// sync applies a batch the client queued while offline.
//
// The endpoint answers 200 even when operations inside the batch failed: the
// per-operation outcome is in the body. A batch of twenty changes returning
// 409 because one of them hit a WIP limit would leave the user stuck on their
// phone with no way to get the other nineteen through (02-testing.md §4.5).
//
// The batch fails whole for exactly one reason: it was malformed. That is a
// bug in the client, not a business case, and answering 200 to it would hide
// the bug.
func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	body, err := decode[syncRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	ops := make([]domain.Operation, 0, len(body.Operations))
	for _, o := range body.Operations {
		op, err := toOperation(o)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		ops = append(ops, op)
	}

	results, err := s.deps.Sync.Apply(r.Context(), ops)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, syncResponse{Results: toOpResults(results)})
}

// toOperation turns the wire shape into the domain's. Every payload is decoded
// strictly, so an operation the client built wrong is a 400 now rather than a
// field silently dropped and a card that never got its label.
func toOperation(o operationRequest) (domain.Operation, error) {
	op := domain.Operation{OpID: o.OpID, Type: domain.OpType(o.Type), At: o.At}
	if !domain.KnownOpType(op.Type) {
		return op, domain.Invalid("type", "unknown operation type: "+o.Type)
	}

	var err error
	switch op.Type {
	case domain.OpBoardCreate:
		var p struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.BoardCreatePayload{ID: p.ID, Name: p.Name}
		}

	case domain.OpBoardUpdate:
		var p struct {
			ID   string  `json:"id"`
			Name *string `json:"name"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.BoardUpdatePayload{ID: p.ID, Name: p.Name}
		}

	case domain.OpBoardDelete:
		var p struct {
			ID string `json:"id"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.BoardDeletePayload{ID: p.ID}
		}

	case domain.OpColumnCreate:
		var p struct {
			ID       string `json:"id"`
			BoardID  string `json:"board_id"`
			Name     string `json:"name"`
			WIPLimit *int   `json:"wip_limit"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.ColumnCreatePayload{ID: p.ID, BoardID: p.BoardID, Name: p.Name, WIPLimit: p.WIPLimit}
		}

	case domain.OpColumnUpdate:
		var p struct {
			ID string `json:"id"`
			updateColumnRequest
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			var patch domain.ColumnPatch
			if patch, err = p.patch(); err == nil {
				op.Payload = domain.ColumnUpdatePayload{ID: p.ID, Patch: patch}
			}
		}

	case domain.OpColumnDelete:
		var p struct {
			ID string `json:"id"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.ColumnDeletePayload{ID: p.ID}
		}

	case domain.OpColumnReorder:
		var p struct {
			BoardID string   `json:"board_id"`
			IDs     []string `json:"ids"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.ColumnReorderPayload{BoardID: p.BoardID, IDs: p.IDs}
		}

	case domain.OpCardCreate:
		var p struct {
			ID          string `json:"id"`
			ColumnID    string `json:"column_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Label       string `json:"label"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.CardCreatePayload{
				ID: p.ID, ColumnID: p.ColumnID, Title: p.Title,
				Description: p.Description, Label: p.Label,
			}
		}

	case domain.OpCardUpdate:
		var p struct {
			ID string `json:"id"`
			updateCardRequest
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.CardUpdatePayload{ID: p.ID, Patch: p.patch()}
		}

	case domain.OpCardDelete:
		var p struct {
			ID string `json:"id"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.CardDeletePayload{ID: p.ID}
		}

	case domain.OpCardMove:
		var p struct {
			ID         string `json:"id"`
			ToColumnID string `json:"to_column_id"`
			ToIndex    int    `json:"to_index"`
		}
		if err = decodePayload(o.Payload, &p); err == nil {
			op.Payload = domain.CardMovePayload{ID: p.ID, ToColumnID: p.ToColumnID, ToIndex: p.ToIndex}
		}
	}
	return op, err
}

func decodePayload(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return domain.Invalid("payload", "must not be empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return domain.Invalid("payload", "does not match the operation type: "+err.Error())
	}
	return nil
}
