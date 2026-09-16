# Recipes contain no platform-specific shell logic; the pinned Go bootstrap owns execution.
set windows-shell := ["powershell.exe", "-NoLogo", "-NoProfile", "-Command"]
export GOTOOLCHAIN := "go1.26.6"

[private]
export-currencies app:
    go run ./tool/bootstrap.go currency --app {{if os() == "windows" { "'" + replace(app, "'", "''") + "'" } else { quote(app) }}}

[private]
default:
    @just --list

check:
    go run ./tool/bootstrap.go check

[private]
security:
    go run ./tool/bootstrap.go security

[private]
security-secrets:
    go run ./tool/bootstrap.go security-secrets

[private]
security-dependencies:
    go run ./tool/bootstrap.go security-dependencies

[private]
policy-check base="":
    go run ./tool/bootstrap.go policy-check {{if base == "" { "" } else { "--base " + if os() == "windows" { "'" + replace(base, "'", "''") + "'" } else { quote(base) } }}}

[private]
skills-check:
    go run ./tool/bootstrap.go skills-check

[private]
recipes-check:
    go run ./tool/bootstrap.go recipes-check

[private]
commit-check title:
    go run ./tool/bootstrap.go commit-check {{if os() == "windows" { "'" + replace(title, "'", "''") + "'" } else { quote(title) }}}

[private]
fuzz-nightly:
    go run ./tool/bootstrap.go fuzz-nightly

[private]
run:
    go run ./tool/bootstrap.go run

[private]
worker:
    go run ./tool/bootstrap.go worker

[private]
build:
    go run ./tool/bootstrap.go build

test: test-unit

[private]
test-unit:
    go run ./tool/bootstrap.go test-unit

[private]
test-integration:
    go run ./tool/bootstrap.go test-integration

[private]
coverage-unit:
    go run ./tool/bootstrap.go coverage-unit

[private]
coverage-check base="":
    go run ./tool/bootstrap.go coverage-check {{if base == "" { "" } else { "--base " + if os() == "windows" { "'" + replace(base, "'", "''") + "'" } else { quote(base) } }}}

lint:
    go run ./tool/bootstrap.go lint

[private]
vet:
    go run ./tool/bootstrap.go vet

[private]
format:
    go run ./tool/bootstrap.go format

fmt: format

[private]
architecture-check:
    go run ./tool/bootstrap.go architecture-check

[private]
generate:
    go run ./tool/bootstrap.go generate

[private]
sqlc-vet:
    go run ./tool/bootstrap.go sqlc-vet

[private]
check-sqlc:
    go run ./tool/bootstrap.go check-sqlc

[private]
generate-api:
    go run ./tool/bootstrap.go generate-api

[private]
check-api:
    go run ./tool/bootstrap.go check-api

[private]
generate-currencies:
    go run ./tool/bootstrap.go currency

[private]
check-currencies:
    go run ./tool/bootstrap.go currency --check

[private]
migrate-up:
    go run ./tool/bootstrap.go migrate-up

[private]
migrate-down:
    go run ./tool/bootstrap.go migrate-down

[private]
migrate-status:
    go run ./tool/bootstrap.go migrate-status

[private]
db-up:
    go run ./tool/bootstrap.go db-up

[private]
db-down:
    go run ./tool/bootstrap.go db-down

[private]
db-logs:
    go run ./tool/bootstrap.go db-logs

[private]
fuzz:
    go run ./tool/bootstrap.go fuzz

[private]
mutation-accounting:
    go run ./tool/bootstrap.go mutation-accounting

changes base="":
    go run ./tool/bootstrap.go changes {{if base == "" { "" } else { "--base " + if os() == "windows" { "'" + replace(base, "'", "''") + "'" } else { quote(base) } }}}

[private]
review-check file=".governance/review.json":
    go run ./tool/bootstrap.go review-check --file {{if os() == "windows" { "'" + replace(file, "'", "''") + "'" } else { quote(file) }}}

[private]
fingerprint:
    go run ./tool/bootstrap.go fingerprint

[private]
debug-start case:
    go run ./tool/bootstrap.go debug-start  {{if os() == "windows" { "'" + replace(case, "'", "''") + "'" } else { quote(case) }}}

[private]
debug-run session profile:
    go run ./tool/bootstrap.go debug-run  {{if os() == "windows" { "'" + replace(session, "'", "''") + "'" } else { quote(session) }}} {{if os() == "windows" { "'" + replace(profile, "'", "''") + "'" } else { quote(profile) }}}

[private]
debug-verify session profile:
    go run ./tool/bootstrap.go debug-verify  {{if os() == "windows" { "'" + replace(session, "'", "''") + "'" } else { quote(session) }}} {{if os() == "windows" { "'" + replace(profile, "'", "''") + "'" } else { quote(profile) }}}

[private]
debug-report session:
    go run ./tool/bootstrap.go debug-report  {{if os() == "windows" { "'" + replace(session, "'", "''") + "'" } else { quote(session) }}}

[private]
ui-report manifest=".governance/ui.json":
    go run ./tool/bootstrap.go ui-report --manifest {{if os() == "windows" { "'" + replace(manifest, "'", "''") + "'" } else { quote(manifest) }}}

[private]
doctor:
    go run ./tool/bootstrap.go doctor

[private]
bootstrap:
    go run ./tool/bootstrap.go bootstrap

arch: architecture-check

[private]
coverage: coverage-unit test-integration coverage-check

