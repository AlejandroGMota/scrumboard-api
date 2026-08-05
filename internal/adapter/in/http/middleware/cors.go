package middleware

import "net/http"

// CORS allows exactly one origin. A request from anywhere else gets no
// Access-Control-Allow-Origin header at all, which is what the browser needs
// to see in order to refuse it.
//
// The reason this is four lines and a test rather than a library: the failure
// mode is someone adding "*" while debugging and never taking it out, which
// opens the API to every page on the internet (02-testing.md §5.4).
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Origin") == origin && origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Op-Id, "+TokenHeader)
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.Header().Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
