package mcp_test

import (
	"fmt"
	"net"
	"testing"

	"tally-mcp/internal/mcp"
)

func TestBindAvailableListener_Normal(t *testing.T) {
	// Let OS assign a free port first to find a safe test port
	dummy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := dummy.Addr().(*net.TCPAddr).Port
	dummy.Close()

	l, addr, activePort, changed, err := mcp.BindAvailableListener(fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer l.Close()

	if changed {
		t.Errorf("expected changed=false for free port %d", port)
	}
	if activePort != port {
		t.Errorf("expected activePort=%d, got %d", port, activePort)
	}
	if addr != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Errorf("expected addr=127.0.0.1:%d, got %s", port, addr)
	}
}

func TestBindAvailableListener_PortBusy(t *testing.T) {
	// Occupy a port
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create blocker listener: %v", err)
	}
	defer blocker.Close()

	busyPort := blocker.Addr().(*net.TCPAddr).Port

	// Attempt to bind to the busy port -> should automatically pick busyPort + 1 (or next available)
	l, _, activePort, changed, err := mcp.BindAvailableListener(fmt.Sprintf("127.0.0.1:%d", busyPort))
	if err != nil {
		t.Fatalf("unexpected error when resolving port conflict: %v", err)
	}
	defer l.Close()

	if !changed {
		t.Errorf("expected changed=true when requested port %d was busy", busyPort)
	}
	if activePort == busyPort {
		t.Errorf("expected activePort to be different from busy port %d, but got %d", busyPort, activePort)
	}
	if activePort <= busyPort {
		t.Errorf("expected activePort > busyPort (%d), got %d", busyPort, activePort)
	}
}

func TestParseHostAndPort(t *testing.T) {
	tests := []struct {
		input        string
		expectedHost string
		expectedPort int
	}{
		{"", "0.0.0.0", 8080},
		{"0.0.0.0:8080", "0.0.0.0", 8080},
		{"127.0.0.1:9000", "127.0.0.1", 9000},
		{"8080", "0.0.0.0", 8080},
		{"8085", "0.0.0.0", 8085},
		{":8080", "0.0.0.0", 8080},
		{"localhost", "localhost", 8080},
		{"127.0.0.1", "127.0.0.1", 8080},
		{"0.0.0.0:invalid", "0.0.0.0", 8080},
	}

	for _, tc := range tests {
		h, p := mcp.ParseHostAndPort(tc.input)
		if h != tc.expectedHost || p != tc.expectedPort {
			t.Errorf("ParseHostAndPort(%q) = (%q, %d), expected (%q, %d)", tc.input, h, p, tc.expectedHost, tc.expectedPort)
		}
	}
}

func TestBindAvailableListener_SequentialIncrement(t *testing.T) {
	// Let OS assign a base port
	dummy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	basePort := dummy.Addr().(*net.TCPAddr).Port
	dummy.Close()

	// Occupy basePort
	l1, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", basePort))
	if err != nil {
		t.Skip("could not bind basePort, skipping")
	}
	defer l1.Close()

	// Occupy basePort + 1
	nextPort := basePort + 1
	l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", nextPort))
	if err != nil {
		t.Skip("could not bind nextPort, skipping")
	}
	defer l2.Close()

	// Attempting to bind starting at basePort should skip basePort and nextPort, finding nextPort+1 or higher
	l3, _, activePort, changed, err := mcp.BindAvailableListener(fmt.Sprintf("127.0.0.1:%d", basePort))
	if err != nil {
		t.Fatalf("BindAvailableListener failed: %v", err)
	}
	defer l3.Close()

	if !changed {
		t.Errorf("expected changed=true because %d and %d are occupied", basePort, nextPort)
	}
	if activePort <= nextPort {
		t.Errorf("expected activePort > %d, got %d", nextPort, activePort)
	}
}
