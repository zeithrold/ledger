# Recipes contain no platform-specific shell logic; the pinned Go bootstrap owns execution.
set windows-shell := ["powershell.exe", "-NoLogo", "-NoProfile", "-Command"]
export GOTOOLCHAIN := "go1.26.6"

default:
    @just --list

check:
    go run ./tool/bootstrap.go check

security:
    go run ./tool/bootstrap.go security

security-secrets:
    go run ./tool/bootstrap.go security-secrets

security-dependencies:
    go run ./tool/bootstrap.go security-dependencies

policy-check base="":
    go run ./tool/bootstrap.go policy-check {{if base == "" { "" } else { "--base " + if os() == "windows" { "'" + replace(base, "'", "''") + "'" } else { quote(base) } }}}

skills-check:
    go run ./tool/bootstrap.go skills-check

recipes-check:
    go run ./tool/bootstrap.go recipes-check

commit-check title:
    go run ./tool/bootstrap.go commit-check {{if os() == "windows" { "'" + replace(title, "'", "''") + "'" } else { quote(title) }}}

fuzz-nightly:
    go run ./tool/bootstrap.go fuzz-nightly

run:
    go run ./tool/bootstrap.go run

build:
    go run ./tool/bootstrap.go build

test: test-unit

test-unit:
    go run ./tool/bootstrap.go test-unit

test-integration:
    go run ./tool/bootstrap.go test-integration

coverage-unit:
    go run ./tool/bootstrap.go coverage-unit

coverage-check base="":
    go run ./tool/bootstrap.go coverage-check {{if base == "" { "" } else { "--base " + if os() == "windows" { "'" + replace(base, "'", "''") + "'" } else { quote(base) } }}}

lint:
    go run ./tool/bootstrap.go lint

vet:
    go run ./tool/bootstrap.go vet

format:
    go run ./tool/bootstrap.go format

fmt: format

architecture-check:
    go run ./tool/bootstrap.go architecture-check

generate:
    go run ./tool/bootstrap.go generate

sqlc-vet:
    go run ./tool/bootstrap.go sqlc-vet

check-sqlc:
    go run ./tool/bootstrap.go check-sqlc

generate-api:
    go run ./tool/bootstrap.go generate-api

check-api:
    go run ./tool/bootstrap.go check-api

generate-currencies:
    go run ./tool/bootstrap.go currency

check-currencies:
    go run ./tool/bootstrap.go currency --check

export-currencies app:
    go run ./tool/bootstrap.go currency --app {{if os() == "windows" { "'" + replace(app, "'", "''") + "'" } else { quote(app) }}}

migrate-up:
    go run ./tool/bootstrap.go migrate-up

migrate-down:
    go run ./tool/bootstrap.go migrate-down

migrate-status:
    go run ./tool/bootstrap.go migrate-status

db-up:
    go run ./tool/bootstrap.go db-up

db-down:
    go run ./tool/bootstrap.go db-down

db-logs:
    go run ./tool/bootstrap.go db-logs

fuzz:
    go run ./tool/bootstrap.go fuzz

mutation-accounting:
    go run ./tool/bootstrap.go mutation-accounting

changes base="":
    go run ./tool/bootstrap.go changes {{if base == "" { "" } else { "--base " + if os() == "windows" { "'" + replace(base, "'", "''") + "'" } else { quote(base) } }}}

review-check file=".governance/review.json":
    go run ./tool/bootstrap.go review-check --file {{if os() == "windows" { "'" + replace(file, "'", "''") + "'" } else { quote(file) }}}

fingerprint:
    go run ./tool/bootstrap.go fingerprint

debug-start case:
    go run ./tool/bootstrap.go debug-start  {{if os() == "windows" { "'" + replace(case, "'", "''") + "'" } else { quote(case) }}}

debug-run session profile:
    go run ./tool/bootstrap.go debug-run  {{if os() == "windows" { "'" + replace(session, "'", "''") + "'" } else { quote(session) }}} {{if os() == "windows" { "'" + replace(profile, "'", "''") + "'" } else { quote(profile) }}}

debug-verify session profile:
    go run ./tool/bootstrap.go debug-verify  {{if os() == "windows" { "'" + replace(session, "'", "''") + "'" } else { quote(session) }}} {{if os() == "windows" { "'" + replace(profile, "'", "''") + "'" } else { quote(profile) }}}

debug-report session:
    go run ./tool/bootstrap.go debug-report  {{if os() == "windows" { "'" + replace(session, "'", "''") + "'" } else { quote(session) }}}

ui-report manifest=".governance/ui.json":
    go run ./tool/bootstrap.go ui-report --manifest {{if os() == "windows" { "'" + replace(manifest, "'", "''") + "'" } else { quote(manifest) }}}

doctor:
    go run ./tool/bootstrap.go doctor

bootstrap:
    go run ./tool/bootstrap.go bootstrap

arch: architecture-check

coverage: coverage-unit test-integration coverage-check

mutation: mutation-accounting
