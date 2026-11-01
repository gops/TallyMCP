package mcp

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// BindAvailableListener attempts to bind a TCP listener to the configured host and port.
// If the port is already in use by another application, it automatically scans subsequent
// ports (up to startPort + 100) until an available port is found.
//
// Returns:
//   - listener: the bound net.Listener
//   - boundAddr: e.g. "0.0.0.0:8081"
//   - activePort: e.g. 8081
//   - portChanged: true if the active port differs from the configured port
//   - err: nil on success
func BindAvailableListener(listenAddr string) (net.Listener, string, int, bool, error) {
	host, portStr, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = "0.0.0.0"
		portStr = "8080"
	}
	if host == "" {
		host = "0.0.0.0"
	}

	startPort, err := strconv.Atoi(portStr)
	if err != nil || startPort <= 0 || startPort > 65535 {
		startPort = 8080
	}

	maxAttempts := 100 // Try ports up to startPort + 100

	for p := startPort; p < startPort+maxAttempts && p <= 65535; p++ {
		candidate := fmt.Sprintf("%s:%d", host, p)
		l, err := net.Listen("tcp", candidate)
		if err == nil {
			portChanged := (p != startPort)
			if portChanged {
				log.Printf("[WARN] Configured port %d was busy / occupied by another application. Automatically switched to port %d.", startPort, p)
			}
			return l, candidate, p, portChanged, nil
		}

		if isAddrInUse(err) {
			continue
		}

		// Other fatal error (e.g. invalid host or permission denied)
		return nil, "", 0, false, fmt.Errorf("binding to %s: %w", candidate, err)
	}

	return nil, "", 0, false, fmt.Errorf("no available ports found in range %d-%d on host %s", startPort, startPort+maxAttempts, host)
}

// isAddrInUse checks whether an error indicates that the address/port is already occupied.
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errno, ok := sysErr.Err.(syscall.Errno); ok {
				// 10048 = WSAEADDRINUSE (Windows)
				// 98 = EADDRINUSE (Linux)
				// 48 = EADDRINUSE (macOS)
				if errno == syscall.EADDRINUSE || errno == 10048 {
					return true
				}
			}
		}
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "address already in use") ||
		strings.Contains(errStr, "bind: address already in use") ||
		strings.Contains(errStr, "only one usage of each socket address")
}
