# Tally MCP Server Makefile
BINARY_NAME=tally-mcp
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "v1.0.0")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_DATE?=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS=-ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

.PHONY: all build build-windows test clean fmt run

all: test build

build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/tally-mcp

# Cross compile standalone Windows 64-bit executable
build-windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME).exe ./cmd/tally-mcp

# Cross compile Windows ARM64 executable
build-windows-arm:
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-arm64.exe ./cmd/tally-mcp

# Build all release targets into dist/
release-build:
	mkdir -p dist
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME).exe ./cmd/tally-mcp
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-arm64.exe ./cmd/tally-mcp
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 ./cmd/tally-mcp
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 ./cmd/tally-mcp
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 ./cmd/tally-mcp

test:
	go test -v ./...

fmt:
	go fmt ./...

clean:
	rm -rf bin dist tally-mcp.exe
