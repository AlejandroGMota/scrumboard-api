package http

// A Route is one line of the API's surface. The table below is the source of
// truth for two things at once: which routes exist, and which of them are
// public. The router reads it to decide whether to wrap a handler in auth, and
// auth_test.go reads it to check that none escaped (02-testing.md §5.1).
type Route struct {
	Method string
	Path   string
	// Public means no Authorization header is required. Exactly one route is,
	// and it is the health check.
	Public bool
	// Handshake marks a route that authenticates itself rather than through
	// the middleware. The WebSocket has to: browsers cannot set headers on a
	// WebSocket connection, so the token travels as a query parameter and is
	// checked during the upgrade.
	Handshake bool
}

// Pattern renders the route as a Go 1.22 ServeMux pattern.
func (r Route) Pattern() string { return r.Method + " " + r.Path }

// Routes is the whole API. A route missing from this table is registered
// fail-closed (it still requires auth) and fails TestRouteTableMatchesRouter,
// which is what makes forgetting to add one a build failure rather than a
// hole (02-testing.md §5.2).
var Routes = []Route{
	{Method: "GET", Path: "/health", Public: true},

	{Method: "GET", Path: "/api/labels"},

	{Method: "GET", Path: "/api/boards"},
	{Method: "POST", Path: "/api/boards"},
	{Method: "GET", Path: "/api/boards/{id}"},
	{Method: "PATCH", Path: "/api/boards/{id}"},
	{Method: "DELETE", Path: "/api/boards/{id}"},

	{Method: "POST", Path: "/api/boards/{boardID}/columns"},
	{Method: "PUT", Path: "/api/boards/{boardID}/columns/order"},
	{Method: "PATCH", Path: "/api/columns/{id}"},
	{Method: "DELETE", Path: "/api/columns/{id}"},

	{Method: "POST", Path: "/api/columns/{columnID}/cards"},
	{Method: "PATCH", Path: "/api/cards/{id}"},
	{Method: "DELETE", Path: "/api/cards/{id}"},
	{Method: "PUT", Path: "/api/cards/{id}/move"},

	{Method: "POST", Path: "/api/sync"},

	{Method: "GET", Path: "/ws", Handshake: true},
}

// lookup finds a route by method and path.
func lookup(method, path string) (Route, bool) {
	for _, r := range Routes {
		if r.Method == method && r.Path == path {
			return r, true
		}
	}
	return Route{}, false
}
