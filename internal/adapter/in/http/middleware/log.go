package middleware

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Log writes one structured line per request.
//
// slog in JSON to stdout, read with `docker compose logs` (00-scope.md §11
// question 6). No log stack: one user does not generate enough lines to need
// somewhere to put them.
func Log(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			logger.LogAttrs(r.Context(), level, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("took", time.Since(started).Round(time.Millisecond)),
			)
		})
	}
}

// statusRecorder remembers the status code so the log line can report it. It
// deliberately does not buffer the body: the response goes straight through.
//
// Wrapping a ResponseWriter costs more than it looks. The interfaces the
// original satisfied are not inherited by the wrapper, and a WebSocket upgrade
// needs http.Hijacker — without the passthrough below, every upgrade answers
// 501 and the only clue is that it works when the logging middleware is off.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// Hijack lets the WebSocket adapter take the connection over.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("the underlying ResponseWriter is not an http.Hijacker")
	}
	return hijacker.Hijack()
}

// Flush keeps streaming responses streaming rather than buffering until the
// handler returns.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}
