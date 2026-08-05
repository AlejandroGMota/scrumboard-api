package http

import (
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// createCard is where the WIP limit is enforced: a column at its limit answers
// 409 with wip_exceeded.
func (s *Server) createCard(w http.ResponseWriter, r *http.Request) {
	body, err := decode[createCardRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	card, created, err := s.deps.Cards.Create(r.Context(), port.CreateCardCmd{
		ID:          body.ID,
		ColumnID:    r.PathValue("columnID"),
		Title:       body.Title,
		Description: body.Description,
		Label:       body.Label,
		OpID:        opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, toCard(card))
}

func (s *Server) updateCard(w http.ResponseWriter, r *http.Request) {
	body, err := decode[updateCardRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	card, err := s.deps.Cards.Update(r.Context(), port.UpdateCardCmd{
		ID:    r.PathValue("id"),
		Patch: body.patch(),
		OpID:  opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toCard(card))
}

func (s *Server) deleteCard(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Cards.Delete(r.Context(), port.DeleteCardCmd{
		ID:   r.PathValue("id"),
		OpID: opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveCard is the most delicate endpoint in the API. Two things it guarantees
// and the tests pin: a refused move leaves the card exactly where it was, and
// after any accepted move both columns hold positions 0..n-1 with no gaps and
// no ties.
func (s *Server) moveCard(w http.ResponseWriter, r *http.Request) {
	body, err := decode[moveCardRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	card, err := s.deps.Cards.Move(r.Context(), port.MoveCardCmd{
		ID:         r.PathValue("id"),
		ToColumnID: body.ToColumnID,
		ToIndex:    body.ToIndex,
		OpID:       opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toCard(card))
}
