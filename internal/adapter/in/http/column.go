package http

import (
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

func (s *Server) createColumn(w http.ResponseWriter, r *http.Request) {
	body, err := decode[createColumnRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	column, created, err := s.deps.Columns.Create(r.Context(), port.CreateColumnCmd{
		ID:       body.ID,
		BoardID:  r.PathValue("boardID"),
		Name:     body.Name,
		WIPLimit: body.WIPLimit,
		OpID:     opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, toColumn(column))
}

func (s *Server) updateColumn(w http.ResponseWriter, r *http.Request) {
	body, err := decode[updateColumnRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	patch, err := body.patch()
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	column, err := s.deps.Columns.Update(r.Context(), port.UpdateColumnCmd{
		ID:    r.PathValue("id"),
		Patch: patch,
		OpID:  opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toColumn(column))
}

// deleteColumn refuses a column that still holds cards, with a 409. Cascading
// that delete onto the cards is the kind of action one regrets at eleven at
// night (00-scope.md §8); the UI moves or deletes them first.
func (s *Server) deleteColumn(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Columns.Delete(r.Context(), port.DeleteColumnCmd{
		ID:   r.PathValue("id"),
		OpID: opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reorderColumns takes the complete list of ids in the desired order. A
// partial list is a 400 rather than a best-effort reorder: this is the easiest
// endpoint in the API to leave half-finished, and the one that corrupts
// ordering when it is (02-testing.md §4.3).
func (s *Server) reorderColumns(w http.ResponseWriter, r *http.Request) {
	body, err := decode[reorderColumnsRequest](r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	columns, err := s.deps.Columns.Reorder(r.Context(), port.ReorderColumnsCmd{
		BoardID: r.PathValue("boardID"),
		IDs:     body.IDs,
		OpID:    opID(r),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toColumns(columns))
}
