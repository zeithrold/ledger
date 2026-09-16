-- +goose Up
-- Public market-rate cache. This is provider-owned reference data and is
-- deliberately not tenant-scoped: it never participates in postings, and
-- retention prunes whole snapshot batches without touching any applied rate
-- that a transaction already stored.
CREATE TABLE market_rate_snapshots (
 id uuid PRIMARY KEY,
 snapshot_date date NOT NULL,
 source text NOT NULL CHECK (source ~ '^[a-z0-9_-]{1,40}$'),
 source_version text NOT NULL CHECK (source_version ~ '^v[0-9]{1,4}$'),
 provider_filter text NOT NULL CHECK (length(btrim(provider_filter)) BETWEEN 1 AND 200),
 base_currency text NOT NULL CHECK (base_currency ~ '^[A-Z]{3}$'),
 status text NOT NULL CHECK (status IN ('pending','published')),
 fetched_at timestamptz NOT NULL,
 published_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (snapshot_date,source,source_version,provider_filter),
 CHECK ((status='published') = (published_at IS NOT NULL))
);
CREATE INDEX market_rate_snapshots_current_idx ON market_rate_snapshots (snapshot_date DESC,published_at DESC)
 WHERE status='published';

CREATE TABLE market_rates (
 id uuid PRIMARY KEY,
 snapshot_id uuid NOT NULL REFERENCES market_rate_snapshots(id) ON DELETE CASCADE,
 base_currency text NOT NULL CHECK (base_currency ~ '^[A-Z]{3}$'),
 quote_currency text NOT NULL CHECK (quote_currency ~ '^[A-Z]{3}$'),
 rate numeric NOT NULL CHECK (rate <> 'NaN'::numeric AND rate > 0 AND rate < 1000000000000000000),
 rate_date date NOT NULL,
 providers text[] NOT NULL DEFAULT '{}',
 UNIQUE (snapshot_id,base_currency,quote_currency),
 CHECK (base_currency <> quote_currency)
);
CREATE INDEX market_rates_pair_idx ON market_rates (base_currency,quote_currency);

-- +goose Down
DROP TABLE market_rates;
DROP TABLE market_rate_snapshots;
