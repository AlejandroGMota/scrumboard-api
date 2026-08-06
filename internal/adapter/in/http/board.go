package http

import (
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// opID reads the client-generated id of this request. It travels back on the
// broadcast event so the originating device can ignore its own echo
// (00-scope.md §8). Absent is fine — a curl has no op id.
func opID(r *http.Request) string { return r.Header.Get("X-Op-Id") }

func (s *Server) listBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := s.deps.Boards.List(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBoards(boards))
}

// getBoard returns the board with its columns and cards nested and ordered by
// position. This is the call that paints the screen, so that ordering is part
// of the contract rather than an implementation detail.
func (s *Server) getBoard(w http.ResponseWriter, r *http.Request) {
	board, err := s.deps.Boards.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toNestedBoard(board))
}

// createBoard answers 201 for a new board and 200 for a repeat of one already
// created with the same id and content — the offline queue resends, and a
// resend is not an error.
func (s *Server) createBoard(w http.ResponseWriter, r *http.Request) {
	body, err := decode[createBoardRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	board, created, err := s.deps.Boards.Create(r.Context(), port.CreateBoardCmd{
		ID:   body.ID,
		Name: body.Name,
		OpID: opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, toBoard(board))
}

func (s *Server) updateBoard(w http.ResponseWriter, r *http.Request) {
	body, err := decode[updateBoardRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	board, err := s.deps.Boards.Update(r.Context(), port.UpdateBoardCmd{
		ID:         r.PathValue("id"),
		Name:       body.Name,
		LabelNames: body.LabelNames,
		OpID:       opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBoard(board))
}

// deleteBoard soft-deletes and cascades. Deleting twice is still a 204:
// deleting is idempotent, and a client retrying after a lost response should
// not have to interpret a 404.
func (s *Server) deleteBoard(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Boards.Delete(r.Context(), port.DeleteBoardCmd{
		ID:   r.PathValue("id"),
		OpID: opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
