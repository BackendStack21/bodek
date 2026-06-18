BINARY := bodek
PKG    := ./cmd/bodek
GOBIN  ?= $(shell go env GOPATH)/bin

.PHONY: all build install run fmt vet tidy clean

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

## tidy: tidy module dependencies
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin
