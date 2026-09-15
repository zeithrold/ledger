package database

import (
	"context"
	"errors"
	"strings"

	"github.com/DataDog/go-sqllexer"
	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// queryTracer instruments every Query, QueryRow and Exec on the shared pool,
// including calls through SQLC and transactions. It never records arguments.
type queryTracer struct{}

type querySpanKey struct{}

var _ pgx.QueryTracer = queryTracer{}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// Never start an orphan transaction or initialize a global Sentry client.
	// Requests without Sentry (and untraced startup/migration work) stay silent.
	if sentry.SpanFromContext(ctx) == nil {
		return ctx
	}
	description := queryDescription(data.SQL)
	span := sentry.StartSpan(ctx, "db.sql.query", sentry.WithDescription(description), sentry.WithSpanOrigin("auto.db.pgx"))
	span.SetData("db.system.name", "postgresql")
	span.SetData("db.query.text", description)
	return context.WithValue(span.Context(), querySpanKey{}, span)
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(querySpanKey{}).(*sentry.Span)
	if !ok {
		return
	}
	defer span.Finish()
	switch {
	case data.Err == nil:
		span.Status = sentry.SpanStatusOK
		span.SetData("db.rows_affected", data.CommandTag.RowsAffected())
	case errors.Is(data.Err, context.Canceled):
		span.Status = sentry.SpanStatusCanceled
	case errors.Is(data.Err, context.DeadlineExceeded):
		span.Status = sentry.SpanStatusDeadlineExceeded
	default:
		span.Status = sentry.SpanStatusInternalError
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			span.SetData("db.response.status_code", pgErr.Code)
		}
	}
	// Raw database error messages/details can contain record values. Issue
	// reporting remains owned by the HTTP problem boundary, avoiding duplicates.
}

// Use the PostgreSQL lexer also used by Sentry's database/sql integration.
// Preserve query structure and placeholders, but omit literals and comments.
func queryDescription(sql string) string {
	lexer := sqllexer.New(sql, sqllexer.WithDBMS(sqllexer.DBMSPostgres))
	var out strings.Builder
	for {
		token := lexer.Scan()
		switch token.Type {
		case sqllexer.EOF:
			return strings.TrimSpace(out.String())
		case sqllexer.ERROR, sqllexer.UNKNOWN:
			return "SQL query"
		case sqllexer.COMMENT, sqllexer.MULTILINE_COMMENT:
			out.WriteByte(' ')
		case sqllexer.NUMBER, sqllexer.STRING, sqllexer.INCOMPLETE_STRING, sqllexer.DOLLAR_QUOTED_STRING, sqllexer.DOLLAR_QUOTED_FUNCTION:
			out.WriteByte('?')
		default:
			out.WriteString(token.Value)
		}
	}
}
