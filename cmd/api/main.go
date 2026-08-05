// Command api is the ScrumBoard backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alejandrogmota/scrumboard-api/config"
	adapterhttp "github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/ws"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/broadcast"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/clock"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/idgen"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/sqlite"
	"github.com/alejandrogmota/scrumboard-api/internal/app/service"
)

func main() {
	// Three modes, because the production image is FROM scratch and has no
	// shell to run anything else with. See ops.go.
	var (
		checkHealth = flag.Bool("healthcheck", false, "ask the running server if it is well, then exit (used by the container health check)")
		backupTo    = flag.String("backup", "", "write a consistent snapshot of the database to this path, then exit")
		verifyPath  = flag.String("verify", "", "run PRAGMA integrity_check on the database at this path, then exit")
	)
	flag.Parse()

	var err error
	switch {
	case *checkHealth:
		err = healthcheck()
	case *backupTo != "":
		err = backup(*backupTo)
	case *verifyPath != "":
		err = verify(*verifyPath)
	default:
		err = run()
	}

	if err != nil {
		// slog is already gone or was never set up; stderr is what is left.
		fmt.Fprintf(os.Stderr, "scrumboard-api: %v\n", err)
		os.Exit(1)
	}
}

// run is the composition root: the only function in the project that knows
// which concrete adapter sits behind each port. Everything else received its
// dependencies from here.
func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Signals first, so a container stopped during startup still stops.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("database ready", slog.String("path", cfg.DBPath))

	// The hub, and the publisher that feeds it. The application never learns
	// either exists: it publishes to a port.
	hub := ws.NewHub(logger)
	go hub.Run(ctx)

	deps := service.Deps{
		Boards:  db.Boards(),
		Columns: db.Columns(),
		Cards:   db.Cards(),
		OpLog:   db.OpLog(),
		Tx:      db,
		Events:  broadcast.New(hub, logger),
		Clock:   clock.New(),
		IDs:     idgen.New(),
	}

	api := adapterhttp.New(adapterhttp.Deps{
		Boards:  service.NewBoard(deps),
		Columns: service.NewColumn(deps),
		Cards:   service.NewCard(deps),
		Sync:    service.NewSync(deps),
		Health:  func(r *http.Request) error { return db.Ping(r.Context()) },
		WS:      ws.NewHandler(hub, cfg.Token, logger),

		Logger:         logger,
		Token:          cfg.Token,
		CORSOrigin:     cfg.CORSOrigin,
		RequestTimeout: cfg.RequestTimeout,
	})

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: api.Handler(),
		// ReadHeaderTimeout is the one that matters against a slowloris; the
		// per-request timeout lives in the router, where the WebSocket can be
		// exempted from it.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	// Bind before announcing it. ListenAndServe would let the process log
	// "listening" and then fail to bind, which is a log line that lies at
	// exactly the moment someone is reading it to find out what went wrong.
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}
	logger.Info("listening",
		slog.String("addr", listener.Addr().String()),
		slog.String("cors_origin", cfg.CORSOrigin),
	)

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	// Give in-flight requests a moment, then stop regardless. Anything longer
	// just delays the container restart everyone is waiting on.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}
