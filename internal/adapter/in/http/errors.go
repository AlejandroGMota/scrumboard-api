package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// errorBody is the one error shape the API ever returns. A client that can
// parse this can handle every failure.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	// Code is for code: "not_found", "wip_exceeded", "invalid"…
	Code string `json:"code"`
	// Message is for a person reading it.
	Message string `json:"message"`
	// Field names the offending field when there is one.
	Field string `json:"field,omitempty"`
}

// writeError is the single place domain errors become HTTP status codes.
// Handlers return domain errors and never choose a status themselves, which is
// what keeps the mapping consistent across fifteen endpoints.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)

	detail := errorDetail{Code: code, Message: err.Error()}
	var fieldErr *domain.FieldError
	if errors.As(err, &fieldErr) {
		detail.Field = fieldErr.Field
		detail.Message = fieldErr.Message
	}

	// A 500 is the one case where the client learns nothing: the message could
	// name a table, a file path or a query. It goes to the log instead.
	if status == http.StatusInternalServerError {
		s.logger.ErrorContext(r.Context(), "request failed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Any("error", err),
		)
		detail = errorDetail{Code: "internal", Message: "something went wrong"}
	}

	writeJSON(w, status, errorBody{Error: detail})
}

func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrWIPExceeded):
		return http.StatusConflict, "wip_exceeded"
	case errors.Is(err, domain.ErrColumnNotEmpty):
		return http.StatusConflict, "column_not_empty"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrInvalid):
		return http.StatusBadRequest, "invalid"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

// writeJSON is the only way a body leaves this package.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	//nolint:errcheck // the header is already sent; there is nothing left to do
	enc.Encode(body)
}

// decode reads a JSON body strictly.
//
// DisallowUnknownFields turns a client typo — "wipLimit" instead of
// "wip_limit" — into a 400 rather than a silently ignored field, which is the
// difference between finding a bug now and wondering later why the limit never
// applied. It is a private API with one client; strictness costs nothing here.
func decode[T any](r *http.Request) (T, error) {
	var body T
	if r.Body == nil {
		return body, domain.Invalid("body", "must not be empty")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20)) // 1 MB is plenty
	dec.DisallowUnknownFields()

	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return body, domain.Invalid("body", "must not be empty")
		}
		return body, domain.Invalid("body", "is not valid JSON: "+err.Error())
	}
	// A second value in the stream means the client sent two documents.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return body, domain.Invalid("body", "must contain exactly one JSON object")
	}
	return body, nil
}
