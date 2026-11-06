package http_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adapterhttp "github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http"
)

// The MCP secret travels in the path, and the path is what the request log
// records. The line must name the route, never the secret.
func TestTheRequestLogNeverHoldsTheSecret(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	srv := adapterhttp.New(adapterhttp.Deps{
		Token:  "test-token-0123456789",
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		MCP:    http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }),
	})

	const secret = "a-secret-that-must-not-be-logged-0123"
	req := httptest.NewRequest("POST", "/api/mcp/"+secret, strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: the route needs no token", rec.Code)
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("the log holds the secret:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), `"path":"/api/mcp/{secret}"`) {
		t.Errorf("the log does not name the route:\n%s", logs.String())
	}
}
