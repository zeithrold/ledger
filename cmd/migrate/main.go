// Command migrate applies explicit Ledger schema migrations.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/observability"
	"github.com/zeithrold/ledger/migrations"
)

func main() { os.Exit(observability.Run("ledger-migrate", run)) }

func run(parent context.Context, cfg config.Config, telemetry *observability.Runtime) error {
	goose.SetLogger(observability.GooseLogger{Logger: telemetry.Logger.With("component", "goose")})
	if len(os.Args) != 2 {
		return errors.New("usage: migrate <up|down|status|version>")
	}
	command := os.Args[1]
	switch command {
	case "up", "down", "status", "version":
	default:
		return errors.New("unsupported migration command")
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required for migrations")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	db, err := database.Open(ctx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()
	// Goose uses database/sql; this adapter shares the native pgx pool.
	pool := stdlib.OpenDBFromPool(db.Pool)
	defer func() {
		if closeErr := pool.Close(); closeErr != nil {
			slog.ErrorContext(parent, "close migration SQL adapter", "error", closeErr)
		}
	}()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.RunContext(parent, command, pool, ".")
}
