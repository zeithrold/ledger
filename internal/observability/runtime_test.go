package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zeithrold/ledger/internal/config"
)

type memoryTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
	closed bool
}

func (*memoryTransport) Configure(sentry.ClientOptions) {}
func (m *memoryTransport) SendEvent(e *sentry.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}
func (*memoryTransport) Flush(time.Duration) bool              { return true }
func (*memoryTransport) FlushWithContext(context.Context) bool { return true }
func (m *memoryTransport) Close()                              { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true }

func TestEnvironmentSamplingAndLogs(t *testing.T) {
	for _, env := range []string{"stage", "production"} {
		t.Run(env, func(t *testing.T) {
			cfg := config.Config{Environment: env, SentryDSN: "https://public@example.invalid/1", LogLevel: slog.LevelInfo}
			transport := &memoryTransport{}
			var output bytes.Buffer
			runtime, err := New(t.Context(), cfg, "test", &output, transport)
			if err != nil {
				t.Fatal(err)
			}
			options := runtime.hub.Client().Options()
			expected := 1.0
			if env == "production" {
				expected = 0.5
			}
			if !options.EnableTracing || options.TracesSampleRate != expected || options.TracesSampler(sentry.SamplingContext{}) != expected || options.SampleRate != 1 || options.Environment != env {
				t.Fatal("wrong environment sampling")
			}
			runtime.Logger.DebugContext(t.Context(), "excluded")
			runtime.Logger.InfoContext(t.Context(), "included", "event", "test_log")
			runtime.Close()
			if strings.Contains(output.String(), "excluded") || !strings.Contains(output.String(), "included") {
				t.Fatal("local level filter")
			}
			transport.mu.Lock()
			defer transport.mu.Unlock()
			found := false
			for _, e := range transport.events {
				for _, log := range e.Logs {
					if log.Body == "included" {
						found = true
					}
					if log.Body == "excluded" {
						t.Fatal("Sentry level filter")
					}
				}
			}
			if !found || !transport.closed {
				t.Fatalf("logs not delivered or not closed: events=%d", len(transport.events))
			}
		})
	}
}

func TestDisabledAndInvalidDSN(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://must-not-be-used@example.invalid/1")
	transport := &memoryTransport{}
	runtime, err := New(t.Context(), config.Config{Environment: "stage"}, "test", io.Discard, transport)
	if err != nil || runtime.hub != nil || len(runtime.Middleware()) != 0 {
		t.Fatalf("disabled Sentry initialized: %v", err)
	}
	runtime.Close()
	if transport.closed || len(transport.events) != 0 {
		t.Fatal("disabled runtime touched transport")
	}
	_, err = New(t.Context(), config.Config{Environment: "stage", SentryDSN: "invalid-secret-dsn"}, "test", io.Discard, transport)
	if err == nil || strings.Contains(err.Error(), "invalid-secret-dsn") {
		t.Fatal("invalid DSN was accepted or exposed")
	}
}

func TestStructuredLoggingAndRedaction(t *testing.T) {
	var output bytes.Buffer
	cfg := config.Config{Environment: "stage", LogLevel: slog.LevelDebug, SentryDSN: "", DatabaseURL: "postgres://credential-secret"}
	runtime, err := New(t.Context(), cfg, "test", &output, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), requestKey{}, &requestState{id: "request", method: "GET", route: "/api/v1/books/:book_id"})
	ctx = context.WithValue(ctx, actorKey{}, actorInfo{userID: "user", tenantID: "tenant"})
	runtime.Logger.WithGroup("operation").With("authorization", "Bearer must-not-leak").ErrorContext(ctx, "operation failed", slog.Any("error", &pgconn.PgError{Code: "23505", Message: "private financial value"}), slog.String("database_url", cfg.DatabaseURL),
		slog.Group("nested", "password", "private-password", "reason", "cannot connect to "+cfg.DatabaseURL))
	raw := output.String()
	for _, secret := range []string{"must-not-leak", "private financial value", "credential-secret", "private-password"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	var entry map[string]any
	if err = json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["level"] != "ERROR" || entry["service"] != "test" || entry["environment"] != "stage" {
		t.Fatalf("schema=%v", entry)
	}
	if !strings.Contains(raw, "23505") || !strings.Contains(raw, "request_id") || !strings.Contains(raw, "tenant_id") {
		t.Fatal("missing diagnostics/correlation")
	}
	if _, err = Writer(runtime.Logger, slog.LevelWarn).Write([]byte("library line\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "library_log") {
		t.Fatal("library bridge")
	}
}

func TestSanitizePanicAndPII(t *testing.T) {
	event := &sentry.Event{Request: &sentry.Request{URL: "https://example.com/private?token=secret", Data: "private"}, User: sentry.User{ID: "user", Email: "private@example.com", IPAddress: "127.0.0.1"}, Message: "panic payload", Exception: []sentry.Exception{{Value: "secret panic"}}}
	got := sanitizeEvent(event, &sentry.EventHint{RecoveredException: errors.New("secret panic")})
	if got.Request != nil || got.User.Email != "" || got.User.IPAddress != "" || got.User.ID != "user" || got.Exception[0].Value != "Unhandled panic" {
		t.Fatal("PII/panic not sanitized")
	}
}
