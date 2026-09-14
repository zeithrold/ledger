# OpenAPI and Scalar

Ledger publishes OpenAPI 3.1 from `internal/apicontract/v1/openapi.json`.
This contract generates `internal/apiv1/api.gen.go` using pinned oapi-codegen
v2.8.0 (runtime v1.7.0). Handlers implement its Gin interface and map generated
HTTP DTOs to the identity service explicitly. Do not edit generated code.

Run `just generate-api` after contract edits and `just check-api` to compare
regeneration in a temporary file, without requiring Git. `just check` includes
this check. CI also regenerates and checks the generated diff.

## Serving and versioning

- `/docs`: Scalar API Reference 1.68.0 loaded from jsDelivr.
- `/openapi/v1/2026-09-14.json`: exact current contract, same origin.
- `DOCS_ENABLED`: defaults to true for stage, false for production; explicit
  boolean overrides both. Disabled documentation and asset routes return the
  standard 404 Problem Details.
- These routes are outside `/api/*`, so need neither authentication nor the
  date header. Enabling docs makes their content public. All business requests
  retain normal version validation, authentication and authorization.
- The explicitly registered v1 contract supplies the accepted date, header
  enum/default, generated DTOs and served URL. Adding another major requires
  explicit registration and handlers; leaving an old JSON file does not enable it.
- Contract changes must update `info.version` and the API version parameter and
  response header enum together. Early iteration supports only the current date;
  archived documentation does not imply a supported API. Use the existing
  maintenance-window deployment policy for breaking revisions.

Scalar uses the contract's required header default for requests and curl samples.
Paste a short-lived Clerk **session token** into Bearer Token when needed;
Scalar does not provide Clerk sign-in or token refresh. Authentication persistence,
telemetry and the AI agent are disabled. There is no public request proxy, external font dependency or Node runtime.
CSP permits scripts from jsDelivr; API connections remain limited to the same
origin. Loading the documentation requires access to jsDelivr. The browser client can make real writes when a user sends a write request.

## Behavioral boundaries

Version validation runs before authentication, authorization and generated
parameter binding. All binding errors use the central Problem Details responder.
Strict JSON decoding still rejects unknown fields, trailing JSON and oversized
bodies. Service validation remains responsible for ISO currency, timezone,
locale, permissions and database-dependent rules. Bootstrap deliberately has
an optional body and optional nullable properties: all three values are required
for a new identity, while an existing identity may retry with an empty body.
An OpenAPI schema alone cannot express that database-dependent condition.

Tests validate document structure, cover every generated route, validate HTTP-only
responses, and validate real PostgreSQL HTTP lifecycle/admin responses against the
published schema. They do not install schema validation in the live request path.

## Script delivery

Scalar is loaded from the pinned URL:
https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.68.0/dist/browser/standalone.js

The large vendor bundle is no longer stored in the repository or embedded in the
Go binary. The HTML, initialization script and OpenAPI document remain local.
Upgrade the explicit version and repeat browser checks when updating Scalar.
