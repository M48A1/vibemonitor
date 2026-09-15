.PHONY: all build test clean release-all

BINARY_NAME=vibemonitor
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS=-s -w -X vibemonitor/internal/version.Version=$(VERSION) -X vibemonitor/internal/version.Commit=$(COMMIT)

all: build

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) .

test:
	@case "$$(uname -s)/$$(uname -m)" in Linux/x86_64|Linux/aarch64|Linux/arm64) ;; *) echo "Full tests require Linux (x86-64 or arm64)."; exit 1 ;; esac
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
	rm -rf dist

release-all:
	mkdir -p dist
	# Linux AMD64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-amd64 .
	# Linux ARM64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-arm64 .
