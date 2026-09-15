// Package database manages PostgreSQL connections and typed queries.
package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zeithrold/ledger/internal/database/sqlgen"
)

// DB holds the PostgreSQL pool and generated queries. Goose owns the schema.
type DB struct {
	Pool    *pgxpool.Pool
	Queries *sqlgen.Queries
}

// Open creates and verifies a pool without applying migrations.
func Open(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL configuration")
	}
	cfg.ConnConfig.Tracer = queryTracer{}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("initialize PostgreSQL pool failed")
	}
	db := &DB{Pool: pool, Queries: sqlgen.New(pool)}
	if err := db.PingContext(ctx); err != nil {
		pool.Close()
		return nil, errors.New("PostgreSQL unavailable; check DATABASE_URL and connectivity")
	}
	return db, nil
}

// PingContext verifies connectivity using a generated SQL query.
func (db *DB) PingContext(ctx context.Context) error {
	_, err := db.Queries.CheckConnection(ctx)
	return err
}

// Close releases the connection pool.
func (db *DB) Close() { db.Pool.Close() }
