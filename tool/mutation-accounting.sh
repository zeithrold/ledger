#!/usr/bin/env bash
set -euo pipefail

mutation_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mutation_tool="${GREMLINS:-$mutation_root/bin/gremlins}"
mutation_output="$mutation_root/mutation-results"
mkdir -p "$mutation_output"
if ! go version -m "$mutation_tool" | awk '
  $1 == "mod" && $2 == "github.com/go-gremlins/gremlins" && $3 == "v0.6.0" { found = 1 }
  END { exit !found }
'; then
  echo 'Install the pinned Gremlins v0.6.0 binary with just install-mutation.' >&2
  exit 1
fi

mutation_copy="$(mktemp -d)"
trap 'rm -rf "$mutation_copy"' EXIT
# An allowlist keeps application configuration and credentials out of Gremlins'
# own temporary copies. These are exact copies of the production pure functions.
cp "$mutation_root/internal/accounting/money.go" \
  "$mutation_root/internal/accounting/money_test.go" \
  "$mutation_root/internal/accounting/currencies.json" "$mutation_copy/"
printf 'module ledger-accounting-mutations\n\ngo 1.26.0\n' > "$mutation_copy/go.mod"
cd "$mutation_copy"
"$mutation_tool" unleash . --workers 2 --timeout-coefficient 20 --output "$mutation_output/accounting.json"
