# Exchange-rate snapshots

## Decision

Ledger uses [Frankfurter](https://frankfurter.dev/) v2 for public reference
exchange rates. A River worker fetches the latest rates once per day at
**17:10 UTC**, persists one immutable snapshot batch in PostgreSQL, and the HTTP
API reads only from the database. No request handler ever calls the provider,
and an unavailable pair never falls back to a rate of 1.

The default provider filter is the v2 blended feed, which blends every upstream
provider; `FX_PROVIDERS` may pin one or more provider keys instead. Each stored
rate keeps the contributing provider keys that Frankfurter reported, so a
snapshot's provenance stays reproducible.

Automatic reference rates are reference data only. Phase 2 already stores the
manual actual conversion ratio, source, date and both principals on immutable
journal postings, and that applied rate never depends on this cache.

## Snapshot model

Two tables in `00006_market_rates.sql` hold a shared, non-tenant cache:

- `market_rate_snapshots` identifies one batch by `(snapshot_date, source,
  source_version, provider_filter)` and carries `base_currency`, `status`
  (`pending` or `published`), `fetched_at` and `published_at`.
- `market_rates` stores one row per provider quote with an exact decimal `rate`,
  the provider's `rate_date`, and the contributing `providers` array.

Retention keeps the snapshot day and the preceding 29 days (30 calendar days by
default; `FX_RETENTION_DAYS` may set 1-365). Pruning uses `snapshot_date`, never
the upstream effective date, and runs only after a batch publishes.

## Publish protocol

`rates.SyncDaily` runs in the worker:

1. If the key is already published, return without contacting the provider. An
   ordinary retry therefore never rewrites a published batch.
2. Fetch `GET {endpoint}/v2/rates?base=EUR&expand=providers[&providers=...]`
   **before** opening any database transaction, with a bounded timeout and a
   1 MiB response cap.
3. Validate every row (uppercase ISO codes, positive exact decimal below 10^18,
   real `YYYY-MM-DD` date), deduplicate quotes and canonicalize provider keys.
4. In one transaction: insert the pending batch with `ON CONFLICT DO NOTHING`,
   lock it with `SELECT ... FOR UPDATE`, skip if a concurrent run published it,
   replace its rates, mark it published and prune expired batches.

A crash between the pending insert and the publish leaves a batch that readers
never see; the next attempt replaces it. Concurrent runs serialize on the
unique key plus the row lock, so exactly one publishes and the others report a
no-op. A failed fetch writes nothing, so the previous batch stays readable.

## Pair resolution

One snapshot stores the pivot base `EUR` and its quotes. A requested pair is
resolved with exact rational arithmetic:

- `direct` when the base is EUR: the provider's own decimal is served verbatim.
- `inverse` when the quote is EUR: the exact reciprocal.
- `cross` otherwise: `quote_leg / base_leg`, where each leg is EUR -> X.

The response carries the exact `numerator` and `denominator` plus a display
value rounded half-even to at most 18 significant digits. `rate_date` is the
earliest effective date among the legs actually used, so a holiday quote is not
relabelled as today's rate. `status` is `available`, `stale` (the snapshot date
is older than the current UTC day) or `unavailable` with `reason` `no_snapshot`
or `pair_unavailable`. A pair whose legs are missing from the newest complete
published batch falls back to an older complete batch before reporting
unavailable.

## Accounting is independent of cache retention

Each posted transaction preserves the applied exchange rate, effective date and
valuation amounts needed to reproduce its accounting. Deleting every market
snapshot must not change any transaction; an integration test proves it by
removing the whole cache and re-reading a cross-currency transfer. Imports older
than the cache window need an explicit supplied rate or a separately designed
asynchronous historical lookup; silently using today's rate is not allowed.
Long-term portfolio valuation and permanent historical market data stay outside
the current scope.

## API and operations

`GET /api/v1/exchange-rates?base=USD&quote=CNY` returns the documented
`MarketRate` object; see [API contract](api.md). It requires an authenticated
session, reads only published snapshots, and returns 400 for a malformed,
equal, or non-catalog currency code.

```sh
just migrate-up    # applies the River and market-rate schema
just run           # HTTP API only; it never fetches rates
just worker        # River worker; publishes on start and daily at 17:10 UTC
```

| Variable | Meaning |
| --- | --- |
| `FRANKFURTER_ENDPOINT` | Provider origin; defaults to `https://api.frankfurter.dev` |
| `FX_PROVIDERS` | Comma-separated provider keys; empty uses the blended feed |
| `FX_RETENTION_DAYS` | Snapshot days kept; defaults to 30 |
| `FX_HTTP_TIMEOUT` | Provider request timeout; defaults to 5s |

The worker validates these settings itself, so the API and migration commands
are unaffected by an invalid value. Automated tests use a controlled HTTP
server; a small explicit probe against the real Frankfurter service is separate
acceptance evidence and is not part of `just check`.
