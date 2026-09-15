# Currency reference data

## Ownership and contract

Currency identity and precision are language-neutral. The database `currencies`
table and `GET /api/v1/currencies` expose `code` and `minor_units`; names and symbols
come from a versioned CLDR resource pack. Contract `2026-09-16` removes the former
`name_en` and `name_zh` currency fields. Deploy the matching App and backend together;
the preceding date is rejected before authentication or writes.

The 148 allowed currency codes and their accounting precision are unchanged.
Integer minor-unit / rational calculations do not use CLDR cash precision or
cash rounding. A reference-label update cannot alter postings.

## Sources and generation

| Path | Role |
| --- | --- |
| `reference/currency-policy.json` | Reviewed code/precision allowlist, locale mapping, default locale, legacy aliases and source hashes |
| `reference/cldr-48/` | Unmodified CLDR 48.0.0 inputs and Unicode license |
| `reference/currencies/catalog.json` | Generated code-to-minor-units metadata |
| `reference/currencies/locales/{locale}.json` | Names, symbols, optional narrow symbols and plural names, keyed by BCP 47 locale |
| `reference/currencies/locale_rules.json` | Main-data parents, likely subtags and aliases for currency-name lookup |
| `reference/currencies/manifest.json` | Schema/CLDR versions, registered paths, source provenance and content hashes |
| `internal/accounting/currencies.json` | Generated flat metadata projection embedded in Go |

The schema version describes Ledger's data shape; the CLDR version describes
upstream content. Per-file and aggregate SHA-256 detect accidental drift; they
are not signatures or a network trust mechanism.

```sh
just generate-currencies
just check-currencies
just export-currencies ../ledger-app
python3 tool/currencies.py --check --app ../ledger-app
```

Generation uses only vendored inputs, verifies their pinned hashes, and checks
every allowed code against standard CLDR `_digits`. It rejects unreviewed precision
changes and incomplete required names. Do not hand-edit generated files.

A release can distribute `reference/currencies/` as a versioned data artifact.
The App requires neither this checkout nor a reference-data request at runtime.
Its independent `python3 tool/check_currencies.py` verifies the exported pack in CI.

To add a locale, vendor its pinned CLDR file, register its source hash and
locale-to-source mapping in the policy, and regenerate/export. No database column,
API currency property or Go/Dart model field is added. CLDR includes historical
currency names, so review eligibility separately. Never change accounting precision
or the allowlist as a side effect of updating labels. New database codes require a
forward seed migration as well as the policy update; historical migrations remain
immutable.

## Locale selection

Published bundles are `en`, `en-CA`, `zh-Hans` and `zh-Hant`. The old `zh` bundle
name aliases to `zh-Hans`. Flutter preserves requested language, script and region
on preference restoration and bootstrap. Name lookup uses aliases, likely subtags
and explicit parents, prevents fallback across scripts, then uses the product's
English fallback. Missing entries show English, then the currency code.

The resolver selects complete generated bundles, not individual CLDR XML nodes.
It is scoped to currency names, not a general ICU implementation. Unicode extensions
do not select different name bundles. UI translations remain English and Simplified
Chinese; region/script preferences refine reference labels within these languages.
Unsupported UI languages also use English currency labels. One shared policy
serves onboarding, account creation and transaction filters. Only the selected
bundle and English fallback are loaded.

For example, USD has symbol `$` in `en`, `US$` in `en-CA`; CNY has name `人民币`
in `zh-Hans`, `人民幣` in `zh-Hant`. Codes remain visible because symbols are
ambiguous. Search includes code, current name, English name and symbol. Exact
amount formatting remains independent of display-name localization.

## Upgrade and rollback

Migration `00004_currency_metadata.sql` removes redundant name columns without
replacing rows or changing account foreign keys. Down restores the exact 148 labels
seeded by migration 00003. Any later code uses its code as a fallback when restoring
that old binary. Precision, journals, revisions and balances remain unchanged.

Run Up during coordinated deployment. Down is an explicit operational rollback,
not a routine production check. Testcontainers creates a USD account under migration
3, upgrades to 4, verifies its 1000.00 balance and exact old-name restoration on
rollback, then tests rollback to 0 and reapplication. It uses no persistent database.

Category and counterparty names are outside this refactor. User-authored names
remain verbatim; preset-category localization can change separately.
