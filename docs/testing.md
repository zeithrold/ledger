# Testing and code quality

## Commands and isolation

| Command | Scope | External dependencies |
| --- | --- | --- |
| `just test-unit` / `just test` | Unit tests and fuzz seed corpus, with race detection | None |
| `just test-integration` | Tagged PostgreSQL lifecycle tests | Docker |
| `just fuzz` | Bounded native Go fuzzing | None |
| `just coverage-unit` | Unit coverage with all production packages instrumented | None |
| `just coverage-check` | Combined unit/integration and changed-code coverage | Both fresh profiles and a Git base |
| `just lint` | Strict lint and formatting, including integration source | Pinned Go tool acquisition on a cold cache |
| `just check` | Gate policy and pinned skills, contracts, architecture, lint, unit/integration coverage, vet, build | Docker for disposable PostgreSQL |
| `go run ./tool/bootstrap.go security` | Redacted source-secret scan and reachable Go vulnerability scan | Pinned tool acquisition and current vulnerability database |

Unit tests live beside production code and must not read developer credentials, contact services, or require Docker. Integration tests live in `tests/integration` and require `//go:build integration`. Missing Docker is a test failure, not a silently skipped pass.

Testcontainers creates an isolated PostgreSQL container with a random host port per lifecycle test. It never uses the Compose database or `DATABASE_URL`. Cleanup has an independent timeout. Compose is for persistent local manual testing only. Both currently use `postgres:17.9-alpine`; update them together. pgvector will be added when vector schema work starts.

## Local PostgreSQL

```sh
just db-up
just migrate-up
just migrate-status
just run
# Stop without deleting the named data volume:
just db-down
```

Compose binds only `127.0.0.1:15432`. The development-only DSN is:

```text
postgres://ledger:ledger_local_only@127.0.0.1:15432/ledger?sslmode=disable
```

This is a local test credential, not a production secret. Put it in `.env.local`. No recipe deletes the database volume.

## Style and lint

Follow the [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md), with automated enforcement through `.golangci.yml`. The guide also contains review concerns that a linter cannot prove: ownership, API clarity, narrow interfaces, and concurrency lifecycle.

The Go runner invokes the pinned lint version from `governance.json`; format with `just format` and check with `just lint`. Checks include errors, security, context propagation, resource closure, static analysis, suspicious constructs, naming and formatting. Generated sqlc files are excluded via their generated header. Any `nolint` requires a specific linter and an explanation; do not introduce blanket exclusions to pass checks. `just architecture-check` additionally validates the declared dependency graph.

## Fuzzing

`just fuzz` gives each configured target a 30-second PR budget. Seed inputs also run during unit tests. Scheduled `fuzz-nightly` uses 600 seconds per target. Check in minimized regressions under `testdata/fuzz/<target>`; never use real screenshots, tokens, or private financial data in corpora.

Accounting targets are `FuzzMoneyRoundTrip`, `FuzzExactRatio` and `FuzzBalance` in `./internal/money`; configuration includes `FuzzGinMode`. They use no database or external services. Future external-response and draft targets must remain deterministic.

## Mutation baseline

Pin [Gremlins v0.6.0](https://github.com/go-gremlins/gremlins/releases/tag/v0.6.0).
The selected target is the pure `internal/money` package. Generate a fresh report
and review surviving mutants; historical counts are not evidence for current
source. `mutation-check` requires a 90% score and zero timeouts, validates report
categories separately and never counts timeouts or invalid mutants as killed.

```sh
just mutation-accounting
```

The Go runner uses pinned Gremlins and copies only `internal/money` plus module
metadata into an isolated workspace. No application configuration, credentials
or unrelated files enter it. Service/database code is tested by Testcontainers
and excluded from this pure-function mutation score. Keep generated
files and credentials out of mutation fixtures. Reports belong in ignored
`mutation-results/`; dated evidence belongs in the external manuscript directory.

## Identity and contract tests

Offline unit tests verify signed session JWTs and JWKS cache behavior without developer credentials. HTTP contract tests cover version negotiation, Problem Details, authentication errors, body validation, routing, and recovery. Tagged integration tests verify concurrent bootstrap, administrator assignment, rollback, authorization, audit atomicity, pagination, and HTTP lifecycle in a disposable PostgreSQL database.

The current schema is version 4. Lifecycle tests upgrade populated version 3, verify an existing balance, restore all old currency names on rollback to 3, then roll back through identity 2 and baseline 1 to 0 and reapply. Never run Down against a persistent database as a routine check. `just check-currencies` also verifies pinned hashes, required locale coverage, precision and generated metadata drift.

`just check` requires Docker and starts disposable databases; it never uses a running developer database. Tool/module acquisition and golangci-lint's configuration-schema verification may require network access. Real Clerk integration is explicit and separate from `just test-unit`; no real token is stored as a fixture. See [governance](governance.md) for coverage floors and independent review.

## Observability checks

Observability tests use in-memory Sentry transports and fake DSNs. They verify logs are actually delivered, stage/production trace sampling, empty-DSN disablement, concurrent user isolation, recovery ordering, error event deduplication and JSON log redaction. They never load `.env.local` or send telemetry to the developer Sentry project. `sloglint` and `forbidigo` enforce the shared Go logging conventions.

## OpenAPI checks

`just check-api` regenerates the pinned Gin interface into a temporary file and
compares it with the checked-in output. Unit tests validate the OpenAPI 3.1 schema,
route coverage and documentation configuration. Integration lifecycle and admin
HTTP responses are schema-validated against the same embedded document.

Database tracing unit tests verify parent linkage, error/cancellation status, no-parent disablement and PostgreSQL literal/argument redaction using a fake Sentry transport. `TestPoolQueriesJoinGinTrace` uses an isolated PostgreSQL container and the real Gin Sentry middleware to verify SQLC, Query, Exec, transaction commands and failed queries appear in one request transaction without sending telemetry to Sentry.

## Native accounting integration

`TestAccountingDeviceServer` is skipped during ordinary integration runs. To test
Flutter against an actual Go HTTP server and disposable PostgreSQL database:

```sh
LEDGER_DEVICE_FIXTURE=/tmp/ledger-native.json go test -tags=integration \
  -run '^TestAccountingDeviceServer$' -v -timeout=26m ./tests/integration
```

The output JSON contains a loopback URL. Use it as `ACCOUNTING_TEST_URL` for the
client's `integration_test/accounting_test.dart` and
`integration_test/accounting_accessibility_test.dart`. Android's emulator uses
`10.0.2.2` in place of `127.0.0.1`. Each platform needs a fresh fixture for the
creation flow. Create `/tmp/ledger-native.json.stop` to finish and clean up its
container; the fixture also has a 25-minute deadline. It uses a synthetic verifier
compiled only with integration tests. This proves native UI, HTTP and PostgreSQL
behavior; it does not prove live Clerk, Sentry ingestion or deployment.
