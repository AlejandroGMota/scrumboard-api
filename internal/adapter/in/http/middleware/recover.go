package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover turns a panic into a 500 and a log line with a stack trace.
//
// Without it, one nil dereference in a handler takes the whole process down —
// and with it the board on every device, over a bug that affected one request.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is the standard library's way of
				// aborting a response on purpose. Passing it on keeps the
				// server's own behaviour intact.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.Error("panic in handler",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				//nolint:errcheck // the connection is already in trouble
				w.Write([]byte(`{"error":{"code":"internal","message":"something went wrong"}}`))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
