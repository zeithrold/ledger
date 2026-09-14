# Ledger

A personal finance application scaffold built with Go, Gin, PostgreSQL, sqlc, and goose.

The selected architecture uses Eino for LLM orchestration, Clerk for authentication, and River for background jobs. The accounting core will use transactions and balanced postings, with independent tenant ownership and multi-currency support from the first release. Screenshots will be stored in S3, and DeepSeek is the selected LLM provider. Clerk authentication and local identity/tenant initialization are implemented. Financial tables, Eino, River, and S3 integrations remain unimplemented.

## Requirements

- Go 1.26 or later
- [just](https://just.systems/) for project commands
- Docker for the local PostgreSQL service and integration tests

## Quick start

```sh
cp .env.example .env.local
chmod 600 .env.local
# Edit .env.local, then:
go mod download
just db-up
# Set DATABASE_URL using the local DSN in docs/testing.md.
just migrate-up
just run
```

If `.env.local` already exists, edit it without overwriting it. The server listens on `127.0.0.1:8080` by default.

- `GET /healthz`: process liveness.
- `GET /readyz`: database readiness, or an explicit `database: disabled` response in HTTP-only mode.

An empty `DATABASE_URL` enables HTTP-only mode; business endpoints return 503 Problem Details. Database-backed API startup additionally requires `CLERK_SECRET_KEY`, `CLERK_ISSUER_URL`, and a valid Clerk API endpoint. Configure `CLERK_AUTHORIZED_PARTIES` for tokens carrying an `azp` claim. When configured, the server verifies the database connection and exits if it fails.

## Local secrets

`.env.local` is ignored by Git. `.env.example` contains only empty values and public defaults. Process environment variables take precedence over the local file. `CONFIG_FILE` can select a different file; ensure custom secret files are also excluded from Git.

| Variable | Purpose |
| --- | --- |
| `CLERK_SECRET_KEY` | Clerk backend API key |
| `CLERK_PUBLISHABLE_KEY` | Clerk frontend publishable key |
| `CLERK_API_ENDPOINT` | Clerk backend API endpoint |
| `CLERK_ISSUER_URL` | Exact token issuer for the Clerk instance |
| `CLERK_AUTHORIZED_PARTIES` | Comma-separated exact permitted token origins |
| `LLM_ENDPOINT` | DeepSeek endpoint; defaults to `https://api.deepseek.com` |
| `LLM_API_KEY` | Model provider API key |
| `LLM_MODEL` | Model identifier |
| `DATABASE_URL` | PostgreSQL connection string |
| `APP_ENV` | stage (default) or production; separate from GIN_MODE |
| `LOG_LEVEL` | debug, info (default), warn, error |
| `SENTRY_DSN` | Empty disables Sentry; non-empty enables tracing and logs |
| `S3_ENDPOINT` | Optional custom S3 endpoint |
| `S3_REGION` / `S3_BUCKET` | Storage region and bucket |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | Optional explicit S3 credentials |
| `S3_SESSION_TOKEN` | Optional temporary-credential session token |

Clerk session authentication uses its JWKS endpoint. DeepSeek and S3 settings remain placeholders and are not called. The publishable key is reserved for a future client. The DeepSeek model remains configurable through `LLM_MODEL`; fill it with the model available through your endpoint. Never log secrets or the complete configuration.

## SQL and migrations

Goose SQL migrations are the schema source of truth. sqlc generates typed pgx/v5 queries from that schema and the SQL in `internal/database/queries`. Generated files are committed and must not be edited manually.

```sh
just generate        # Pinned sqlc version; no database needed
just sqlc-vet
just migrate-status  # Requires DATABASE_URL
just migrate-up
# Explicitly roll back the latest migration when intended:
just migrate-down
```

Migration 1 is a no-op baseline. Migration 2 creates identity, tenant, preference, instance administrator, and audit tables; it creates no financial data. Goose maintains its own version table. Migrations are embedded into the migration binary; rebuild it after changing SQL. The HTTP server never runs migrations automatically.

The runtime uses a native pgx pool and sqlc queries. The migration command adapts that same pool to `database/sql` for goose.

## Layout

```text
cmd/api/                    HTTP entry point and graceful shutdown
cmd/migrate/                Explicit goose migration command
internal/config/            Environment configuration
internal/database/          pgx pool and generated-query wiring
internal/database/queries/  Hand-written SQL
internal/database/sqlgen/   sqlc-generated Go code
internal/auth/              Clerk session verification and isolated key cache
internal/identity/          Atomic bootstrap and tenant authorization
internal/problem/           RFC 7807 types and responder
internal/httpserver/        Version gate and Gin routes
migrations/                 Embedded goose migrations
docs/                       Maintained project reference and operational guides
```

## Verification

```sh
just install-lint
just check
just test-integration
just fuzz
```

The module and repository path is `github.com/zeithrold/ledger`. This repository is backend-only. A future client, potentially Flutter, will live outside this directory. See [architecture](docs/architecture.md) for the selected stack and implementation boundaries.

## License

[MIT](LICENSE).

See [testing and code quality](docs/testing.md) for isolation, fuzzing, and mutation preparation, and [exchange-rate snapshots](docs/exchange-rates.md) for the Frankfurter retention design.

## Identity API

All `/api/v1` requests require `X-Ledger-API-Version: 2026-09-14`. Login with Clerk, then explicitly call `POST /api/v1/bootstrap`. The first successful initializer becomes instance administrator; control initial access through Clerk registration. Later users receive isolated personal spaces. See [API contract](docs/api.md) and [Problem Details](docs/errors.md).

## Observability

API and migration commands emit structured JSON logs through `log/slog`. Set `APP_ENV=production` in production deployments; stage is the default. A non-empty `SENTRY_DSN` enables sentrygin, request tracing, error reporting, and official slog integration. Trace sampling is 100% in stage and 50% in production. See [logging and Sentry](docs/observability.md) for correlation, user scopes, redaction, and lifecycle rules.

API reference: stage exposes `/docs` with Scalar loaded from jsDelivr and the current
OpenAPI 3.1 contract. Production defaults to disabled; override with
`DOCS_ENABLED`. See [OpenAPI workflow](docs/openapi.md) for generated handlers,
contract checks and authentication.

See the [documentation index](docs/README.md) for the complete project reference.
