// Command api serves the Ledger HTTP API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/httpserver"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/observability"
)

func main() { os.Exit(observability.Run("ledger-api", run)) }

func run(parent context.Context, cfg config.Config, telemetry *observability.Runtime) error {
	gin.SetMode(cfg.GinMode)
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	var pinger httpserver.Pinger
	deps := httpserver.Dependencies{Telemetry: telemetry, DocsEnabled: cfg.DocsEnabled}
	if cfg.DatabaseURL != "" {
		verifier, authErr := auth.New(cfg.Clerk)
		if authErr != nil {
			return authErr
		}
		connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		db, connectErr := database.Open(connectCtx, cfg.DatabaseURL)
		cancel()
		if connectErr != nil {
			return connectErr
		}
		defer db.Close()
		pinger = db
		deps = httpserver.Dependencies{DocsEnabled: cfg.DocsEnabled, Backend: identity.New(db), Verifier: verifier, Telemetry: telemetry}
	}
	router, err := httpserver.New(pinger, deps) //nolint:contextcheck // Middleware derives context from each HTTP request, not process startup.
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: cfg.HTTPAddr, Handler: router,
		ErrorLog:          slog.NewLogLogger(telemetry.Logger.With("component", "net/http").Handler(), slog.LevelError),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	slog.InfoContext(ctx, "HTTP server starting", "event", "http_start", "address", cfg.HTTPAddr, "database_enabled", pinger != nil)
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
