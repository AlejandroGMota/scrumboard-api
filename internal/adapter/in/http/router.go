// Package http is the primary adapter: it turns HTTP requests into calls on
// the application's inbound ports, and domain errors into status codes.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http/middleware"
	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// Deps is what the HTTP layer needs. It receives ports, never concrete
// services, and certainly never a database handle.
type Deps struct {
	Boards  port.BoardService
	Columns port.ColumnService
	Cards   port.CardService
	Sync    port.SyncService

	// Health reports whether the process can still reach its database. A
	// health check that only proves the process started would not catch a
	// misconfigured volume (02-testing.md §4.1).
	Health func(r *http.Request) error

	// WS is the WebSocket upgrade handler. Nil until step 4 of the build
	// order, and the route simply is not registered while it is.
	WS http.Handler

	Logger         *slog.Logger
	Token          string
	CORSOrigin     string
	RequestTimeout time.Duration
}

// Server owns the route table as it was actually registered.
type Server struct {
	deps       Deps
	logger     *slog.Logger
	mux        *http.ServeMux
	registered []Route
}

func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.RequestTimeout == 0 {
		d.RequestTimeout = 15 * time.Second
	}

	s := &Server{deps: d, logger: d.Logger, mux: http.NewServeMux()}

	s.handle("GET", "/health", s.health)
	s.handle("GET", "/api/labels", s.listLabels)

	s.handle("GET", "/api/boards", s.listBoards)
	s.handle("POST", "/api/boards", s.createBoard)
	s.handle("GET", "/api/boards/{id}", s.getBoard)
	s.handle("PATCH", "/api/boards/{id}", s.updateBoard)
	s.handle("DELETE", "/api/boards/{id}", s.deleteBoard)

	s.handle("POST", "/api/boards/{boardID}/columns", s.createColumn)
	s.handle("PUT", "/api/boards/{boardID}/columns/order", s.reorderColumns)
	s.handle("PATCH", "/api/columns/{id}", s.updateColumn)
	s.handle("DELETE", "/api/columns/{id}", s.deleteColumn)

	s.handle("POST", "/api/columns/{columnID}/cards", s.createCard)
	s.handle("PATCH", "/api/cards/{id}", s.updateCard)
	s.handle("DELETE", "/api/cards/{id}", s.deleteCard)
	s.handle("PUT", "/api/cards/{id}/move", s.moveCard)

	s.handle("POST", "/api/sync", s.sync)

	if d.WS != nil {
		s.handle("GET", "/ws", d.WS.ServeHTTP)
	}

	return s
}

// handle registers one route and records that it did.
//
// The recording is a side effect of registration rather than something derived
// from the table, which is the whole point: TestRouteTableMatchesRouter
// compares the two, so a route added here and forgotten in routes.go breaks
// the build instead of quietly escaping the auth check (02-testing.md §5.2).
func (s *Server) handle(method, path string, h http.HandlerFunc) {
	route, known := lookup(method, path)
	if !known {
		// Fail closed. An unknown route still gets auth; the test is what
		// tells anyone it happened.
		route = Route{Method: method, Path: path}
	}

	var handler http.Handler = h
	if !route.Handshake {
		// The WebSocket cannot be wrapped in a timeout: the connection is
		// meant to stay open, and TimeoutHandler would cut it.
		handler = http.TimeoutHandler(handler, s.deps.RequestTimeout, `{"error":{"code":"timeout","message":"the request took too long"}}`)
	}
	if !route.Public && !route.Handshake {
		handler = middleware.Auth(s.deps.Token)(handler)
	}

	s.mux.Handle(route.Pattern(), handler)
	s.registered = append(s.registered, route)
}

// Registered returns the routes the router actually registered, in order.
func (s *Server) Registered() []Route {
	return append([]Route(nil), s.registered...)
}

// Handler wraps the mux in the middleware every request passes through.
// Auth is not here: it is per-route, because exactly one route is public.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = middleware.CORS(s.deps.CORSOrigin)(h)
	h = middleware.Log(s.logger)(h)
	h = middleware.Recover(s.logger)(h)
	return h
}
