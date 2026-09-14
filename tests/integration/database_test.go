//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/migrations"
)

// TestDatabaseLifecycle never reads .env.local or uses the development database.
func TestDatabaseLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:17.9-alpine",
		postgres.WithDatabase("ledger_test"), postgres.WithUsername("ledger"),
		postgres.WithPassword("test_only"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if cleanupErr := container.Terminate(cleanupCtx); cleanupErr != nil {
			t.Errorf("terminate postgres: %v", cleanupErr)
		}
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlDB := stdlib.OpenDBFromPool(db.Pool)
	defer func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close SQL adapter: %v", closeErr)
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil || version != 2 {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err = db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 1 {
		t.Fatalf("rollback to baseline version = %d, error = %v", version, err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 0 {
		t.Fatalf("rollback version = %d, error = %v", version, err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatalf("reapply after rollback: %v", err)
	}
}
