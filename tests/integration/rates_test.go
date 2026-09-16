//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/apicontract"
	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/contracttest"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/frankfurter"
	"github.com/zeithrold/ledger/internal/httpserver"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/jobs"
	"github.com/zeithrold/ledger/internal/observability"
	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/internal/rates"
)

func newWorkerClient(t *testing.T, db *database.DB, service *rates.Service) *jobs.Client {
	t.Helper()
	client, err := jobs.NewClient(db, slog.New(slog.DiscardHandler), service)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// testClock is a race-safe injected clock for retention and staleness checks.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = value
}

// controlledProvider serves canned Frankfurter payloads and can be failed.
type controlledProvider struct {
	requests atomic.Int64
	failing  atomic.Bool
	body     string
}

func (p *controlledProvider) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/v2/rates") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if p.failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if _, err := w.Write([]byte(p.body)); err != nil {
			t.Errorf("write provider response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const sampleRates = `[
  {"date":"2026-09-16","base":"EUR","quote":"USD","rate":1.1,"providers":[{"key":"ECB","date":"2026-09-16","rate":1.1}]},
  {"date":"2026-09-16","base":"EUR","quote":"CNY","rate":7.7},
  {"date":"2026-09-15","base":"EUR","quote":"GBP","rate":0.85}
]`

func TestMarketRateLifecycle(t *testing.T) {
	db := phaseOneDB(t)
	clock := &testClock{now: time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)}
	provider := &controlledProvider{body: sampleRates}
	server := provider.start(t)
	service := rates.New(db, rates.Options{
		Provider:       frankfurter.New(server.URL, nil, 3*time.Second),
		ProviderFilter: rates.BlendedFilter,
		RetentionDays:  30,
		Clock:          clock.Now,
	})

	t.Run("publishes one batch and resolves direct, inverse and cross pairs", func(t *testing.T) {
		result, err := service.SyncDaily(t.Context(), "2026-09-16")
		if err != nil {
			t.Fatal(err)
		}
		if !result.Published || result.Rates != 3 {
			t.Fatalf("result %+v", result)
		}
		direct, err := service.Rate(t.Context(), "EUR", "USD")
		if err != nil {
			t.Fatal(err)
		}
		if direct.Status != rates.StatusAvailable || direct.Stale || direct.Derived != rates.DerivedDirect ||
			direct.Display != "1.1" || direct.Numerator != "11" || direct.Denominator != "10" ||
			direct.RateDate != "2026-09-16" || direct.SnapshotDate != "2026-09-16" || direct.Pivot != "EUR" ||
			direct.ProviderFilter != rates.BlendedFilter || direct.Source != "frankfurter" || direct.FetchedAt == "" {
			t.Fatalf("direct %+v", direct)
		}
		cross, err := service.Rate(t.Context(), "USD", "CNY")
		if err != nil || cross.Derived != rates.DerivedCross || cross.Display != "7" || cross.Numerator != "7" || cross.Denominator != "1" {
			t.Fatalf("cross %+v %v", cross, err)
		}
		inverse, err := service.Rate(t.Context(), "USD", "EUR")
		if err != nil || inverse.Derived != rates.DerivedInverse || inverse.Numerator != "10" || inverse.Denominator != "11" {
			t.Fatalf("inverse %+v %v", inverse, err)
		}
		// The reported effective date is the earliest date among the legs used.
		legacy, err := service.Rate(t.Context(), "USD", "GBP")
		if err != nil || legacy.Derived != rates.DerivedCross || legacy.RateDate != "2026-09-15" {
			t.Fatalf("legacy %+v %v", legacy, err)
		}
	})

	t.Run("ordinary retries never call the provider twice", func(t *testing.T) {
		before := provider.requests.Load()
		result, err := service.SyncDaily(t.Context(), "2026-09-16")
		if err != nil || result.Published || result.Rates != 0 {
			t.Fatalf("retry result %+v %v", result, err)
		}
		if after := provider.requests.Load(); after != before {
			t.Fatalf("retry issued %d extra provider requests", after-before)
		}
		var snapshots, stored int
		if err = db.Pool.QueryRow(t.Context(), "SELECT count(*) FROM market_rate_snapshots").Scan(&snapshots); err != nil || snapshots != 1 {
			t.Fatalf("snapshots %d %v", snapshots, err)
		}
		if err = db.Pool.QueryRow(t.Context(), "SELECT count(*) FROM market_rates").Scan(&stored); err != nil || stored != 3 {
			t.Fatalf("rates %d %v", stored, err)
		}
	})

	t.Run("concurrent same-day syncs publish exactly one batch", func(t *testing.T) {
		type outcome struct {
			result rates.Result
			err    error
		}
		results := make([]outcome, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() { results[i].result, results[i].err = service.SyncDaily(t.Context(), "2026-09-17") })
		}
		wg.Wait()
		published := 0
		for _, outcome := range results {
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			if outcome.result.Published {
				published++
			}
		}
		if published != 1 {
			t.Fatalf("published %d batches", published)
		}
		var snapshots int
		if err := db.Pool.QueryRow(t.Context(), "SELECT count(*) FROM market_rate_snapshots WHERE snapshot_date='2026-09-17'").Scan(&snapshots); err != nil || snapshots != 1 {
			t.Fatalf("snapshot rows %d %v", snapshots, err)
		}
	})

	t.Run("provider failure keeps the previous batch usable", func(t *testing.T) {
		provider.failing.Store(true)
		defer provider.failing.Store(false)
		if _, err := service.SyncDaily(t.Context(), "2026-09-18"); err == nil {
			t.Fatal("accepted a failing provider")
		}
		var failed int
		if err := db.Pool.QueryRow(t.Context(), "SELECT count(*) FROM market_rate_snapshots WHERE snapshot_date='2026-09-18'").Scan(&failed); err != nil || failed != 0 {
			t.Fatalf("failed batch rows %d %v", failed, err)
		}
		stale, err := service.Rate(t.Context(), "EUR", "USD")
		if err != nil || stale.Display != "1.1" {
			t.Fatalf("previous batch unusable: %+v %v", stale, err)
		}
	})

	t.Run("staleness follows the injected UTC clock", func(t *testing.T) {
		clock.Set(time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC))
		stale, err := service.Rate(t.Context(), "EUR", "USD")
		if err != nil || stale.Status != rates.StatusStale || !stale.Stale || stale.LatestSnapshotDate != "2026-09-17" || stale.SnapshotDate != "2026-09-17" {
			t.Fatalf("stale %+v %v", stale, err)
		}
	})

	t.Run("invalid parameters are rejected without a snapshot lookup", func(t *testing.T) {
		for _, tc := range []struct{ base, quote string }{
			{"usd", "EUR"}, {"USD", "eur"}, {"USD", "USD"}, {"ABC", "EUR"}, {"USD", "ABC"}, {"", "EUR"}, {"US", "EUR"},
		} {
			_, err := service.Rate(t.Context(), tc.base, tc.quote)
			expectKind(t, err, problem.InvalidRequest)
		}
		// A cancelled context surfaces the database failure instead of a rate.
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := service.Rate(cancelled, "EUR", "USD"); err == nil {
			t.Fatal("served a rate with a cancelled context")
		}
	})

	t.Run("rolling retention keeps thirty snapshot days", func(t *testing.T) {
		if _, execErr := db.Pool.Exec(t.Context(), "DELETE FROM market_rate_snapshots"); execErr != nil {
			t.Fatal(execErr)
		}
		start := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
		for offset := 35; offset >= 0; offset-- {
			date := start.AddDate(0, 0, -offset).Format(time.DateOnly)
			if _, err := service.SyncDaily(t.Context(), date); err != nil {
				t.Fatal(err)
			}
		}
		var snapshots int
		var oldest, newest string
		if err := db.Pool.QueryRow(t.Context(), "SELECT count(*),min(snapshot_date)::text,max(snapshot_date)::text FROM market_rate_snapshots WHERE status='published'").Scan(&snapshots, &oldest, &newest); err != nil {
			t.Fatal(err)
		}
		if snapshots != 30 || oldest != "2026-08-18" || newest != "2026-09-16" {
			t.Fatalf("retention kept %d rows %s..%s", snapshots, oldest, newest)
		}
		if _, err := service.Rate(t.Context(), "USD", "GBP"); err != nil {
			t.Fatal("retention removed a still-required leg")
		}
	})

	t.Run("a future-dated job cannot prune the recent cache", func(t *testing.T) {
		if _, err := service.SyncDaily(t.Context(), "2026-11-15"); err != nil {
			t.Fatal(err)
		}
		if countRows(t, db, "SELECT count(*) FROM market_rate_snapshots WHERE snapshot_date='2026-09-16'") != 1 {
			t.Fatal("a future-dated snapshot pruned the recent cache")
		}
		if countRows(t, db, "SELECT count(*) FROM market_rate_snapshots") == 0 {
			t.Fatal("a future-dated snapshot emptied the cache")
		}
	})

	t.Run("injected publish failures never expose a partial batch", func(t *testing.T) {
		if _, err := service.SyncDaily(t.Context(), "2026-09-19"); err != nil {
			t.Fatal(err)
		}
		execute(t, db, `CREATE FUNCTION reject_market_rate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$`)
		execute(t, db, `CREATE TRIGGER reject_market_rate BEFORE INSERT ON market_rates FOR EACH ROW EXECUTE FUNCTION reject_market_rate()`)
		if _, err := service.SyncDaily(t.Context(), "2026-09-20"); err == nil {
			t.Fatal("published through an injected insert failure")
		}
		execute(t, db, `DROP TRIGGER reject_market_rate ON market_rates`)
		execute(t, db, `CREATE TRIGGER reject_market_rate BEFORE UPDATE ON market_rate_snapshots FOR EACH ROW EXECUTE FUNCTION reject_market_rate()`)
		if _, err := service.SyncDaily(t.Context(), "2026-09-20"); err == nil {
			t.Fatal("published through an injected update failure")
		}
		execute(t, db, `DROP TRIGGER reject_market_rate ON market_rate_snapshots`)
		// A committed pending batch forces the replacement path through its delete.
		execute(t, db, `INSERT INTO market_rate_snapshots(id,snapshot_date,source,source_version,provider_filter,base_currency,status,fetched_at) VALUES (gen_random_uuid(),'2026-09-21','frankfurter','v2','blended','EUR','pending',now())`)
		execute(t, db, `INSERT INTO market_rates(id,snapshot_id,base_currency,quote_currency,rate,rate_date) SELECT gen_random_uuid(),id,'EUR','USD',1.1,'2026-09-16' FROM market_rate_snapshots WHERE snapshot_date='2026-09-21'`)
		execute(t, db, `CREATE TRIGGER reject_market_rate BEFORE DELETE ON market_rates FOR EACH ROW EXECUTE FUNCTION reject_market_rate()`)
		if _, err := service.SyncDaily(t.Context(), "2026-09-21"); err == nil {
			t.Fatal("published through an injected delete failure")
		}
		execute(t, db, `DROP TRIGGER reject_market_rate ON market_rates`)
		execute(t, db, `DROP FUNCTION reject_market_rate()`)
		if countRows(t, db, "SELECT count(*) FROM market_rate_snapshots WHERE snapshot_date='2026-09-20'") != 0 {
			t.Fatal("a failed batch became visible")
		}
		if countRows(t, db, "SELECT count(*) FROM market_rate_snapshots WHERE snapshot_date='2026-09-21' AND status='published'") != 0 {
			t.Fatal("a pending batch was published through a failure")
		}
		rate, err := service.Rate(t.Context(), "EUR", "USD")
		if err != nil || rate.Display == "" || rate.SnapshotDate == "" {
			t.Fatalf("published cache unusable after injected failures: %+v %v", rate, err)
		}
		execute(t, db, "DELETE FROM market_rate_snapshots")
	})

	t.Run("market cache cleanup never changes applied transaction rates", func(t *testing.T) {
		identities := identity.New(db)
		actor, _, err := identities.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "market-rate"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "UTC", Locale: "en"})
		if err != nil {
			t.Fatal(err)
		}
		book := actor.DefaultBook.ID
		ledger := accounting.New(db)
		usd, err := ledger.CreateAccount(t.Context(), actor, book, uuid.NewString(), accounting.AccountInput{Name: "USD", Kind: "bank", Currency: "USD", OpeningAmount: "1000", OpeningDate: "2026-09-01"})
		if err != nil {
			t.Fatal(err)
		}
		cny, err := ledger.CreateAccount(t.Context(), actor, book, uuid.NewString(), accounting.AccountInput{Name: "CNY", Kind: "bank", Currency: "CNY", OpeningAmount: "0", OpeningDate: "2026-09-01"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := ledger.CreateTransaction(t.Context(), actor, book, uuid.NewString(), accounting.CreateInput{Entry: accounting.EntryInput{Kind: "transfer", OccurredOn: "2026-09-10", AccountID: usd.ID, Amount: "100", ToAccountID: cny.ID, ToAmount: "700"}})
		if err != nil {
			t.Fatal(err)
		}
		id, err := uuid.Parse(result.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		before, err := ledger.Transaction(t.Context(), actor, book, id)
		if err != nil || before.ExchangeRate == nil {
			t.Fatalf("applied rate missing: %+v %v", before.ExchangeRate, err)
		}
		// Simulate every market snapshot expiring, including the whole cache.
		if _, execErr := db.Pool.Exec(t.Context(), "DELETE FROM market_rate_snapshots"); execErr != nil {
			t.Fatal(execErr)
		}
		unavailable, err := service.Rate(t.Context(), "USD", "CNY")
		if err != nil || unavailable.Status != rates.StatusUnavailable || unavailable.Reason != rates.ReasonNoSnapshot || unavailable.Display != "" {
			t.Fatalf("cache still served a rate: %+v %v", unavailable, err)
		}
		after, err := ledger.Transaction(t.Context(), actor, book, id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before.ExchangeRate, after.ExchangeRate) || !reflect.DeepEqual(before.Journals, after.Journals) {
			t.Fatalf("applied accounting changed after cache cleanup:\n%+v\n%+v", before.ExchangeRate, after.ExchangeRate)
		}
	})
}

func TestMarketRateHTTP(t *testing.T) {
	db := phaseOneDB(t)
	clock := &testClock{now: time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)}
	provider := &controlledProvider{body: sampleRates}
	server := provider.start(t)
	service := rates.New(db, rates.Options{Provider: frankfurter.New(server.URL, nil, 3*time.Second), Clock: clock.Now})
	identities := identity.New(db)
	if _, _, err := identities.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "rates-http"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "UTC", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	router, err := httpserver.New(db, httpserver.Dependencies{Backend: identities, Accounting: accounting.New(db), Rates: service, Verifier: localVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(t *testing.T, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer rates-http")
		req.Header.Set(httpserver.VersionHeader, apicontract.Version())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		contracttest.Response(t, req, w)
		return w
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", w.Body, err)
		}
		return body
	}

	t.Run("an empty cache reports unavailable instead of a rate", func(t *testing.T) {
		w := get(t, "/api/v1/exchange-rates?base=USD&quote=CNY")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d %s", w.Code, w.Body)
		}
		body := decode(t, w)
		if body["status"] != "unavailable" || body["reason"] != "no_snapshot" || body["rate"] != nil || body["latest_snapshot_date"] != nil {
			t.Fatalf("body %s", w.Body)
		}
	})

	t.Run("a published snapshot is served with provenance", func(t *testing.T) {
		if _, err := service.SyncDaily(t.Context(), "2026-09-16"); err != nil {
			t.Fatal(err)
		}
		w := get(t, "/api/v1/exchange-rates?base=USD&quote=CNY")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d %s", w.Code, w.Body)
		}
		body := decode(t, w)
		if body["status"] != "available" || body["derived"] != "cross" || body["rate"] != "7" ||
			body["rate_date"] != "2026-09-16" || body["snapshot_date"] != "2026-09-16" ||
			body["pivot"] != "EUR" || body["provider_filter"] != "blended" || body["stale"] != false || body["reason"] != nil {
			t.Fatalf("body %s", w.Body)
		}
	})

	t.Run("an unsupported pair stays explicitly unavailable", func(t *testing.T) {
		w := get(t, "/api/v1/exchange-rates?base=USD&quote=CHF")
		body := decode(t, w)
		if w.Code != http.StatusOK || body["status"] != "unavailable" || body["reason"] != "pair_unavailable" || body["latest_snapshot_date"] != "2026-09-16" {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})

	t.Run("invalid and missing parameters are rejected", func(t *testing.T) {
		for _, path := range []string{
			"/api/v1/exchange-rates?base=usd&quote=CNY",
			"/api/v1/exchange-rates?base=USD&quote=usd",
			"/api/v1/exchange-rates?base=USD&quote=USD",
			"/api/v1/exchange-rates?base=ABC&quote=CNY",
			"/api/v1/exchange-rates?base=USD",
			"/api/v1/exchange-rates",
		} {
			if w := get(t, path); w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status %d %s", path, w.Code, w.Body)
			}
		}
	})

	t.Run("the endpoint is unavailable without a rate service", func(t *testing.T) {
		plain, err := httpserver.New(db, httpserver.Dependencies{Backend: identities, Verifier: localVerifier{}})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/exchange-rates?base=USD&quote=CNY", nil)
		req.Header.Set("Authorization", "Bearer rates-http")
		req.Header.Set(httpserver.VersionHeader, apicontract.Version())
		w := httptest.NewRecorder()
		plain.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d %s", w.Code, w.Body)
		}
	})
}

func TestWorkerRunAndTransactionalEnqueue(t *testing.T) {
	db := phaseOneDB(t)
	clock := &testClock{now: time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)}
	provider := &controlledProvider{body: sampleRates}
	server := provider.start(t)
	service := rates.New(db, rates.Options{Provider: frankfurter.New(server.URL, nil, 3*time.Second), Clock: clock.Now})
	if _, err := jobs.NewClient(db, nil, service); err != nil {
		t.Fatal(err)
	}
	client := newWorkerClient(t, db, service)

	t.Run("a business write and its job commit or roll back together", func(t *testing.T) {
		args := jobs.FetchRatesArgs{ScheduledFor: "2026-08-01"}
		tx, err := db.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		rolledBack, err := client.InsertTx(t.Context(), tx, args, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		if countRows(t, db, fmt.Sprintf("SELECT count(*) FROM river_job WHERE id=%d", rolledBack)) != 0 {
			t.Fatal("rolled-back transaction enqueued a job")
		}
		if _, err = client.InsertTx(t.Context(), tx, args, nil); err == nil {
			t.Fatal("enqueued into a finished transaction")
		}
		tx, err = db.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		committed, err := client.InsertTx(t.Context(), tx, args, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if countRows(t, db, fmt.Sprintf("SELECT count(*) FROM river_job WHERE id=%d", committed)) != 1 {
			t.Fatal("committed transaction lost its job")
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	t.Run("the worker publishes on start", func(t *testing.T) {
		if err := client.Start(ctx); err != nil {
			t.Fatal(err)
		}
		published := false
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
			var count int
			if queryErr := db.Pool.QueryRow(ctx, "SELECT count(*) FROM market_rate_snapshots WHERE status='published'").Scan(&count); queryErr != nil {
				t.Fatal(queryErr)
			}
			if count >= 1 {
				published = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !published {
			t.Fatal("worker did not publish a snapshot")
		}
	})

	t.Run("a failing job surfaces as a retry, never a silent success", func(t *testing.T) {
		provider.failing.Store(true)
		defer provider.failing.Store(false)
		tx, err := db.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		id, err := client.InsertTx(t.Context(), tx, jobs.FetchRatesArgs{ScheduledFor: "2026-08-02"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(60 * time.Second)
		state, attempts := "", 0
		for time.Now().Before(deadline) {
			if err := db.Pool.QueryRow(t.Context(), "SELECT state, coalesce(array_length(errors,1),0) FROM river_job WHERE id=$1", id).Scan(&state, &attempts); err != nil {
				t.Fatal(err)
			}
			if attempts > 0 && state != "completed" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if attempts == 0 || state == "completed" {
			t.Fatalf("failing job was treated as success: state=%s recorded_errors=%d", state, attempts)
		}
		if countRows(t, db, "SELECT count(*) FROM market_rate_snapshots WHERE snapshot_date='2026-08-02'") != 0 {
			t.Fatal("failing job published a snapshot")
		}
	})

	t.Run("the worker stops gracefully", func(t *testing.T) {
		cancel()
		if err := client.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-client.Stopped():
		case <-time.After(30 * time.Second):
			t.Fatal("worker did not stop gracefully")
		}
	})
}

// TestWorkerProcessRun exercises the real worker lifecycle, including the daily
// schedule registration and the signal-driven soft stop.
func TestWorkerProcessRun(t *testing.T) {
	db := phaseOneDB(t)
	provider := &controlledProvider{body: sampleRates}
	server := provider.start(t)
	cfg := config.Config{
		Environment: "stage", LogLevel: slog.LevelInfo, DocsEnabled: false, HTTPAddr: "127.0.0.1:0",
		DatabaseURL: db.Pool.Config().ConnString(),
		Rates:       config.Rates{Endpoint: server.URL, RetentionDays: 30, Timeout: 3 * time.Second},
	}
	telemetry, err := observability.New(t.Context(), cfg, "ledger-worker-test", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- jobs.Run(ctx, cfg, telemetry) }()
	published := false
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		var count int
		if queryErr := db.Pool.QueryRow(t.Context(), "SELECT count(*) FROM market_rate_snapshots WHERE status='published'").Scan(&count); queryErr != nil {
			t.Fatal(queryErr)
		}
		if count >= 1 {
			published = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !published {
		t.Fatal("worker process did not publish a snapshot")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("worker process did not stop gracefully")
	}
}
