-- name: CurrentMarketRateSnapshot :one
-- Newest published snapshot that carries every leg the requested pair needs, so
-- a provider coverage change falls back to an older complete batch instead of
-- serving a partial conversion.
SELECT s.* FROM market_rate_snapshots s
WHERE s.status='published' AND s.base_currency=$1
AND EXISTS (SELECT 1 FROM market_rates r WHERE r.snapshot_id=s.id AND r.quote_currency=$2)
AND (sqlc.narg(leg2)::text IS NULL OR EXISTS (SELECT 1 FROM market_rates r2 WHERE r2.snapshot_id=s.id AND r2.quote_currency=sqlc.narg(leg2)::text))
ORDER BY s.snapshot_date DESC,s.published_at DESC
LIMIT 1;
-- name: MarketSnapshotRates :many
SELECT * FROM market_rates WHERE snapshot_id=$1 AND quote_currency=ANY(sqlc.arg(quotes)::text[]) ORDER BY quote_currency;
-- name: LatestPublishedMarketSnapshotDate :one
SELECT max(snapshot_date)::date FROM market_rate_snapshots WHERE status='published';
-- name: HasPublishedMarketRateSnapshot :one
SELECT id FROM market_rate_snapshots
WHERE snapshot_date=$1 AND source=$2 AND source_version=$3 AND provider_filter=$4 AND status='published';
-- name: FindMarketRateSnapshot :one
SELECT * FROM market_rate_snapshots
WHERE snapshot_date=$1 AND source=$2 AND source_version=$3 AND provider_filter=$4
FOR UPDATE;
-- name: InsertMarketRateSnapshot :exec
INSERT INTO market_rate_snapshots(id,snapshot_date,source,source_version,provider_filter,base_currency,status,fetched_at)
VALUES ($1,$2,$3,$4,$5,$6,'pending',$7)
ON CONFLICT (snapshot_date,source,source_version,provider_filter) DO NOTHING;
-- name: DeleteMarketRates :exec
DELETE FROM market_rates WHERE snapshot_id=$1;
-- name: InsertMarketRates :copyfrom
INSERT INTO market_rates(id,snapshot_id,base_currency,quote_currency,rate,rate_date,providers)
VALUES ($1,$2,$3,$4,$5,$6,$7);
-- name: PublishMarketRateSnapshot :exec
UPDATE market_rate_snapshots SET status='published',published_at=$2 WHERE id=$1;
-- name: PruneMarketRateSnapshots :exec
DELETE FROM market_rate_snapshots WHERE snapshot_date < $1;
