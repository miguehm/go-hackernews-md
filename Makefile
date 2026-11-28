# Common development tasks. `make check` runs what CI runs.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE   ?= hnmd

.PHONY: help fmt fmt-check vet lint test test-race cover vuln build run docker check

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-10s %s\n", $$1, $$2}'

fmt: ## Format code
	gofmt -w .

fmt-check: ## Fail if code is not gofmt-formatted
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## go vet
	go vet ./...

lint: ## golangci-lint (config in .golangci.yml)
	golangci-lint run ./...

test: ## Unit tests
	go test ./...

test-race: ## Tests with the race detector
	go test -race ./...

cover: ## Coverage report (coverage.out)
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vuln: ## govulncheck
	govulncheck ./...

build: ## Build bin/server with the version embedded
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/server ./cmd/server

run: build ## Build and run locally
	./bin/server

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

check: fmt-check vet lint test-race vuln ## Everything CI checks
