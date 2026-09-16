//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/identity"
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
	if _, err = provider.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	who := auth.Identity{Issuer: "test", Subject: "pre-accounting-user"}
	identityService := identity.New(db)
	before, _, err := identityService.Bootstrap(ctx, who, identity.BootstrapInput{BaseCurrency: "USD", Timezone: "Asia/Shanghai", Locale: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	var labelsBefore string
	if err = db.Pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(c) ORDER BY code)::text FROM currencies c").Scan(&labelsBefore); err != nil {
		t.Fatal(err)
	}
	account, err := accounting.New(db).CreateAccount(ctx, before, before.DefaultBook.ID, "00000000-0000-4000-8000-000000000004", accounting.AccountInput{Name: "Existing USD", Kind: "bank", Currency: "USD", OpeningAmount: "1000", OpeningDate: "2026-09-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil || version != 6 {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	// The River queue schema and the public market-rate cache are applied by the
	// same explicit migration command and survive an upgrade of existing data.
	var infrastructureTables int
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('river_job','river_queue','river_leader','river_notification','market_rate_snapshots','market_rates')").Scan(&infrastructureTables); err != nil || infrastructureTables != 6 {
		t.Fatalf("worker infrastructure tables: %d %v", infrastructureTables, err)
	}
	var metadata []byte
	if err = db.Pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(c) ORDER BY code) FROM currencies c").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var stored []accounting.Currency
	if err = json.Unmarshal(metadata, &stored); err != nil || !slices.Equal(stored, accounting.Currencies()) {
		t.Fatalf("database metadata differs from the generated API catalog: %v", err)
	}
	after, err := identityService.Current(ctx, who)
	if err != nil || after.User.ID != before.User.ID || after.DefaultBook.ID != before.DefaultBook.ID || after.Preferences.Locale != before.Preferences.Locale {
		t.Fatalf("identity changed during accounting upgrade: %+v, %v", after, err)
	}
	categories, err := accounting.New(db).Categories(ctx, after, after.DefaultBook.ID)
	if err != nil || len(categories) != 16 {
		t.Fatalf("existing book categories after upgrade: %d, %v", len(categories), err)
	}
	if err = db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	var labelColumns int
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='currencies' AND column_name IN ('name_en','name_zh')").Scan(&labelColumns); err != nil || labelColumns != 0 {
		t.Fatalf("currency labels retained: %d %v", labelColumns, err)
	}
	accounts, err := accounting.New(db).Accounts(ctx, after, after.DefaultBook.ID)
	if err != nil || len(accounts) != 1 || accounts[0].ID != account.ID || accounts[0].Balance != "1000.00" {
		t.Fatalf("currency migration changed an existing balance: %+v %v", accounts, err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 5 {
		t.Fatalf("rollback to worker schema: %d %v", version, err)
	}
	var marketTables int
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('market_rate_snapshots','market_rates')").Scan(&marketTables); err != nil || marketTables != 0 {
		t.Fatalf("market-rate tables retained: %d %v", marketTables, err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 4 {
		t.Fatalf("rollback to currency metadata: %d %v", version, err)
	}
	var riverTables int
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name LIKE 'river_%'").Scan(&riverTables); err != nil || riverTables != 0 {
		t.Fatalf("River tables retained: %d %v", riverTables, err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 3 {
		t.Fatalf("rollback to accounting: %d %v", version, err)
	}
	var labelsAfter string
	if err = db.Pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(c) ORDER BY code)::text FROM currencies c").Scan(&labelsAfter); err != nil || labelsBefore != labelsAfter {
		t.Fatalf("currency rollback did not restore the exact catalog: %v", err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 2 {
		t.Fatalf("rollback to identity: %d %v", version, err)
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
