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

// ParseHostAndPort splits an input listen address into host and port.
// It guarantees port 8080 is used as the default starting port if no port is specified or if invalid.
func ParseHostAndPort(listenAddr string) (string, int) {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return "0.0.0.0", 8080
	}

	// Case 1: Plain port number like "8080"
	if p, err := strconv.Atoi(listenAddr); err == nil && p > 0 && p <= 65535 {
		return "0.0.0.0", p
	}

	// Case 2: Host:Port or :Port
	host, portStr, err := net.SplitHostPort(listenAddr)
	if err == nil {
		if host == "" {
			host = "0.0.0.0"
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p <= 0 || p > 65535 {
			p = 8080
		}
		return host, p
	}

	// Case 3: Plain host without port (e.g., "127.0.0.1", "localhost")
	return listenAddr, 8080
}

// BindAvailableListener attempts to bind a TCP listener starting at the configured host and port (defaulting to 8080).
// It tests port 8080 (or the requested port) first. If available, it binds it immediately.
// If port 8080 is occupied or unavailable, it automatically increments one by one (8081, 8082, ...)
// until an open port is successfully found and bound.
//
// Returns:
//   - listener: the bound net.Listener
//   - boundAddr: e.g. "0.0.0.0:8080" or "0.0.0.0:8081"
//   - activePort: e.g. 8080 or 8081
//   - portChanged: true if the active port differs from the initial requested port
//   - err: nil on success
func BindAvailableListener(listenAddr string) (net.Listener, string, int, bool, error) {
	host, startPort := ParseHostAndPort(listenAddr)
	if startPort <= 0 || startPort > 65535 {
		startPort = 8080
	}

	// Maximum consecutive ports to scan when searching for an open port
	maxAttempts := 500

	for p := startPort; p < startPort+maxAttempts && p <= 65535; p++ {
		candidate := fmt.Sprintf("%s:%d", host, p)
		l, err := net.Listen("tcp", candidate)
		if err == nil {
			portChanged := (p != startPort)
			if portChanged {
				log.Printf("[WARN] Port %d was busy. Automatically incremented and bound to open port %d.", startPort, p)
			} else {
				log.Printf("[INFO] Successfully bound to primary port %d on %s.", p, host)
			}
			return l, candidate, p, portChanged, nil
		}

		if isAddrInUse(err) {
			continue
		}

		// Other fatal error (e.g. invalid host format or OS permission denied on low port < 1024)
		return nil, "", 0, false, fmt.Errorf("binding to %s: %w", candidate, err)
	}

	return nil, "", 0, false, fmt.Errorf("no available ports found in range %d-%d on host %s", startPort, startPort+maxAttempts, host)
}

// isAddrInUse checks whether an error indicates that the address/port is already occupied or unavailable.
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
				// 10013 = WSAEACCES (Windows permission denied when port is reserved by Hyper-V / WSL / excluded port range)
				// 13 = EACCES (Linux permission denied)
				if errno == syscall.EACCES || errno == 10013 {
					return true
				}
			}
		}
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "address already in use") ||
		strings.Contains(errStr, "bind: address already in use") ||
		strings.Contains(errStr, "only one usage of each socket address") ||
		strings.Contains(errStr, "permission denied") ||
		strings.Contains(errStr, "access permissions")
}
