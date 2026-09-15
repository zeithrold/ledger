# Manual accounting

## Contract and boundaries

The current contract is `2026-09-16`. All `/api/v1` requests require
`X-Ledger-API-Version: 2026-09-16` and an authenticated session. The canonical
OpenAPI document is `internal/apicontract/v1/openapi.json`; run `just generate-api`
after changing it. The Flutter client lives in the sibling `ledger-app` repository.

Phase 2 supports cash, bank, wallet and other asset accounts; opening balances,
income, expenses, same-currency and cross-currency transfers, partial refunds,
independent expense fees, and ordinary transaction links. Credit cards, debt,
automatic rates, imports, multiple-book creation and offline posting are later work.

## Exact money and applied rates

- API amounts are decimal **strings**, without grouping, exponent notation, a
  leading plus sign, or redundant leading zeros. Inputs must have no more than the
  currency's accounting minor units. Over-precision is rejected, including trailing
  fractional zeros in a zero-place currency.
- The 148-code catalog retains the identity currency selection. Standard accounting
  precision comes from pinned [CLDR 48 currency data](https://github.com/unicode-org/cldr-json/blob/48.0.0/cldr-json/cldr-core/supplemental/currencyData.json).
  Cash rounding and cash-specific precision are not applied. The current catalog
  contains zero-, two- and three-place currencies; four-place representation is
  tested synthetically without adding an unsupported currency.
- Go uses integer minor units and `big.Rat`. Flutter uses `BigInt` plus an explicit
  scale. PostgreSQL uses unconstrained `NUMERIC` with finite-value, absolute-value
  (`< 10^18`) and precision checks, so it cannot silently round a posting.
- Debits are positive, credits negative. All journals balance in one common
  valuation currency. Original-currency quantities determine account balances.
  Negative asset balances are allowed. Reports group by currency and never add
  unrelated currencies.
- An actual transfer rate is **received principal / transferred principal**, in
  major currency units. Its numerator and denominator are exact integers. The
  display is rounded half-even to at most 18 significant digits, never used to
  calculate postings. The destination posting stores the inverse conversion into
  the source valuation currency. Both use the business date and `manual_actual`
  for cross-currency transfers; same-currency conversion is `identity`.
- Fees do not change either transfer principal or the actual rate. A fee can use
  another account and another currency.

## Tables and posting lifecycle

Migration `00003_accounting.sql` adds currencies, accounts, categories,
counterparties, stable transactions, immutable revisions, journals, postings,
links, idempotency receipts and audit events. Asset-account currency is fixed.
Category accounts are created per category/currency; opening balances use equity.

Migration `00004_currency_metadata.sql` removes currency display-name columns.
The currency API returns code and precision; clients join the versioned
[locale resource pack](currencies.md) by code.

The service owns journal construction. Public endpoints accept business intent;
they do not expose arbitrary posting writes. A write transaction:

1. Locks the tenant row to serialize tenant-wide idempotency keys, rechecks active
   user and membership, then locks the selected book row.
2. Replays a matching idempotency receipt or rejects a conflicting fingerprint.
3. Validates references, precision, expected revisions and remaining refund amount.
4. Inserts the business revision and all principal/fee journals, then seals them.
5. Inserts links, audit events and the response receipt, and commits together.

Tenant serialization is intentionally conservative for this personal-ledger phase.
No network call or user confirmation happens while a database transaction is open.

Composite foreign keys enforce tenant/book ownership. Deferred constraints require
sealed journals, at least two postings, zero common valuation sum, and a complete
revision lifecycle. Insert guards check minor units, exact conversion equations
and rate dates. Sealed journals and postings cannot be edited, deleted or appended
to. Reversals exactly negate the prior journal, retaining its transaction, currency
and business date. Revision, audit and idempotency history are immutable.

## Business inputs

`POST /books/{book_id}/accounts` accepts `name`, `kind`, `currency` and optional
`opening_amount` / `opening_date`. An omitted or zero opening amount creates no
financial transaction. Account maintenance changes name and archival state only.

`POST /books/{book_id}/transactions` accepts:

```json
{
  "entry": {
    "kind": "transfer",
    "occurred_on": "2026-09-15",
    "account_id": "00000000-0000-4000-8000-000000000001",
    "amount": "100.00",
    "to_account_id": "00000000-0000-4000-8000-000000000002",
    "to_amount": "720.00"
  },
  "fee": {
    "kind": "expense",
    "occurred_on": "2026-09-15",
    "account_id": "00000000-0000-4000-8000-000000000001",
    "amount": "1.00",
    "category_id": "00000000-0000-4000-8000-000000000003"
  }
}
```

IDs above are illustrative. Supply actual account/category IDs and a fresh UUID
`Idempotency-Key`. Expenses/income require an appropriate category; transfers
require two distinct accounts and two principal amounts. A refund uses
`kind: refund`, `original_id`, an amount and a same-currency destination asset
account; its category comes from the original expense. Optional `counterparty_id`
and `note` are available. To add a fee from a detail page, submit an expense entry
with `fee_for_id` and no nested `fee`.

Every create and accounting mutation requires an idempotency UUID. A successful
receipt and business writes commit atomically. Equivalent decoded inputs replay
the original response; another operation, book, or payload under that key returns
409. Keep the method, path, body and key unchanged after a timeout or lost response.

## Corrections, voids and links

`POST /books/{book_id}/transactions/{id}/corrections` accepts
`expected_revision`, an optional replacement `entry`, and optional `fees` (at most
20). Each fee has its own `id`, `expected_revision`, and optional `entry`.
Omitting an entry explicitly voids that transaction. Only directly associated
fees may be included. An outdated principal or fee revision rolls back the entire
operation. Unselected fees remain posted and linked.

A correction adds an exact reversal of the previous revision and a new posting;
a void adds only the reversal. Stable transaction IDs and links survive both.
Reversals take effect on the old business date, while audit timestamps record UTC
operation time. Details expose current data, history, journals and postings.

Multiple partial refunds are serialized and cannot exceed the active original
expense. Refunds reduce expense in their actual business-date period. Financial
correction or deletion of a refunded expense requires handling its active refunds
first; a note-only revision remains available. Refunds themselves can be corrected
or voided with the usual expected-revision rule.

Ordinary `related` links are symmetric, unique and removable. `fee` links identify
an active expense and cannot form nested chains; a fee has one principal parent.
Only the refund operation creates `refund` links. Ordinary link operations cannot
manufacture or remove refund semantics. Links never participate in balance or
income/expense sums.

## Catalogs and reads

Categories have stable preset codes and English/Chinese labels, or a custom name
displayed verbatim. Roots and children are both selectable. Parent/child kinds must
match; a third level, cycles, reparenting and kind changes are rejected. Archiving
a root archives its children; historical references remain readable. Renaming a
preset replaces its translated display name with the supplied custom name.
Counterparties belong to the tenant and support name/archive maintenance.

Transaction lists use descending business date, creation time and ID, with an
opaque exclusive cursor and a maximum page size of 100. Filters include inclusive
dates, account, type, currency, category (including children), counterparty and
`include_voided`. The default lists only current active revisions. Detail reads use
a consistent database snapshot so history and current revision agree.

`GET /books/{book_id}/summary` supports inclusive `from` / `to` dates. `totals`
contains per-currency income/expense; `categories` contains independent category
rollups. A parent includes its own postings and children exactly once. Do not sum
parent and child rollup rows to reconstruct totals. All sums include reversals and
refunds directly from postings.

## Validation and deployment

See [testing](testing.md) for race, Testcontainers, fuzz, mutation and native-client
commands. Apply migrations explicitly before deploying both current-date clients.
Down migration destroys Phase 2 financial data; exercise it only in disposable
databases. Backup/restore and live Clerk, Sentry and deployment checks remain
separate from local acceptance. Historical verification records and native captures
belong in the sibling `ledger-manuscript` directory.
