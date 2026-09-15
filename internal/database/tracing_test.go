package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type traceTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (*traceTransport) Configure(sentry.ClientOptions)        {}
func (*traceTransport) Flush(time.Duration) bool              { return true }
func (*traceTransport) FlushWithContext(context.Context) bool { return true }
func (*traceTransport) Close()                                {}
func (m *traceTransport) SendEvent(event *sentry.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func TestQueryTracerParentStatusAndPrivacy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status sentry.SpanStatus
	}{
		{"success", nil, sentry.SpanStatusOK},
		{"postgres", &pgconn.PgError{Code: "23505", Message: "private record", Detail: "private detail"}, sentry.SpanStatusInternalError},
		{"canceled", context.Canceled, sentry.SpanStatusCanceled},
		{"deadline", context.DeadlineExceeded, sentry.SpanStatusDeadlineExceeded},
		{"other", errors.New("private error"), sentry.SpanStatusInternalError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := &traceTransport{}
			client, err := sentry.NewClient(sentry.ClientOptions{Dsn: "https://public@example.invalid/1", EnableTracing: true, TracesSampleRate: 1, Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			ctx := sentry.SetHubOnContext(t.Context(), sentry.NewHub(client, sentry.NewScope()))
			transaction := sentry.StartTransaction(ctx, "GET /test")
			tracer := queryTracer{}
			queryCtx := tracer.TraceQueryStart(transaction.Context(), nil, pgx.TraceQueryStartData{SQL: "SELECT $1::text", Args: []any{"private argument"}})
			tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: tt.err, CommandTag: pgconn.NewCommandTag("SELECT 1")})
			if !transaction.EndTime.IsZero() {
				t.Fatal("query finished its parent")
			}
			transaction.Finish()
			if len(transport.events) != 1 || len(transport.events[0].Spans) != 1 {
				t.Fatalf("expected one transaction with one child: %+v", transport.events)
			}
			span := transport.events[0].Spans[0]
			if span.ParentSpanID != transaction.SpanID || span.TraceID != transaction.TraceID || span.Status != tt.status || span.Op != "db.sql.query" || span.Description != "SELECT $1::text" {
				t.Fatalf("unexpected SQL span: %+v", span)
			}
			if tt.name == "postgres" && span.Data["db.response.status_code"] != "23505" {
				t.Fatal("missing SQLSTATE")
			}
			payload, err := json.Marshal(transport.events[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), "private") {
				t.Fatal("arguments or error details leaked")
			}
		})
	}
}

func TestQueryTracerWithoutParentIsNoop(t *testing.T) {
	ctx := t.Context()
	tracer := queryTracer{}
	got := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	if got != ctx || sentry.SpanFromContext(got) != nil {
		t.Fatal("untraced database call created a span")
	}
	tracer.TraceQueryEnd(got, nil, pgx.TraceQueryEndData{})
}

func TestQueryDescriptionRedactsPostgreSQLValues(t *testing.T) {
	for _, sql := range []string{
		"SELECT 'private', 12345, $1 -- private comment",
		"SELECT $$private$$, $tag$private$tag$, $1 /* private */",
		"SELECT E'private\\n', $1",
		"SELECT 'private",
	} {
		got := queryDescription(sql)
		if strings.Contains(got, "private") || strings.Contains(got, "12345") {
			t.Fatalf("literal or comment leaked: %q", got)
		}
		if !strings.Contains(got, "SELECT") || !strings.Contains(got, "?") {
			t.Fatalf("query shape lost: %q", got)
		}
	}
	if got := queryDescription("SELECT id FROM users WHERE id = $1"); got != "SELECT id FROM users WHERE id = $1" {
		t.Fatalf("parameterized SQL changed: %q", got)
	}
}
