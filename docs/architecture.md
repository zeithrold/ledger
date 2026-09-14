# Architecture

## Selected stack

| Concern | Selection |
| --- | --- |
| Language | Go |
| HTTP | Gin |
| Database | PostgreSQL |
| SQL access | sqlc with pgx/v5 |
| Schema migrations | goose |
| Background jobs | River |
| LLM orchestration | Eino |
| Authentication | Clerk |
| LLM provider | DeepSeek |
| Screenshot storage | S3 |
| Command runner | just |
| Module / repository | github.com/zeithrold/ledger |
| License | MIT |
| Exchange rates | Frankfurter, daily PostgreSQL snapshots, rolling 30-day cache |

## Accounting and tenancy

The planned accounting core uses transactions and balanced postings for income, expenses, transfers, credit cards, and lending or borrowing. AI produces editable drafts; only validated, user-confirmed transactions affect balances.

Tenants remain independent from users. The initial product provisions one personal tenant per user, while retaining a membership relationship. Business data belongs to a tenant; creator and editor identities are separate audit fields.

Clerk manages authentication. The application manages internal identity mapping and tenant authorization.

## Multi-currency scope

Multi-currency support is required in the first release. The accounting core must retain account currency, original amounts, and the transaction valuation currency. Amounts in different currencies must never be summed directly to test posting balance.

Proposed implementation details, not yet implemented:

- Keep posting quantities in account currency and valuation amounts in a common transaction currency.
- Store the exchange rate and its source/time where needed to explain conversion.
- Distinguish historical transaction conversion from report-date conversion.
- Define exact decimal precision, currency minor units, and rounding entries before adding the accounting schema.

## Client boundary

No frontend is included now. A future client may use Flutter and will be developed outside this repository. Authentication integration must account for that future client rather than assume a web-only UI.

## Runtime boundaries

- sqlc generates typed queries; pgx manages PostgreSQL connections.
- Goose owns schema changes and runs through a separate command.
- River will schedule background execution; Eino will orchestrate model and tool calls inside workers.
- Agent checkpoints and business idempotency are separate concerns.
- Model calls and user input waits must not hold database transactions open.
- PostgreSQL will store versioned knowledge; embedding model and vector index configuration remain undecided.

## Current implementation

Implemented: configuration loading, health endpoints, pgx/sqlc and goose, explicit date-versioned identity APIs, RFC 7807 errors, Clerk session verification, atomic personal bootstrap, first-instance administrator assignment, and audited user suspension/restoration.

Not implemented: financial tables or ledger operations, Eino agents, River workers, knowledge retrieval, file storage, Clerk lifecycle webhooks, administrator transfer, or multiple-book creation.

The instance administrator is separate from tenant owner and cannot bypass tenant scope. A singleton database row serializes provisioning; the identity key and personal mappings have unique constraints. Initialization and administrator assignment commit together. No background job is needed in phase 1; River initialization work remains in phase 3.

[API contracts](api.md) use major path versions plus required exact date headers. [Problem Details](errors.md) use stable type URIs under `https://ledger.ztd.me/errors/`.

## Open implementation choices

Remaining details: exact money/rate precision and rounding policy, daily FX schedule/provider filtering, the DeepSeek model and image-input path, embedding model/dimensions, and S3 deployment settings. These do not change the selected multi-currency scope, DeepSeek provider, or S3 storage. No frontend implementation is planned at this stage.

See [exchange rates](exchange-rates.md) and [testing](testing.md). Historical applied transaction rates remain independent from the expiring market-data cache.

## Observability

Both binaries use standard-library slog with JSON stdout, context correlation, and shared lifecycle/level/redaction rules. Sentry is opt-in through a non-empty SENTRY_DSN; sentrygin owns request tracing and panic capture, while official Sentry slog integration sends Logs. APP_ENV selects stage (100% tracing) or production (50% tracing), independent of Gin mode. User scopes are per request, using internal user IDs after local resolution. See [logging and Sentry](observability.md).

## Generated HTTP boundary

OpenAPI 3.1 now defines the HTTP interface and DTOs. Generated Gin wrappers bind
parameters after the existing version/authentication/authorization middleware;
handwritten adapters retain strict body decoding and map HTTP DTOs to identity
service inputs and outputs. Problem Details and observability remain centralized.
Scalar loaded from jsDelivr is an optional documentation surface with same-origin API requests; see [OpenAPI](openapi.md).
