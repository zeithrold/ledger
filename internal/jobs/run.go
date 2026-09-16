package jobs

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/frankfurter"
	"github.com/zeithrold/ledger/internal/observability"
	"github.com/zeithrold/ledger/internal/rates"
)

// Run is the worker process lifecycle used by cmd/worker. It validates the
// worker-only configuration, opens the database, starts River and waits for a
// soft stop triggered by SIGINT/SIGTERM or by the parent context.
func Run(parent context.Context, cfg config.Config, telemetry *observability.Runtime) error {
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required for the worker")
	}
	if err := cfg.Rates.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	db, err := database.Open(openCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()
	provider := frankfurter.New(cfg.Rates.Endpoint, cfg.Rates.Providers, cfg.Rates.Timeout)
	service := rates.New(db, rates.Options{
		Provider:       provider,
		ProviderFilter: rates.Filter(cfg.Rates.Providers),
		RetentionDays:  cfg.Rates.RetentionDays,
	})
	client, err := NewClient(db, telemetry.Logger, service)
	if err != nil {
		return err
	}
	if err = client.Start(ctx); err != nil {
		return err
	}
	telemetry.Logger.InfoContext(parent, "worker starting", "event", "worker_start", "sync_hour_utc", ratesSyncHour, "sync_minute_utc", ratesSyncMinute, "retention_days", cfg.Rates.RetentionDays)
	<-client.Stopped()
	return nil
}
