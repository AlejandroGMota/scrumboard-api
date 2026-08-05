package http

import (
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

type healthResponse struct {
	Status string `json:"status"`
}

// health is the only unauthenticated route, deliberately: the deploy pipeline
// curls it over SSH on the VM (01-structure.md §4.3), and putting a token in
// that curl would mean putting the token in the workflow.
//
// It answers 503 when the database cannot be reached. A 200 that only means
// "the process started" would pass with the volume unmounted and an empty
// database underneath, which is exactly the deploy this check exists to catch.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.deps.Health != nil {
		if err := s.deps.Health(r); err != nil {
			s.logger.ErrorContext(r.Context(), "health check failed", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

type labelsResponse struct {
	Labels []string `json:"labels"`
}

// listLabels serves the closed palette. The set is defined and validated in
// the API (00-scope.md §11 question 2); serving it means the frontend cannot
// drift out of step with what the server will accept.
func (s *Server) listLabels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, labelsResponse{Labels: domain.Labels})
}
