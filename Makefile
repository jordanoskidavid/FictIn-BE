.PHONY: run build test test-integration tidy vet fmt

run:
	go run ./cmd/api

build:
	go build -trimpath -o bin/api ./cmd/api

test:
	go test ./...

# Requires TEST_DATABASE_URL pointed at a throwaway database.
test-integration:
	go test ./... -count=1

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w .
