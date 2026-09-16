package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/zeithrold/ledger/internal/rates"
)

// workerTimeout bounds one provider round trip plus its publish transaction.
const workerTimeout = 2 * time.Minute

// FetchRatesWorker synchronizes one daily market-rate snapshot.
type FetchRatesWorker struct {
	river.WorkerDefaults[FetchRatesArgs]
	service *rates.Service
	clock   rates.Clock
}

// Timeout bounds the job so a stuck provider call cannot hold a worker slot
// indefinitely; the client's soft stop cancels it on shutdown.
func (w *FetchRatesWorker) Timeout(*river.Job[FetchRatesArgs]) time.Duration { return workerTimeout }

// Work publishes the snapshot for the scheduled UTC date. A date that is
// already published is an idempotent no-op: the service never calls the
// provider and never rewrites a published batch.
func (w *FetchRatesWorker) Work(ctx context.Context, job *river.Job[FetchRatesArgs]) error {
	date := job.Args.ScheduledFor
	if date == "" {
		date = w.clock().UTC().Format(time.DateOnly)
	}
	result, err := w.service.SyncDaily(ctx, date)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "market rate snapshot synchronized", "event", "rates_sync", "snapshot_date", result.SnapshotDate, "published", result.Published, "rates", result.Rates)
	return nil
}
