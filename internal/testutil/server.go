// Package testutil boots the real application over a temporary database.
//
// The endpoint tests use no mocks (02-testing.md §2.1): router, middleware,
// services and SQLite repositories are the real ones. The only substitutions
// are the three things that would otherwise make a test non-deterministic or
// unobservable — the clock, the id generator, and the event publisher.
package testutil

import (
	"context"
	"database/sql"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adapterhttp "github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/ws"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/broadcast"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/sqlite"
	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/app/port/fake"
	"github.com/alejandrogmota/scrumboard-api/internal/app/service"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Token is the shared token every test client sends.
const Token = "test-token-0123456789"

// Env is one test's whole world: a client with the token already set, the
// events that were published, a clock the test controls, and the database
// underneath so assertions can check what was actually stored.
type Env struct {
	Client *Client
	Events *fake.Publisher
	Clock  *fake.Clock
	IDs    *fake.IDGen
	DB     *sql.DB

	Server *httptest.Server
	URL    string

	// Hub and WSURL are for the tests that assert an HTTP change reaches a
	// connected device.
	Hub   *ws.Hub
	WSURL string
}

// publishers sends every event to each of them in turn, so a test can both
// assert on what was published and listen to it over a real socket.
type publishers []port.EventPublisher

func (ps publishers) Publish(ctx context.Context, e domain.Event) {
	for _, p := range ps {
		p.Publish(ctx, e)
	}
}

// NewEnv boots the full stack over a temporary database.
//
// Shutdown and file removal register with t.Cleanup, so tests clean nothing
// up, and every test starts from its own empty database — which is what makes
// t.Parallel() safe here (01-structure.md §1.7).
func NewEnv(t *testing.T) *Env {
	t.Helper()

	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir()+"/scrumboard.db")
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	events := fake.NewPublisher()
	clock := fake.NewClock()
	ids := fake.NewIDGen()

	// The hub is real, and so is the publisher in front of it. The fake
	// publisher is kept alongside rather than instead: assertions need
	// somewhere to look, and the WebSocket tests need something to listen to.
	hubCtx, stopHub := context.WithCancel(ctx)
	hub := ws.NewHub(slog.New(slog.DiscardHandler))
	go hub.Run(hubCtx)
	t.Cleanup(stopHub)

	deps := service.Deps{
		Boards:  db.Boards(),
		Columns: db.Columns(),
		Cards:   db.Cards(),
		OpLog:   db.OpLog(),
		Tx:      db,
		Events: publishers{
			events,
			broadcast.New(hub, slog.New(slog.DiscardHandler)),
		},
		Clock: clock,
		IDs:   ids,
	}

	srv := adapterhttp.New(adapterhttp.Deps{
		Boards:  service.NewBoard(deps),
		Columns: service.NewColumn(deps),
		Cards:   service.NewCard(deps),
		Sync:    service.NewSync(deps),
		Health:  func(r *nethttp.Request) error { return db.Ping(r.Context()) },
		WS:      ws.NewHandler(hub, Token, slog.New(slog.DiscardHandler)),
		// Discard: a passing test should print nothing, and a failing one
		// says what went wrong through its assertions.
		Logger:         slog.New(slog.DiscardHandler),
		Token:          Token,
		CORSOrigin:     "https://scrumboard.alejandrogmota.com",
		RequestTimeout: 5 * time.Second,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &Env{
		Client: newClient(t, ts.URL, Token),
		Hub:    hub,
		WSURL:  "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + Token,
		Events: events,
		Clock:  clock,
		IDs:    ids,
		DB:     db.SQL(),
		Server: ts,
		URL:    ts.URL,
	}
}

// Routes exposes the registered route table so the meta-tests can compare it
// against the declared one.
func Routes(t *testing.T) []adapterhttp.Route {
	t.Helper()

	srv := adapterhttp.New(adapterhttp.Deps{Token: Token, Logger: slog.New(slog.DiscardHandler)})
	return srv.Registered()
}
