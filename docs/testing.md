# Testing and code quality

## Commands and isolation

| Command | Scope | External dependencies |
| --- | --- | --- |
| `just test-unit` / `just test` | Unit tests and fuzz seed corpus, with race detection | None |
| `just test-integration` | Tagged PostgreSQL lifecycle tests | Docker |
| `just fuzz` | Bounded native Go fuzzing | None |
| `just coverage-unit` | Separate unit coverage report | None |
| `just lint` | Strict lint and formatting, including integration source | Installed pinned lint tool |
| `just check` | SQL validation, lint, unit tests, vet, build | No running database |

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

Install the pinned version with `just install-lint`, format with `just format`, and check with `just lint`. Checks include errors, security, context propagation, resource closure, static analysis, suspicious constructs, naming and formatting. Generated sqlc files are excluded via their generated header. Any `nolint` requires a specific linter and an explanation; do not introduce blanket exclusions to pass checks.

## Fuzzing

`just fuzz ./internal/config FuzzGinMode 10s` starts a bounded native fuzz run. Seed inputs also run during unit tests. Check in minimized regressions under `testdata/fuzz/<target>`; never use real screenshots, tokens, or private financial data in corpora.

Future targets should test money parsing, currency conversion/rounding invariants, external response decoding, and draft validation using pure functions and deterministic inputs. Avoid external calls in fuzz targets.

## Mutation testing preparation

Mutation testing is a separate future quality gate, not part of `just test`. [Gremlins](https://github.com/go-gremlins/gremlins) is the candidate engine; pin and validate a Go-compatible release before enabling it. No mutation score is claimed by this scaffold.

Run mutations on pure, handwritten domain packages in a disposable copy or worktree. Exclude generated SQL code, migrations, adapters, integration tests, and fuzz harnesses. Do not copy `.env.local`, Git credentials, or storage keys into the work directory. Execute unit tests for each mutant; killed, survived, uncovered and timed-out mutants must be reported separately. Review equivalent mutants explicitly.

Store reports under ignored `mutation-results/`. Start with money rounding and posting balance validation once those functions exist; set a threshold only after establishing a meaningful baseline. The present scaffold has no financial domain logic to mutate.

## Identity and contract tests

Offline unit tests verify signed session JWTs and JWKS cache behavior without developer credentials. HTTP contract tests cover version negotiation, Problem Details, authentication errors, body validation, routing, and recovery. Tagged integration tests verify concurrent bootstrap, administrator assignment, rollback, authorization, audit atomicity, pagination, and HTTP lifecycle in a disposable PostgreSQL database.

The phase 1 migration is version 2; lifecycle tests verify migration to 2, rollback through baseline 1 to 0, and reapply. Never run Down against a persistent identity database as a routine check.

`just check` requires no running database, but tool/module acquisition and golangci-lint's configuration-schema verification may require network access. Real Clerk integration is explicit and separate from `just test-unit`; no real token is stored as a fixture.

## Observability checks

Observability tests use in-memory Sentry transports and fake DSNs. They verify logs are actually delivered, stage/production trace sampling, empty-DSN disablement, concurrent user isolation, recovery ordering, error event deduplication and JSON log redaction. They never load `.env.local` or send telemetry to the developer Sentry project. `sloglint` and `forbidigo` enforce the shared Go logging conventions.

## OpenAPI checks

`just check-api` regenerates the pinned Gin interface into a temporary file and
compares it with the checked-in output. Unit tests validate the OpenAPI 3.1 schema,
route coverage and documentation configuration. Integration lifecycle and admin
HTTP responses are schema-validated against the same embedded document.
