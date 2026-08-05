package ws

import (
	"log/slog"
	"net/http"

	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http/middleware"
	"github.com/coder/websocket"
)

// NewHandler returns the GET /ws handler.
//
// It authenticates itself rather than going through the auth middleware,
// because browsers will not let a page set headers on a WebSocket connection.
// The token therefore arrives as a query parameter — which is why the route
// table marks it Handshake rather than Public: it is authenticated, just not
// by the middleware.
//
// A token in a URL is worth a moment's thought. It reaches the server over
// TLS, so it is not on the wire in clear text, but it does land in nginx's
// access log. On a single-user VM where the same person owns the log and the
// token, that is an acceptable trade for a WebSocket that works in a browser
// at all.
func NewHandler(hub *Hub, token string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := middleware.TokenFromRequest(r)
		if got == "" || !middleware.ValidToken(got, token) {
			// Rejected before the upgrade: the connection never becomes a
			// WebSocket, so the client gets a status code it can read rather
			// than an immediate close frame it cannot.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			//nolint:errcheck // nothing useful to do if the client hung up
			w.Write([]byte(`{"error":{"code":"unauthorized","message":"a valid token is required"}}`))
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// The browser's Origin is checked by the host's nginx and by the
			// CORS middleware for HTTP. Here the token is the check that
			// matters, and the app is served from a different path on the
			// same origin, so the library's default same-origin rule is what
			// we want — stated explicitly rather than inherited silently.
			InsecureSkipVerify: false,
			CompressionMode:    websocket.CompressionDisabled,
		})
		if err != nil {
			logger.Debug("websocket upgrade failed", slog.Any("error", err))
			return
		}

		client := newClient(conn, logger)
		if !hub.add(client) {
			// The server is shutting down.
			client.close()
			return
		}
		defer func() {
			hub.remove(client)
			client.close()
		}()

		logger.Info("websocket connected", slog.Int("clients", hub.Count()))

		ctx := r.Context()
		go client.writePump(ctx)
		// readPump blocks until the client goes away, which keeps this handler
		// — and therefore the connection — alive for as long as it should be.
		client.readPump(ctx)

		logger.Info("websocket disconnected", slog.Int("clients", hub.Count()-1))
	})
}
