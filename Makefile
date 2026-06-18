BINARY := bodek
PKG    := ./cmd/bodek
GOBIN  ?= $(shell go env GOPATH)/bin

.PHONY: all build install run fmt vet lint test cover tidy clean

all: build

## build: compile the bodek binary into ./bin
build:
	@mkdir -p bin
	go build -o bin/$(BINARY) $(PKG)

## install: install bodek into $GOBIN
install:
	go install $(PKG)

## run: build and launch bodek (spawns `odek serve`)
run: build
	./bin/$(BINARY)

## fmt: format all Go sources
fmt:
	go fmt ./...

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint if available
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; skipping (see https://golangci-lint.run)"; \
	fi

## test: run the race-enabled test suite
test:
	go test -race ./...

## cover: print internal-package coverage
cover:
	go test -coverpkg=./internal/... -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | tail -1

## tidy: tidy module dependencies
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin
