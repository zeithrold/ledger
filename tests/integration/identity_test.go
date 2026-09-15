//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/contracttest"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/httpserver"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/migrations"
)

func phaseOneDB(t *testing.T) *database.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:17.9-alpine", postgres.WithDatabase("ledger_test"), postgres.WithUsername("ledger"), postgres.WithPassword("test_only"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
		defer stop()
		if cleanupErr := container.Terminate(cleanup); cleanupErr != nil {
			t.Errorf("container cleanup: %v", cleanupErr)
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
	t.Cleanup(db.Close)
	adapter := stdlib.OpenDBFromPool(db.Pool)
	t.Cleanup(func() {
		if closeErr := adapter.Close(); closeErr != nil {
			t.Errorf("adapter cleanup: %v", closeErr)
		}
	})
	provider, err := goose.NewProvider(goose.DialectPostgres, adapter, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func execute(t *testing.T, db *database.DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, db *database.DB, sql string) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(t.Context(), sql).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func expectKind(t *testing.T, err error, kind problem.Kind) {
	t.Helper()
	var p *problem.Error
	if !errors.As(err, &p) || p.Kind != kind {
		t.Fatalf("error=%v, want %s", err, kind)
	}
}

type localVerifier struct{}

func (localVerifier) Verify(_ context.Context, token string) (auth.Identity, error) {
	return auth.Identity{Issuer: "test", Subject: token}, nil
}

func TestPhaseOne(t *testing.T) {
	db := phaseOneDB(t)
	s := identity.New(db)
	t.Run("rollback does not consume first administrator", func(t *testing.T) {
		execute(t, db, `CREATE FUNCTION reject_preferences() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$`)
		execute(t, db, `CREATE TRIGGER reject_preferences BEFORE INSERT ON user_preferences FOR EACH ROW EXECUTE FUNCTION reject_preferences()`)
		_, _, err := s.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "first"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "UTC", Locale: "en"})
		if err == nil {
			t.Fatal("injected failure succeeded")
		}
		for _, query := range []string{"SELECT count(*) FROM users", "SELECT count(*) FROM user_identities", "SELECT count(*) FROM tenants", "SELECT count(*) FROM tenant_members", "SELECT count(*) FROM books", "SELECT count(*) FROM personal_tenant_bindings", "SELECT count(*) FROM instance_state WHERE admin_user_id IS NOT NULL"} {
			if countRows(t, db, query) != 0 {
				t.Fatalf("partial provisioning: %s", query)
			}
		}
		execute(t, db, "DROP TRIGGER reject_preferences ON user_preferences")
		execute(t, db, "DROP FUNCTION reject_preferences()")
	})
	var admin identity.Context
	t.Run("different identities race for one administrator", func(t *testing.T) {
		const workers = 12
		contexts := make([]identity.Context, workers)
		failures := make([]error, workers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range workers {
			wg.Go(func() {
				<-start
				contexts[i], _, failures[i] = s.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: fmt.Sprintf("race-%d", i)}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "UTC", Locale: "en"})
			})
		}
		close(start)
		wg.Wait()
		admins := 0
		for i, err := range failures {
			if err != nil {
				t.Fatal(err)
			}
			if contexts[i].InstanceRole == "admin" {
				admins++
				admin = contexts[i]
			}
		}
		if admins != 1 || countRows(t, db, "SELECT count(*) FROM users") != workers {
			t.Fatalf("admins=%d", admins)
		}
	})
	var personal identity.Context
	t.Run("same identity concurrent bootstrap is idempotent", func(t *testing.T) {
		const workers = 16
		results := make([]identity.Context, workers)
		created := make([]bool, workers)
		failures := make([]error, workers)
		var wg sync.WaitGroup
		for i := range workers {
			wg.Go(func() {
				results[i], created[i], failures[i] = s.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "same"}, identity.BootstrapInput{BaseCurrency: "USD", Timezone: "UTC", Locale: "en-US"})
			})
		}
		wg.Wait()
		newCount := 0
		for i, err := range failures {
			if err != nil {
				t.Fatal(err)
			}
			if created[i] {
				newCount++
			}
			if results[i].User.ID != results[0].User.ID || results[i].DefaultBook.ID != results[0].DefaultBook.ID {
				t.Fatal("duplicate personal space")
			}
		}
		if newCount != 1 {
			t.Fatalf("created %d times", newCount)
		}
		personal = results[0]
		repeated, wasCreated, err := s.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "same"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "Asia/Shanghai", Locale: "zh-CN"})
		if err != nil || wasCreated || repeated.DefaultBook.BaseCurrency != "USD" || repeated.Preferences.Locale != "en-US" {
			t.Fatalf("retry changed preferences: %v %v", repeated, err)
		}
		otherIssuer, _, err := s.Bootstrap(t.Context(), auth.Identity{Issuer: "other", Subject: "same"}, identity.BootstrapInput{BaseCurrency: "EUR", Timezone: "UTC", Locale: "en"})
		if err != nil || otherIssuer.User.ID == personal.User.ID {
			t.Fatal("issuer not part of identity")
		}
	})
	t.Run("authorization membership and tenant isolation", func(t *testing.T) {
		_, err := s.Book(t.Context(), admin, personal.DefaultBook.ID)
		expectKind(t, err, problem.NotFound)
		_, err = s.Book(t.Context(), personal, admin.DefaultBook.ID)
		expectKind(t, err, problem.NotFound)
		books, err := s.Books(t.Context(), personal)
		if err != nil || len(books) != 1 || books[0].ID != personal.DefaultBook.ID {
			t.Fatal("book list isolation failed")
		}
		_, err = s.Instance(t.Context(), personal)
		expectKind(t, err, problem.AccessDenied)
		_, err = s.Users(t.Context(), personal, nil, 50)
		expectKind(t, err, problem.AccessDenied)
		_, err = s.SetStatus(t.Context(), personal, admin.User.ID, identity.StatusInput{Status: "disabled", Reason: "test"})
		expectKind(t, err, problem.AccessDenied)
		execute(t, db, "UPDATE tenant_members SET status = 'disabled' WHERE user_id = $1", personal.User.ID)
		_, err = s.Current(t.Context(), auth.Identity{Issuer: "test", Subject: "same"})
		expectKind(t, err, problem.AccessDenied)
		execute(t, db, "UPDATE tenant_members SET status = 'active' WHERE user_id = $1", personal.User.ID)
		execute(t, db, "UPDATE tenants SET status = 'disabled' WHERE id = $1", personal.Tenant.ID)
		_, err = s.Current(t.Context(), auth.Identity{Issuer: "test", Subject: "same"})
		expectKind(t, err, problem.AccessDenied)
		execute(t, db, "UPDATE tenants SET status = 'active' WHERE id = $1", personal.Tenant.ID)
		_, err = db.Pool.Exec(t.Context(), "UPDATE personal_tenant_bindings SET default_book_id = $1 WHERE user_id = $2", admin.DefaultBook.ID, personal.User.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("cross tenant foreign key: %v", err)
		}
	})
	t.Run("status changes audit and retry cannot reactivate", func(t *testing.T) {
		_, err := s.SetStatus(t.Context(), admin, admin.User.ID, identity.StatusInput{Status: "disabled", Reason: "test"})
		expectKind(t, err, problem.AccessDenied)
		_, err = s.SetStatus(t.Context(), admin, personal.User.ID, identity.StatusInput{Status: "disabled", Reason: "test suspension"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Current(t.Context(), auth.Identity{Issuer: "test", Subject: "same"})
		expectKind(t, err, problem.UserDisabled)
		_, _, err = s.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "same"}, identity.BootstrapInput{})
		expectKind(t, err, problem.UserDisabled)
		_, err = s.SetStatus(t.Context(), admin, personal.User.ID, identity.StatusInput{Status: "active", Reason: "restored"})
		if err != nil {
			t.Fatal(err)
		}
		current, err := s.Current(t.Context(), auth.Identity{Issuer: "test", Subject: "same"})
		if err != nil || current.Tenant.ID != personal.Tenant.ID {
			t.Fatal("restore changed space")
		}
		if countRows(t, db, "SELECT count(*) FROM admin_audit_events") != 2 {
			t.Fatal("audit count")
		}
		execute(t, db, `CREATE FUNCTION reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$`)
		execute(t, db, `CREATE TRIGGER reject_audit BEFORE INSERT ON admin_audit_events FOR EACH ROW EXECUTE FUNCTION reject_audit()`)
		_, err = s.SetStatus(t.Context(), admin, personal.User.ID, identity.StatusInput{Status: "disabled", Reason: "must rollback"})
		if err == nil {
			t.Fatal("audit failure ignored")
		}
		if _, err = s.Current(t.Context(), auth.Identity{Issuer: "test", Subject: "same"}); err != nil {
			t.Fatal("status not rolled back")
		}
		execute(t, db, "DROP TRIGGER reject_audit ON admin_audit_events")
		execute(t, db, "DROP FUNCTION reject_audit()")
	})
	t.Run("preferences pagination and instance", func(t *testing.T) {
		dark := "dark"
		prefs, err := s.UpdatePreferences(t.Context(), personal, identity.PreferencesPatch{Theme: &dark})
		if err != nil || prefs.Locale != "en-US" || prefs.Theme != "dark" {
			t.Fatal("patch lost preferences")
		}
		state, err := s.Instance(t.Context(), admin)
		if err != nil || state.AdminUserID != admin.User.ID || state.InitializedAt.IsZero() {
			t.Fatal("instance state")
		}
		seen := map[uuid.UUID]bool{}
		var cursor *uuid.UUID
		for {
			page, pageErr := s.Users(t.Context(), admin, cursor, 3)
			if pageErr != nil {
				t.Fatal(pageErr)
			}
			for _, u := range page.Users {
				if seen[u.ID] {
					t.Fatal("duplicate page entry")
				}
				seen[u.ID] = true
			}
			if page.NextCursor == nil {
				break
			}
			cursor = page.NextCursor
		}
		if len(seen) != countRows(t, db, "SELECT count(*) FROM users") {
			t.Fatal("pagination missed users")
		}
	})

	t.Run("administrator HTTP endpoints", func(t *testing.T) {
		var adminSubject string
		if err := db.Pool.QueryRow(t.Context(), "SELECT subject FROM user_identities WHERE user_id = $1", admin.User.ID).Scan(&adminSubject); err != nil {
			t.Fatal(err)
		}
		router, err := httpserver.New(db, httpserver.Dependencies{Backend: s, Verifier: localVerifier{}})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			method, path, body string
			status             int
		}{
			{"GET", "/api/v1/admin/instance", "", 200},
			{"GET", "/api/v1/admin/users?limit=2", "", 200},
			{"GET", "/api/v1/admin/users?limit=0", "", 400},
			{"GET", "/api/v1/admin/users?after=invalid", "", 400},
			{"PATCH", "/api/v1/admin/users/" + personal.User.ID.String() + "/status", `{"status":"disabled","reason":"HTTP administrative test"}`, 200},
			{"PATCH", "/api/v1/admin/users/" + personal.User.ID.String() + "/status", `{"status":"active","reason":"HTTP restore"}`, 200},
			{"PATCH", "/api/v1/admin/users/" + admin.User.ID.String() + "/status", `{"status":"disabled","reason":"forbidden"}`, 403},
			{"PATCH", "/api/v1/admin/users/" + uuid.NewString() + "/status", `{"status":"disabled","reason":"missing"}`, 404},
		} {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+adminSubject)
			req.Header.Set(httpserver.VersionHeader, "2026-09-16")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			contracttest.Response(t, req, w)
			if w.Code != tc.status {
				t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
			}
		}
	})
	t.Run("disabled administrator does not reopen claim", func(t *testing.T) {
		execute(t, db, "UPDATE users SET status = 'disabled' WHERE id = $1", admin.User.ID)
		next, _, err := s.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "after-disabled-admin"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "UTC", Locale: "en"})
		if err != nil || next.InstanceRole != "user" {
			t.Fatalf("administrator reclaimed: %v %v", next, err)
		}
		execute(t, db, "UPDATE users SET status = 'active' WHERE id = $1", admin.User.ID)
	})

	t.Run("HTTP lifecycle", func(t *testing.T) {
		router, err := httpserver.New(db, httpserver.Dependencies{Backend: s, Verifier: localVerifier{}})
		if err != nil {
			t.Fatal(err)
		}
		call := func(method, path, body string, want int) *httptest.ResponseRecorder {
			req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer http-user")
			req.Header.Set(httpserver.VersionHeader, "2026-09-16")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			contracttest.Response(t, req, w)
			if w.Code != want {
				t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body)
			}
			return w
		}
		call("GET", "/api/v1/me", "", http.StatusConflict)
		call("POST", "/api/v1/bootstrap", "{}", http.StatusBadRequest)
		w := call("POST", "/api/v1/bootstrap", `{"base_currency":"CNY","timezone":"Asia/Shanghai","locale":"zh-CN"}`, http.StatusCreated)
		var actor identity.Context
		if err = json.Unmarshal(w.Body.Bytes(), &actor); err != nil {
			t.Fatal(err)
		}
		call("POST", "/api/v1/bootstrap", "", http.StatusOK)
		call("GET", "/api/v1/me", "", http.StatusOK)
		call("GET", "/api/v1/books", "", http.StatusOK)
		call("GET", "/api/v1/books/"+actor.DefaultBook.ID.String(), "", http.StatusOK)
		call("GET", "/api/v1/books/"+admin.DefaultBook.ID.String(), "", http.StatusNotFound)
		call("PATCH", "/api/v1/me/preferences", `{"theme":"dark"}`, http.StatusOK)
		call("GET", "/api/v1/admin/users", "", http.StatusForbidden)
		call("GET", "/api/v1/admin/instance", "", http.StatusForbidden)
		_, err = s.SetStatus(t.Context(), admin, actor.User.ID, identity.StatusInput{Status: "disabled", Reason: "HTTP regression"})
		if err != nil {
			t.Fatal(err)
		}
		call("GET", "/api/v1/me", "", http.StatusForbidden)
		call("POST", "/api/v1/bootstrap", "", http.StatusForbidden)
	})
}
