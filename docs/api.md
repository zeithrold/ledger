# API contract

Current contract: **`/api/v1` + `X-Ledger-API-Version: 2026-09-16`**.

## Version selection

All `/api/*` requests require exactly one date header. The server accepts only dates registered for that path's major version; there is no default, nearest-date matching, or implicit upgrade. Major versions have independent date registries. `internal/httpserver/version.go` is the source of truth. Unknown major versions return 404; missing, malformed, repeated, or unsupported dates return 400 Problem Details. Major selection precedes date validation; both precede authentication or writes.

Every response handled by the application includes a server-generated `X-Request-ID` for correlation. Accepted responses, including subsequent errors, echo `X-Ledger-API-Version`. Rejected versions do not. API responses include `Vary: X-Ledger-API-Version` and `Cache-Control: no-store`. `/healthz` and `/readyz` do not require a version. Browser preflight requests are exempt from date selection, but CORS itself is not enabled yet: OPTIONS currently returns 405 with Allow. When a browser client is added, allow and expose the version header in CORS configuration.

| Major | Supported date | Status |
| --- | --- | --- |
| v1 | 2026-09-16 | Identity and manual accounting; language-neutral currency metadata |

A major version tolerates the current date beside the immediately preceding one, so an already deployed client survives a backend roll-forward. Only the current document is ever served, which means a date revision must be additive: renaming, removing, or retyping a field is a breaking change and requires a new path major version instead. `internal/apicontract/contract.go` holds the tolerated pair; `Supported` collapses it while both entries are equal, as they are for this release.

During early development, only one date implementation is maintained per major version. Existing request, response, or authorization semantics must not silently change under that date. Breaking revisions advance the date and can immediately remove the previous implementation; old clients receive an explicit unsupported-version problem. Internal fixes and independent new endpoints need not change the date. A broad redesign uses a new major path. If compatibility adapters become necessary, register explicit date handlers; no adapters are implemented yet.

The date raised to the backend must move with the release in the same change: `2026-09-15` removed the currency name fields and is deliberately not tolerated, because serving a client a document it cannot parse is worse than an explicit rejection.

Breaking deployment procedure: enter a maintenance window, stop traffic to old instances, apply the explicit migration, deploy the matching API version, then restore traffic after checks. Do not mix incompatible date implementations behind a load balancer. Version negotiation does not make destructive database changes compatible. A rollback involving phase 1 Down migrations deletes identity data and is only suitable for disposable environments; production recovery requires a reviewed backup/restore plan.

## Authentication

