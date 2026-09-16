package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/rates"
)

const (
	ratesJobID      = "rates-fetch-daily"
	softStopTimeout = 30 * time.Second
	maxAttempts     = 5
)

// Client owns the worker's River client and the transactional enqueue seam.
type Client struct{ inner *river.Client[pgx.Tx] }

// NewClient builds the River client with one queue, one worker and the pinned
// daily schedule. It performs no database work and no provider call.
func NewClient(db *database.DB, logger *slog.Logger, service *rates.Service) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &FetchRatesWorker{service: service, clock: time.Now})
	inner, err := river.NewClient(riverpgxv5.New(db.Pool), &river.Config{
		Logger:  logger,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{river.NewPeriodicJob(RatesSchedule(), func() (river.JobArgs, *river.InsertOpts) {
			return FetchRatesArgs{ScheduledFor: time.Now().UTC().Format(time.DateOnly)}, nil
		}, &river.PeriodicJobOpts{ID: ratesJobID, RunOnStart: true})},
		Hooks: []rivertype.Hook{
			river.HookWorkBeginFunc(func(ctx context.Context, job *rivertype.JobRow) error {
				logger.InfoContext(ctx, "job started", append([]any{"event", "job_start"}, jobFields(job)...)...)
				return nil
			}),
			river.HookWorkEndFunc(func(ctx context.Context, job *rivertype.JobRow, err error) error {
				logger.InfoContext(ctx, "job finished", append([]any{"event", "job_finish", "failed", err != nil}, jobFields(job)...)...)
				// River replaces the job error with the hook's return value, so a
				// logging hook must never swallow a failure.
				return err
			}),
		},
		JobTimeout:      workerTimeout,
		SoftStopTimeout: softStopTimeout,
		MaxAttempts:     maxAttempts,
	})
	if err != nil {
		return nil, err
	}
	return &Client{inner: inner}, nil
}

// InsertTx enqueues args inside the caller's transaction, so the job exists
// only if the business write commits. This is the required enqueue path for any
// business write that schedules work.
func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (int64, error) {
	result, err := c.inner.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return 0, err
	}
	return result.Job.ID, nil
}

// Start begins job processing; the client stops softly when ctx is cancelled.
func (c *Client) Start(ctx context.Context) error { return c.inner.Start(ctx) }

// Stop requests a graceful stop with the configured soft-stop timeout.
func (c *Client) Stop(ctx context.Context) error { return c.inner.Stop(ctx) }

// Stopped reports when the client has finished stopping.
func (c *Client) Stopped() <-chan struct{} { return c.inner.Stopped() }

func jobFields(job *rivertype.JobRow) []any {
	return []any{"kind", job.Kind, "job_id", job.ID, "attempt", job.Attempt, "queue", job.Queue}
}
