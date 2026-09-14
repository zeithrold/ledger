// Package observability owns structured logging, request correlation and Sentry.
package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgconn"
)

// handler applies the same level and redaction policy before both destinations.
type handler struct {
	destinations []slog.Handler
	level        slog.Level
	secrets      []string
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }
func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	sanitized := slog.NewRecord(record.Time.UTC(), record.Level, scrub(record.Message, h.secrets), record.PC)
	record.Attrs(func(a slog.Attr) bool { sanitized.AddAttrs(h.clean(a)); return true })
	sanitized.AddAttrs(contextAttrs(ctx)...)
	var failures []error
	for _, destination := range h.destinations {
		if destination.Enabled(ctx, record.Level) {
			failures = append(failures, destination.Handle(ctx, sanitized.Clone()))
		}
	}
	return errors.Join(failures...)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.clean(a)
	}
	next := *h
	next.destinations = make([]slog.Handler, len(h.destinations))
	for i, d := range h.destinations {
		next.destinations[i] = d.WithAttrs(clean)
	}
	return &next
}

func (h *handler) WithGroup(name string) slog.Handler {
	next := *h
	next.destinations = make([]slog.Handler, len(h.destinations))
	for i, d := range h.destinations {
		next.destinations[i] = d.WithGroup(name)
	}
	return &next
}

func (h *handler) clean(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	key := strings.ToLower(a.Key)
	for _, word := range []string{"password", "secret", "token", "authorization", "cookie", "dsn", "database_url", "body", "query", "api_key"} {
		if strings.Contains(key, word) {
			return slog.String(a.Key, "[REDACTED]")
		}
	}
	switch a.Value.Kind() {
	case slog.KindGroup:
		children := a.Value.Group()
		clean := make([]slog.Attr, len(children))
		for i, child := range children {
			clean[i] = h.clean(child)
		}
		a.Value = slog.GroupValue(clean...)
	case slog.KindString:
		a.Value = slog.StringValue(scrub(a.Value.String(), h.secrets))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				a.Value = slog.StringValue("PostgreSQL error (SQLSTATE " + databaseError.Code + ")")
			} else {
				a.Value = slog.StringValue(scrub(err.Error(), h.secrets))
			}
		} else {
			a.Value = slog.StringValue("[unsupported structured value]")
		}
	}
	return a
}

func scrub(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

// Writer adapts standard-library and framework logging into the shared handler.
// Application code should use context-aware slog directly rather than this adapter.
func Writer(logger *slog.Logger, level slog.Level) io.Writer { return logWriter{logger, level} }

type logWriter struct {
	logger *slog.Logger
	level  slog.Level
}

func (w logWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text != "" {
		w.logger.Log(context.Background(), w.level, text, "event", "library_log")
	}
	return len(p), nil
}

// GooseLogger preserves migration diagnostics without Fatalf bypassing deferred flush.
type GooseLogger struct{ Logger *slog.Logger }

// Printf adapts goose progress output to structured logs.
func (l GooseLogger) Printf(format string, args ...any) {
	l.Logger.Info("migration progress", "event", "migration_progress", "message", fmt.Sprintf(format, args...))
}

// Fatalf unwinds to the process boundary so cleanup and Sentry flush still run.
func (l GooseLogger) Fatalf(format string, args ...any) {
	panic(fmt.Errorf("migration failure: %s", fmt.Sprintf(format, args...)))
}

func contextAttrs(ctx context.Context) []slog.Attr {
	attrs := []slog.Attr{}
	if state, ok := ctx.Value(requestKey{}).(*requestState); ok {
		attrs = append(attrs, slog.String("request_id", state.id), slog.String("method", state.method), slog.String("route", state.route))
	}
	if actor, ok := ctx.Value(actorKey{}).(actorInfo); ok {
		attrs = append(attrs, slog.String("user_id", actor.userID))
		if actor.tenantID != "" {
			attrs = append(attrs, slog.String("tenant_id", actor.tenantID))
		}
	}
	if span := sentry.SpanFromContext(ctx); span != nil {
		attrs = append(attrs, slog.String("trace_id", span.TraceID.String()), slog.String("span_id", span.SpanID.String()))
	}
	return attrs
}
