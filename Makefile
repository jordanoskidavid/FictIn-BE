.PHONY: run build test test-integration test-all cover cover-check lint tidy vet fmt db-up db-down

# -race needs cgo (a C compiler). Disable with: make test RACE=
RACE ?= -race
# Minimum total coverage enforced by cover-check (and CI).
COVERAGE_MIN ?= 0
# Packages whose coverage is measured (test helpers excluded).
COVERPKG = $(shell go list ./internal/... | grep -v /testutil | paste -sd, -)

run:
	go run ./cmd/api

build:
	go build -trimpath -o bin/api ./cmd/api

# Unit tests only: no database, network or Docker needed.
test:
	go test $(RACE) ./...

# Integration tests only (needs Docker, or TEST_POSTGRES_URL; see CONTRIBUTING.md).
test-integration:
	go test $(RACE) -tags=integration -count=1 ./tests/integration/...

# Unit + integration tests.
test-all:
	go test $(RACE) -tags=integration -count=1 ./...

# Full suite with coverage; writes coverage.out and prints the total.
cover:
	go test $(RACE) -tags=integration -count=1 -covermode=atomic -coverpkg=$(COVERPKG) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

cover-check: cover
	sh scripts/check-coverage.sh coverage.out $(COVERAGE_MIN)

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

vet:
	go vet ./...
	go vet -tags=integration ./...

fmt:
	gofmt -w .

# Local development database (docker-compose.yml).
db-up:
	docker compose up -d db

db-down:
	docker compose down
