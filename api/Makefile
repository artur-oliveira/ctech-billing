# The deployed binary MUST be named `app` — the shared CDK userdata expects
# /opt/app/current/app.
BINARY      := app
BUILD_DIR   := dist
GOOS        ?= linux
GOARCH      ?= arm64
CGO_ENABLED := 0

.PHONY: build lint test test-integration vet cover clean

# Every binary ships together. The jobs are one-shot processes, not services, but
# they run the same domain code against the same tables — building one from a
# different commit than the API is a way to bill from logic nobody reviewed.
build:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY) ./cmd/server
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/sweep ./cmd/sweep
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/reconcile ./cmd/reconcile
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/deliver ./cmd/deliver
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/dunning ./cmd/dunning
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/seed ./cmd/seed

lint:
	golangci-lint run ./...

test:
	go test ./... -race -coverprofile=coverage.out

# Requires DynamoDB Local: docker compose -f docker-compose.test.yml up -d
#
# These are not optional extras. The repository layer's interesting failures —
# a condition that does not fire, a transaction that is not atomic, an index the
# service rejects — cannot be reproduced against a fake that agrees with our
# assumptions.
test-integration:
	DYNAMODB_ENDPOINT=http://localhost:8124 \
		go test -tags integration -race -count=1 ./tests/integration/ -v

vet:
	go vet ./...

cover: test
	go tool cover -func=coverage.out | tail -1

clean:
	rm -rf $(BUILD_DIR) coverage.out
