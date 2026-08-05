// Package middleware holds the cross-cutting HTTP concerns: auth, CORS,
// logging, panic recovery and the request timeout.
package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// TokenHeader is where layer 2's token travels.
//
// It is NOT `Authorization`, and that is the whole point. Layer 1 is basic
// auth in nginx, which uses `Authorization: Basic`. A request carries exactly
// one Authorization header, so a client that puts its bearer token there
// displaces the basic credentials — and nginx answers 401 before the request
// ever reaches this process. The two layers cannot share the header, and the
// deployed app is where you find that out.
//
// So the token gets a header of its own, the browser keeps `Authorization`
// for basic auth, and both layers apply to the same request.
const TokenHeader = "X-Scrumboard-Token"

// Auth is layer 2 of the access control. Layer 1 filters scanners before they
// ever reach this process; this is what stops anyone who gets past it, or who
// reaches the container directly (00-scope.md §6.1).
//
// There is no user table and no hashing, because there are no users. There is
// one shared token, compared in constant time — the comparison is cheap and
// the alternative leaks the token one byte at a time to anyone patient.
func Auth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ValidToken(TokenFromHeaders(r), token) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				//nolint:errcheck // nothing useful to do if the client hung up
				w.Write([]byte(`{"error":{"code":"unauthorized","message":"a valid token is required in the ` + TokenHeader + ` header"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TokenFromHeaders reads the token from its own header, falling back to
// `Authorization: Bearer`.
//
// The fallback is for callers with no basic auth in front of them — curl, the
// tests, a health probe inside the VM — where `Authorization` is free and
// typing a bearer token is the familiar thing to do. The browser never uses
// it, for the reason on TokenHeader.
func TokenFromHeaders(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get(TokenHeader)); t != "" {
		return t
	}
	if t, ok := bearer(r); ok {
		return t
	}
	return ""
}

// TokenFromRequest adds the `token` query parameter to the header sources.
//
// The query parameter exists for one caller only: the WebSocket upgrade, which
// browsers will not let us set headers on. It is deliberately not accepted by
// the HTTP middleware — a token in a query string is a token in every nginx
// access log line, and the WebSocket has no alternative while the REST API
// does.
func TokenFromRequest(r *http.Request) string {
	if t := TokenFromHeaders(r); t != "" {
		return t
	}
	return r.URL.Query().Get("token")
}

// ValidToken compares in constant time. Exported for the WebSocket handshake,
// which authenticates itself rather than through the middleware.
//
// An empty token is never valid, including against an empty configured one:
// a misconfigured server should refuse everyone, not admit everyone.
func ValidToken(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, value, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(value), true
}
