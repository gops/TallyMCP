# Tally MCP Server Makefile (Dedicated Windows Build)
BINARY_NAME=tally-mcp.exe
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "v1.0.0")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_DATE?=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
WINDOWS_LDFLAGS=-ldflags "-s -w -H=windowsgui -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

.PHONY: all build build-arm release-build test clean fmt

all: test build

# Compile Windows 64-bit standalone executable (System Tray, no cmd window)
build:
	GOOS=windows GOARCH=amd64 go build -mod=vendor $(WINDOWS_LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/tally-mcp

# Compile Windows ARM64 executable
build-arm:
	GOOS=windows GOARCH=arm64 go build -mod=vendor $(WINDOWS_LDFLAGS) -o bin/tally-mcp-arm64.exe ./cmd/tally-mcp

# Build all release packages into dist/
release-build:
	mkdir -p dist
	GOOS=windows GOARCH=amd64 go build -mod=vendor $(WINDOWS_LDFLAGS) -o dist/$(BINARY_NAME) ./cmd/tally-mcp
	GOOS=windows GOARCH=arm64 go build -mod=vendor $(WINDOWS_LDFLAGS) -o dist/tally-mcp-arm64.exe ./cmd/tally-mcp

test:
	go test -v ./...

fmt:
	go fmt ./...

clean:
	rm -rf bin dist tally-mcp.exe
