//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/observability"
)

type sqlTraceTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (*sqlTraceTransport) Configure(sentry.ClientOptions)        {}
func (*sqlTraceTransport) Flush(time.Duration) bool              { return true }
func (*sqlTraceTransport) FlushWithContext(context.Context) bool { return true }
func (*sqlTraceTransport) Close()                                {}
func (m *sqlTraceTransport) SendEvent(event *sentry.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func TestPoolQueriesJoinGinTrace(t *testing.T) {
	db := phaseOneDB(t)
	transport := &sqlTraceTransport{}
	runtime, err := observability.New(t.Context(), config.Config{Environment: "stage", SentryDSN: "https://public@example.invalid/1"}, "test", io.Discard, transport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	router := gin.New()
	router.Use(runtime.Middleware()...)
	router.GET("/trace", func(c *gin.Context) {
		ctx := c.Request.Context()
		if pingErr := db.PingContext(ctx); pingErr != nil {
			t.Fatal(pingErr)
		}
		rows, queryErr := db.Pool.Query(ctx, "SELECT $1::text", "private-query-argument")
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for rows.Next() {
			var value string
			if scanErr := rows.Scan(&value); scanErr != nil {
				t.Fatal(scanErr)
			}
		}
		rows.Close()
		if rowsErr := rows.Err(); rowsErr != nil {
			t.Fatal(rowsErr)
		}
		if _, execErr := db.Pool.Exec(ctx, "SELECT $1::text", "private-exec-argument"); execErr != nil {
			t.Fatal(execErr)
		}
		tx, beginErr := db.Pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		var value int
		if scanErr := tx.QueryRow(ctx, "SELECT 42").Scan(&value); scanErr != nil {
			t.Fatal(scanErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
		if scanErr := db.Pool.QueryRow(ctx, "SELECT 1 / 0").Scan(&value); scanErr == nil {
			t.Fatal("expected division by zero")
		}
		c.Status(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/trace", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.events) != 1 {
		t.Fatalf("expected one transaction without orphan or duplicate error events, got %d", len(transport.events))
	}
	event := transport.events[0]
	if len(event.Spans) != 7 {
		t.Fatalf("expected SQLC, Query, Exec, BEGIN, transaction QueryRow, COMMIT and failed query spans, got %d", len(event.Spans))
	}
	var parent sentry.SpanID
	var trace sentry.TraceID
	for i, span := range event.Spans {
		if i == 0 {
			parent, trace = span.ParentSpanID, span.TraceID
		}
		if span.Op != "db.sql.query" || span.ParentSpanID != parent || span.TraceID != trace || span.EndTime.IsZero() {
			t.Fatalf("SQL call not attached or finished: %+v", span)
		}
	}
	failed := event.Spans[len(event.Spans)-1]
	if failed.Status != sentry.SpanStatusInternalError || failed.Data["db.response.status_code"] != "22012" {
		t.Fatalf("missing failed query status: %+v", failed)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private-") {
		t.Fatal("SQL argument leaked into trace")
	}
}
