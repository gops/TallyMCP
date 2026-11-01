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