Send exactly one `Authorization: Bearer <Clerk session token>` header. Backend API keys, cookies, and client-supplied user IDs are not accepted as user authentication. A valid token must have a verified RS256 signature, exact configured issuer, exp, nbf, non-empty sub and sid, and cannot have `sts=pending`. Time claims (`iat`, `nbf`, `exp`) allow at most five seconds of server clock skew, matching [Clerk’s documented default](https://clerk.com/docs/reference/backend/verify-token). Keep host clocks synchronized; larger drift remains a verification failure. An azp claim, when present, must match a configured origin. An absent azp is allowed for native clients; an empty origin allowlist rejects all non-empty azp values.

The Clerk v2.7.0 SDK verifies signatures and fetches JWKS from the configured backend endpoint. Ledger uses a per-verifier cache because the SDK middleware uses a global sliding cache and conflates verification and key-fetch errors. Cache lifetime is one hour without sliding renewal. Unknown key IDs trigger refresh at most once per 30 seconds; failed fetches also have a 30-second cooldown. Existing unexpired keys can be used during a key-service outage. Expired or unavailable keys fail closed with 503; malformed or unverifiable tokens return 401. HTTP fetches time out after five seconds and do not follow redirects. A newly rotated unknown key may require retry after the cooldown.

Clerk manages sessions; Ledger checks local user, tenant, and membership status on every business request. Local suspension applies to requests authorized after the suspension commits; already executing requests are not cancelled. Clerk webhook synchronization and immediate remote session revocation checks are not implemented. Future user-owned background jobs must recheck local permissions and must not carry a session token as their execution credential.

## Bootstrap and personal context

```sh
# SESSION_TOKEN is a short-lived session token acquired from your Clerk instance.
curl -X POST http://127.0.0.1:8080/api/v1/bootstrap \
  -H 'X-Ledger-API-Version: 2026-09-16' \
  -H "Authorization: Bearer ${SESSION_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"base_currency":"CNY","timezone":"Asia/Shanghai","locale":"zh-CN"}'
```

First initialization requires an uppercase ISO 4217 currency, an IANA timezone (not the host-dependent `Local`), and a BCP 47 locale. It creates `Personal`, owner membership, `Main`, and preferences with theme `system`, all in one transaction. No money, accounts, classifications, or background jobs are created.

The first transaction to initialize the instance assigns its user as the instance administrator. There is no installation secret or preselected subject: control initial access through Clerk registration. The singleton instance row is locked before checking identity, so concurrent requests cannot produce duplicate personal spaces or multiple administrators. Any failure rolls back the administrator assignment and all initialization records.

A new identity returns 201; an existing identity returns 200 with the same context and ignores replacement initialization values. An empty body is allowed for retries. Repeated initialization cannot reactivate disabled users. HTTP bodies must still be structurally valid and contain only supported fields. There is no automatic repair of partially missing identity data and no administrator reclaim, transfer, deletion, or promotion endpoint.

Context response shape (UUIDs shown as placeholders):

```json
{
  "user": {"id":"<uuid>","display_name":"","status":"active"},
  "instance_role":"admin",
  "tenant":{"id":"<uuid>","name":"Personal","role":"owner"},
  "default_book":{"id":"<uuid>","tenant_id":"<uuid>","name":"Main","base_currency":"CNY"},
  "preferences":{"locale":"zh-CN","timezone":"Asia/Shanghai","theme":"system"}
}
```

Subsequent users have `instance_role: user`. Display name remains empty until a future profile capability; no external profile call is needed to initialize. The identity key is `(issuer, subject)`, never email. A different issuer denotes a different identity.

| Method and path | Request | Success |
| --- | --- | --- |
| POST /api/v1/bootstrap | Initialization fields above, or empty retry | 201/200 context |
| GET /api/v1/me | None | 200 context; 409 if not initialized |
| GET /api/v1/books | None | 200 `{"books":[book]}` |
| GET /api/v1/books/:book_id | UUID path parameter | 200 book; 404 for foreign or absent book |
| PATCH /api/v1/me/preferences | Any of locale, timezone, theme | 200 full preferences |
| GET /api/v1/admin/instance | Administrator required | 200 `{"admin_user_id":"<uuid>","initialized_at":"<RFC3339 timestamp>"}` |
| GET /api/v1/admin/users | Optional `after=<uuid>&limit=50` | 200 `{"users":[user],"next_cursor":null}` |
| PATCH /api/v1/admin/users/:user_id/status | `{"status":"disabled","reason":"..."}` | 200 user |

Preference patches require at least one non-null value; null values are treated as absent. Theme is `system`, `light`, or `dark`. The API does not allow changing book base currency. User listing uses exclusive UUID ordering, limit 1–100 (default 50), with next_cursor null on the last page; it is not a snapshot across concurrent registrations.

Status accepts `active` or `disabled` with a nonblank reason of at most 1000 UTF-8 bytes. Every accepted status command, including repeated same-state commands, atomically records the previous/new status and actor. Administrator status cannot be changed. Restoration retains existing data. Administrator access permits internal user management only, never another tenant's books.

JSON request bodies are limited to 16 KiB; unknown fields, invalid types, and trailing JSON are rejected. Clients cannot set local roles, IDs, or ownership. Unknown resources return 404 and unsupported methods return 405 with Allow, both as Problem Details. Successful health payloads remain unchanged. See [error types](errors.md).

## Machine-readable contract

The canonical OpenAPI 3.1 contract generates the Gin interface and HTTP DTOs.
See [OpenAPI and Scalar](openapi.md) for `/docs`, exact-date spec URLs, configuration,
generation, authentication in the request client and contract validation.

## Accounting API

The current date includes currencies, asset accounts, two-level categories, tenant counterparties, immutable transaction revisions, ordinary/fee links, refunds and per-currency summaries. See [manual accounting](accounting.md) for business inputs, idempotency, optimistic revision checks and reversal semantics.

## Exchange-rate API

`GET /api/v1/exchange-rates?base=USD&quote=CNY` requires an authenticated session and returns the `MarketRate` object described in [exchange-rate snapshots](exchange-rates.md). Both query parameters are required uppercase ISO 4217 codes from the ledger catalog and must differ; anything else is a 400 Problem Details response. The endpoint only reads published daily snapshots: it never calls the provider, and a pair with no usable snapshot returns 200 with `"status": "unavailable"`, a `reason`, and null rate fields instead of an invented value. The response reports `snapshot_date`, the effective `rate_date`, `stale`, the exact `numerator`/`denominator` and how the value was `derived` (`direct`, `inverse` or `cross`).

This endpoint was added as an additive revision of `2026-09-16`; it renames or removes no existing field, so the date stays unchanged and already deployed clients keep working.
