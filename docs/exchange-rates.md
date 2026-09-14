# Exchange-rate snapshots

## Decision

Use [Frankfurter](https://frankfurter.dev/) for reference exchange rates. A River periodic job will fetch rates once per day and persist daily snapshots in PostgreSQL. Request handlers read the database and never fetch rates synchronously.

The current repository only records this design. No exchange-rate tables, jobs, or API calls are implemented yet.

## Proposed storage and retention

Use a shared public market-data cache, separate from tenant-owned accounting data. Retain a rolling 30 calendar days of snapshots rather than storing the market cache forever. Treat 30 days as the initial configurable interpretation of one month.

Suggested snapshot fields:

- `snapshot_date`: the scheduler's UTC date.
- `base_currency`, `quote_currency`, and an exact decimal `rate`.
- `rate_date`: the effective date reported by Frankfurter for this rate.
- `fetched_at`, provider attribution, and the provider/filter configuration.
- A batch identifier and completion state so readers never observe a partial refresh.

For a snapshot date D, keep D and the preceding 29 days. Use snapshot date for retention, not the upstream effective date. Only prune after a successful batch publication. Uniqueness by snapshot date, pair and provider configuration makes retries idempotent. Do not overwrite a published daily batch on an ordinary retry.

Weekend or holiday data can have an earlier effective date. Preserve it instead of relabeling it as today's market rate. If refresh fails, retain the last successful snapshot and expose its date/staleness. If no suitable snapshot exists, return an explicit unavailable state; do not silently call the upstream API or substitute a rate of 1.

The daily execution time and provider filters will be finalized during worker implementation. The v2 API offers blended rates by default and provider filtering; persist that choice so rate provenance is reproducible.

## Accounting is independent of cache retention

Each posted transaction must preserve the applied exchange rate, effective date/provenance, original amounts and valuation amounts needed to reproduce its accounting. Do not rely solely on a foreign key to an expiring cache row. Removing old market snapshots must not delete or change historical transactions.

Imports older than the cache window need an explicit supplied rate or a separately designed asynchronous historical lookup. Do not silently use today's rate. Long-term portfolio valuation and permanent historical market data are outside the current scope.

## Future acceptance cases

- Repeated daily jobs cannot duplicate or partially publish a batch.
- Effective market date can differ from the snapshot date.
- Failed refresh leaves the prior snapshot usable and marked stale.
- The rolling 30-day boundary is exact and testable with an injected clock.
- Cleanup leaves transaction-applied rates intact.
- No request handler performs an upstream FX call.
