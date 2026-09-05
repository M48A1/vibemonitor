.PHONY: all build test clean release-all

BINARY_NAME=vibemonitor
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS=-s -w -X vibemonitor/internal/version.Version=$(VERSION) -X vibemonitor/internal/version.Commit=$(COMMIT)

all: build

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) .

test:
	@test "$$(uname -s)/$$(uname -m)" = "Linux/x86_64" || { echo "Full tests require Linux x86-64."; exit 1; }
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
	rm -f *.json.tmp test-*.json
	rm -rf dist

release-all:
	mkdir -p dist
	# Linux AMD64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-amd64 .
