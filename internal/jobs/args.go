// Package jobs wires River into the Ledger worker process: the durable job
// client, per-job observability, the daily market-rate schedule and the
// transactional enqueue seam business writes must use.
package jobs

// FetchRatesArgs schedules one daily market-rate snapshot.
type FetchRatesArgs struct {
	// ScheduledFor is the UTC snapshot date. An empty value lets the worker
	// derive it from its clock, which keeps a manual or replayed job usable.
	ScheduledFor string `json:"scheduled_for,omitempty"`
}

// Kind is the stable River job kind.
func (FetchRatesArgs) Kind() string { return "rates_fetch_daily" }
