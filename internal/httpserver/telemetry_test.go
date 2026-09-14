package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/observability"
	"github.com/zeithrold/ledger/internal/problem"
)

type testTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (*testTransport) Configure(sentry.ClientOptions)        {}
func (*testTransport) Flush(time.Duration) bool              { return true }
func (*testTransport) FlushWithContext(context.Context) bool { return true }
func (*testTransport) Close()                                {}
func (s *testTransport) SendEvent(e *sentry.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

type namedVerifier struct{}

func (namedVerifier) Verify(_ context.Context, token string) (auth.Identity, error) {
	return auth.Identity{Issuer: "test", Subject: token}, nil
}

type contextBackend struct{ Backend }

func (contextBackend) Current(_ context.Context, who auth.Identity) (identity.Context, error) {
	return identity.Context{User: identity.User{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(who.Subject))}, Tenant: identity.Tenant{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(who.Subject))}}, nil
}

func TestSentryGinUserIsolationPanicAndCorrelation(t *testing.T) {
	transport := &testTransport{}
	var output bytes.Buffer
	runtime, err := observability.New(t.Context(), config.Config{Environment: "stage", SentryDSN: "https://public@example.invalid/1"}, "test", &output, transport)
	if err != nil {
		t.Fatal(err)
	}
	old := slog.Default()
	slog.SetDefault(runtime.Logger)
	t.Cleanup(func() { slog.SetDefault(old) })
	router, err := New(nil, Dependencies{Backend: contextBackend{}, Verifier: namedVerifier{}, Telemetry: runtime})
	if err != nil {
		t.Fatal(err)
	}
	router.GET("/panic/:private", func(*gin.Context) { panic("secret panic payload") })
	const users = 8
	var wg sync.WaitGroup
	for i := range users {
		wg.Go(func() {
			who := fmt.Sprintf("user-%d", i)
			req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/me?token=secret-query", nil)
			req.Header.Set(VersionHeader, "2026-09-14")
			req.Header.Set("Authorization", "Bearer "+who)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 200 || w.Header().Get("X-Request-ID") == "" {
				t.Errorf("request failed: %d", w.Code)
			}
		})
	}
	wg.Wait()
	w := request(t, router, "GET", "/panic/secret-path", "", "")
	checkProblem(t, w, 500, problem.Internal)
	request(t, router, "GET", "/api/v1/me", "", "")
	runtime.Close()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	seen := map[string]bool{}
	panics := 0
	badVersionAnonymous := false
	panicTraceFailed := false
	correlatedLogs := 0
	for _, event := range transport.events {
		if event.Request != nil {
			t.Fatal("raw request exported")
		}
		if event.Type == "transaction" {
			if event.Transaction == "GET /api/v1/me" && event.User.ID != "" {
				seen[event.User.ID] = true
			}
			if event.Transaction == "GET /api/v1/me" && event.User.ID == "" {
				badVersionAnonymous = true
			}
			if event.Transaction == "GET /panic/:private" {
				trace := event.Contexts["trace"]
				if fmt.Sprint(trace["status"]) == "internal_error" {
					panicTraceFailed = true
				}
			}
		} else if event.Type != "log" && event.Message == "Unhandled panic" {
			panics++
		}
		for _, log := range event.Logs {
			if strings.Contains(log.Body, "secret") {
				t.Fatal("log leaked panic")
			}
			if log.Body == "HTTP request completed" {
				if id, ok := log.Attributes["user_id"]; ok && id.AsString() != "" {
					if log.TraceID.String() == strings.Repeat("0", 32) {
						t.Fatal("uncorrelated log")
					}
					correlatedLogs++
				}
			}
		}
	}
	if len(seen) != users || panics != 1 || !badVersionAnonymous || !panicTraceFailed || correlatedLogs != users {
		t.Fatalf("users=%d panics=%d anonymous=%v panic trace=%v logs=%d", len(seen), panics, badVersionAnonymous, panicTraceFailed, correlatedLogs)
	}
	if strings.Contains(output.String(), "secret-path") || strings.Contains(output.String(), "secret-query") || strings.Contains(output.String(), "secret panic payload") {
		t.Fatal("access log leaked input")
	}
	if !strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatal("missing 5xx error level")
	}
}

func TestProblemEventsAreNotDuplicatedByLogging(t *testing.T) {
	transport := &testTransport{}
	runtime, err := observability.New(t.Context(), config.Config{Environment: "stage", SentryDSN: "https://public@example.invalid/1"}, "test", &bytes.Buffer{}, transport)
	if err != nil {
		t.Fatal(err)
	}
	old := slog.Default()
	slog.SetDefault(runtime.Logger)
	t.Cleanup(func() { slog.SetDefault(old) })
	router, err := New(failedDB{}, Dependencies{Telemetry: runtime})
	if err != nil {
		t.Fatal(err)
	}
	request(t, router, "GET", "/readyz", "", "")
	request(t, router, "GET", "/missing", "", "")
	runtime.Close()
	errors := 0
	for _, event := range transport.events {
		if event.Type == "" {
			errors++
			if event.Tags["problem.type"] != "service-unavailable" || event.Tags["problem.instance"] == "" {
				t.Fatal("missing problem correlation")
			}
		}
	}
	if errors != 1 {
		t.Fatalf("error events=%d", errors)
	}
}

var _ http.Handler = (*gin.Engine)(nil)
