package mcp

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
)

// maxBody bounds one message. A tool call is a title and a description; the
// API itself caps a description at 50000 characters.
const maxBody = 1 << 20

// NewHandler serves the protocol over Streamable HTTP, statelessly: every
// POST carries one message (or a batch) and gets its answer as plain JSON, so
// there is no session to keep and no event stream to hold open.
//
// The secret is the path segment {secret}, compared in constant time. It is
// the connector's only credential: claude.ai configures a remote server with
// nothing but a URL, so neither a header nor a query parameter is available.
// A wrong secret answers 404, as if the route did not exist.
func NewHandler(secret string, s *Server) http.Handler {
	want := []byte(secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.PathValue("secret"))
		if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			http.NotFound(w, r)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			status := http.StatusBadRequest
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, http.StatusText(status), status)
			return
		}

		out := s.Respond(r.Context(), body)
		if out == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		//nolint:errcheck // nothing useful to do if the client hung up
		w.Write(out)
	})
}
