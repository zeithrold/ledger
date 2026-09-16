# Documentation

Maintained reference for developing and operating Ledger.

| Document | Contents |
| --- | --- |
| [Architecture](architecture.md) | Stack, tenancy, runtime boundaries and implementation scope |
| [API contract](api.md) | Date versions, authentication, bootstrap and identity endpoints |
| [Manual accounting](accounting.md) | Exact amounts, posting invariants, corrections, fees, refunds and queries |
| [Currency reference data](currencies.md) | Locale bundles, source generation, client export and migrations |
| [Problem Details](errors.md) | Stable error types and response format |
| [OpenAPI and Scalar](openapi.md) | Generated HTTP boundary, documentation delivery and contract checks |
| [Logging and Sentry](observability.md) | Configuration, structured logging, tracing and user scopes |
| [Testing and code quality](testing.md) | Local database, checks, isolation and quality conventions |
| [Architecture and quality governance](governance.md) | Coverage floors, change selection, independent review and debug evidence |
| [Exchange-rate snapshots](exchange-rates.md) | Frankfurter snapshot model, River publish protocol, retention and pair resolution |

Keep these guides aligned with the implementation. Development journals, dated
verification results and planning manuscripts are maintained outside this repository.
