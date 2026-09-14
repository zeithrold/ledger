# Logging and Sentry

Both `cmd/api` and `cmd/migrate` use the same `internal/observability` lifecycle. Configure through process environment or `.env.local`; process environment retains precedence. Application environment is independent of Gin's debug/release/test mode.

| Setting | Default | Behavior |
| --- | --- | --- |
| APP_ENV | stage | Only stage or production; invalid values fail startup |
| LOG_LEVEL | info | debug, info, warn, error; one minimum level for stdout and Sentry Logs |
| SENTRY_DSN | empty | Empty: no Sentry client, middleware, tracing, logs or network traffic. Non-empty: enable Sentry. Invalid DSN fails startup without printing it |

Tracing is enabled with 100% sampling in stage and 50% in production. The environment sampler takes precedence over an upstream sampling decision. Error events have a 100% SDK sampling rate; enabled log levels are not randomly sampled. Normal SDK buffering, network delivery, and server quotas still apply.

The official `sentry-go`, `sentrygin`, and `sentry-go/slog` modules are pinned to **v0.49.0**. In this SDK version `EnableLogs` and its replacement `DisableLogs` were removed; constructing the official slog handler is the supported way to enable Sentry Logs. `EnableTracing` remains explicitly true. Ledger creates the slog handler only when DSN is non-empty. See the [upstream v0.49.0 changelog](https://github.com/getsentry/sentry-go/blob/v0.49.0/CHANGELOG.md).

## Go logging rules

Use standard-library `log/slog`, with a constant message and structured attributes. When a context is available, use `InfoContext`, `WarnContext`, `ErrorContext`, or `Log`. Log operational failures once at the owning boundary; return errors from lower layers. Never call Fatal/Exit from services. Do not concatenate JSON or use fmt.Print, log.Print/Fatal/Panic, gin.Logger or gin.Recovery. Lint enforces those entry-point restrictions and context-aware slog conventions.

```go
slog.InfoContext(ctx, "operation completed",
    "event", "operation_complete",
    "resource_id", resourceID.String(),
)
```

Use scalar attributes or `slog.Group`; do not log entire configurations, DTOs, requests or arbitrary objects. Unknown `Any` values are replaced instead of serialized. Errors are supported: PostgreSQL errors retain SQLSTATE but exclude SQL, detail, and record values. Sensitive attribute keys and configured credential values are redacted before either logging destination. This is defense in depth, not permission to include credentials in log messages.

- JSON lines to stdout, UTC timestamps; no application file rotation. Infrastructure owns collection/retention.
- Common fields: time, level, msg, service (`ledger-api`/`ledger-migrate`), environment, plus event-specific attributes.
- Request logs add request_id, method, route template, user_id/tenant_id when resolved, and trace_id/span_id when Sentry is enabled.
- `http_request` adds status, duration_ms and the accepted api_version; unmatched paths use `unmatched`, never the raw path or query string.
- INFO for successful requests and lifecycle events, WARN for 4xx problems, ERROR for 5xx and process failures, DEBUG for diagnostic/framework output.
- `http_problem` includes the RFC 7807 instance, type and status. Unexpected causes are logged here after sanitization, without exposing them in the response. The separate `http_request` event is the access summary, not a second exception report.
- stdlib logging is redirected through slog's default logger; Gin diagnostics, net/http errors and goose progress use explicit adapters. Goose fatal errors unwind to the process boundary instead of bypassing cleanup.
- Only the binary entrypoint exits. Process errors/panics are reported before flushing Sentry, with a five-second flush budget and transport shutdown. Flush timeout warnings use the local JSON destination only.

No inbound request body, query string, authorization header, cookie, email or inferred IP address is sent to Sentry by request instrumentation. Unhandled panic values are replaced with a fixed message; stack traces remain available. Error type, request ID and route provide correlation without raw inputs.

## Request tracing and user identity

Every HTTP request receives a server-generated `X-Request-ID`. An incoming header cannot overwrite it. This is additive to the existing date-versioned API contract; it does not change request or response body semantics.

Middleware order is request correlation/access logging → Problem Details recovery → request-scoped hub → sentrygin → panic-status bridge → version/auth/handlers. The bridge sets a pending 500 before sentrygin finishes a panicking transaction. Sentry repanics into Problem Details recovery, preserving the existing response contract.

Only verified identities are attached. Before local initialization, `user.id` is `clerk:<subject>`. Once local authorization or bootstrap succeeds, it is the internal user UUID, with tenant_id on scope/logs. Rejected credentials remain anonymous. Hubs are cloned for every request; no user is set on a global hub. Instance administrators still use their own user and tenant identities.

`sentrygin` captures panic events. Non-panic 5xx problems produce one Sentry error event with problem.instance/type tags; expected 4xx responses produce logs rather than issues. The official v0.49 slog integration sends Logs only, avoiding duplicate issues for ERROR-level access/problem logs. The panic event and final Problem Details response share request_id even though the response instance is generated after panic capture.
