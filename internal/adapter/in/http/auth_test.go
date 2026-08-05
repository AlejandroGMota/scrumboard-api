package http_test

import (
	"net/http"
	"regexp"
	"testing"

	adapterhttp "github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http/middleware"
	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
	"github.com/google/go-cmp/cmp"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// concretePath turns "/api/boards/{id}" into a real URL. The id does not need
// to exist: auth runs before the handler, so a route that answers 404 here has
// already failed the test it is in.
func concretePath(p string) string {
	return pathParam.ReplaceAllString(p, "0192-does-not-exist")
}

// The test that polices every endpoint written from now on.
func TestEveryRouteRequiresAuth(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	for _, route := range adapterhttp.Routes {
		if route.Public || route.Handshake {
			continue
		}
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()

			path := concretePath(route.Path)

			// C2: no token at all.
			res := env.Client.NoAuth().Do(route.Method, path, `{}`)
			if res.Status != http.StatusUnauthorized {
				t.Errorf("without a token: status = %d, want 401\nbody: %s", res.Status, res.Body)
			}

			// C3: a token, but the wrong one.
			res = env.Client.WithToken("not-the-right-token").Do(route.Method, path, `{}`)
			if res.Status != http.StatusUnauthorized {
				t.Errorf("with an invalid token: status = %d, want 401\nbody: %s", res.Status, res.Body)
			}
		})
	}
}

// The layer-2 token must not travel in `Authorization`, because layer 1 —
// basic auth in nginx — already owns that header and a request carries only
// one of them.
//
// This was found by putting the real nginx config in front of the real
// container: with basic auth alone nginx passed and the API rejected; with a
// bearer token alone nginx rejected; and both together is not expressible,
// because the second Authorization header replaces the first. The deployed
// PWA could never have called the API.
func TestTheTokenTravelsInItsOwnHeader(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	t.Run("the dedicated header is accepted", func(t *testing.T) {
		t.Parallel()

		res := requestWith(t, env, map[string]string{middleware.TokenHeader: testutil.Token})
		if res.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", res.StatusCode)
		}
	})

	// Kept for callers with nothing in front of them: curl, CI, a probe on
	// the VM. The browser never uses it.
	t.Run("Authorization: Bearer still works for callers with no basic auth", func(t *testing.T) {
		t.Parallel()

		res := requestWith(t, env, map[string]string{"Authorization": "Bearer " + testutil.Token})
		if res.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", res.StatusCode)
		}
	})

	// The combination production actually produces: nginx consumed the
	// Authorization header for basic auth, and the token arrived beside it.
	t.Run("both layers on one request", func(t *testing.T) {
		t.Parallel()

		res := requestWith(t, env, map[string]string{
			"Authorization":        "Basic YWxlamFuZHJvOnRlc3RwYXNz",
			middleware.TokenHeader: testutil.Token,
		})
		if res.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200: basic auth and the token must coexist", res.StatusCode)
		}
	})

	t.Run("a basic auth header alone is not a token", func(t *testing.T) {
		t.Parallel()

		res := requestWith(t, env, map[string]string{"Authorization": "Basic YWxlamFuZHJvOnRlc3RwYXNz"})
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", res.StatusCode)
		}
	})

	t.Run("an empty token is refused", func(t *testing.T) {
		t.Parallel()

		res := requestWith(t, env, map[string]string{middleware.TokenHeader: ""})
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", res.StatusCode)
		}
	})
}

func requestWith(t *testing.T, env *testutil.Env, headers map[string]string) *http.Response {
	t.Helper()

	req, err := http.NewRequest("GET", env.URL+"/api/boards", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// The counterpart, and the one that actually catches the mistake. Without it
// the test above gives false comfort: a new endpoint nobody added to the table
// is simply never checked (02-testing.md §5.2).
func TestRouteTableMatchesRouter(t *testing.T) {
	t.Parallel()

	registered := testutil.Routes(t)

	declared := map[string]adapterhttp.Route{}
	for _, r := range adapterhttp.Routes {
		declared[r.Pattern()] = r
	}

	seen := map[string]bool{}
	for _, r := range registered {
		seen[r.Pattern()] = true
		if _, ok := declared[r.Pattern()]; !ok {
			t.Errorf("the router registers %q, which is missing from Routes in routes.go.\n"+
				"Add it there — otherwise TestEveryRouteRequiresAuth will never check it.", r.Pattern())
		}
	}

	for pattern, route := range declared {
		// The WebSocket route is only registered once its adapter is wired in,
		// which the endpoint tests do not do.
		if route.Handshake {
			continue
		}
		if !seen[pattern] {
			t.Errorf("Routes declares %q, but the router never registered it", pattern)
		}
	}
}

// One test per security header. Four lines that stop the API being opened to
// the world by a "*" added during debugging and never removed
// (02-testing.md §5.4).
func TestCORSAllowsOnlyTheConfiguredOrigin(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	const allowed = "https://scrumboard.alejandrogmota.com"

	t.Run("the configured origin is allowed", func(t *testing.T) {
		t.Parallel()

		res := requestWithOrigin(t, env, allowed)
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != allowed {
			t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, allowed)
		}
	})

	t.Run("any other origin gets no header at all", func(t *testing.T) {
		t.Parallel()

		res := requestWithOrigin(t, env, "https://evil.example.com")
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, want it absent", got)
		}
	})
}

func requestWithOrigin(t *testing.T, env *testutil.Env, origin string) *http.Response {
	t.Helper()

	req, err := http.NewRequest("GET", env.URL+"/api/boards", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Authorization", "Bearer "+testutil.Token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// The health check is deliberately unauthenticated: the deploy pipeline curls
// it over SSH, and a token in that curl would be a token in the workflow.
func TestHealth(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	t.Run("answers 200 without a token, on an empty board", func(t *testing.T) {
		t.Parallel()

		res := env.Client.NoAuth().GET("/health").ExpectStatus(http.StatusOK)

		var body struct {
			Status string `json:"status"`
		}
		res.JSON(&body)
		if body.Status != "ok" {
			t.Errorf("status = %q, want ok", body.Status)
		}
	})
}

// A 200 that only means "the process started" would pass with the volume
// unmounted, which is the deploy this check exists to catch
// (02-testing.md §4.1).
func TestHealthReportsAnUnreachableDatabase(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	// Closing the pool is the closest thing to pulling the volume out from
	// under a running container.
	if err := env.DB.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	res := env.Client.NoAuth().GET("/health")
	if res.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503\nbody: %s", res.Status, res.Body)
	}
}

func TestLabelsServeTheClosedPalette(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	var body struct {
		Labels []string `json:"labels"`
	}
	env.Client.GET("/api/labels").ExpectStatus(http.StatusOK).JSON(&body)

	want := []string{"red", "orange", "yellow", "green", "blue", "purple"}
	if diff := cmp.Diff(want, body.Labels); diff != "" {
		t.Errorf("palette mismatch (-want +got):\n%s", diff)
	}
}
