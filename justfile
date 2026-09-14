sqlc_version := "v1.31.1"

# Show available commands.
default:
    @just --list

run:
    go run ./cmd/api

build:
    go build -o bin/ledger ./cmd/api
    go build -o bin/migrate ./cmd/migrate

test: test-unit

test-unit:
    go test -race -count=1 ./...

vet:
    go vet ./...

fmt:
    gofmt -w cmd internal migrations

generate:
    go run github.com/sqlc-dev/sqlc/cmd/sqlc@{{sqlc_version}} generate

sqlc-vet:
    go run github.com/sqlc-dev/sqlc/cmd/sqlc@{{sqlc_version}} vet

migrate-up:
    go run ./cmd/migrate up

migrate-down:
    go run ./cmd/migrate down

migrate-status:
    go run ./cmd/migrate status

check: check-api sqlc-vet lint test-unit vet build

# Start the persistent local database without deleting existing data.
db-up:
    docker compose up -d --wait postgres

db-down:
    docker compose down

db-logs:
    docker compose logs --tail=100 postgres

# Install pinned lint tooling into the ignored project bin directory.
install-lint:
    GOBIN="{{justfile_directory()}}/bin" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3

lint:
    ./bin/golangci-lint config verify
    ./bin/golangci-lint run
    ./bin/golangci-lint fmt --diff

format:
    ./bin/golangci-lint fmt

# Requires Docker; creates isolated containers and does not read DATABASE_URL.
test-integration:
    go test -race -tags=integration -count=1 -timeout=5m ./tests/integration/...

coverage-unit:
    mkdir -p coverage
    go test -race -covermode=atomic -coverprofile=coverage/unit.out ./internal/...

# Keep each fuzz target scoped to a single package.
fuzz package="./internal/config" target="FuzzGinMode" duration="10s":
    go test {{package}} -run='^$' -fuzz='^{{target}}$' -fuzztime={{duration}}

# Generate the HTTP interface and DTOs from the canonical OpenAPI 3.1 contract.
generate-api:
    go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 --config openapi-codegen.yaml -o internal/apiv1/api.gen.go internal/apicontract/v1/openapi.json

# Compare in a temporary file, including workspaces without Git.
check-api:
    #!/usr/bin/env sh
    set -eu
    temporary="$(mktemp)"
    trap 'rm -f "$temporary"' EXIT
    go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 --config openapi-codegen.yaml -o "$temporary" internal/apicontract/v1/openapi.json
    cmp internal/apiv1/api.gen.go "$temporary"
