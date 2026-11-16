package tally

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// StartResult encapsulates the response when attempting to start Tally.
type StartResult struct {
	Status         string        `json:"status"` // "already_running", "started", "started_waiting", "failed"
	Message        string        `json:"message"`
	ExePath        string        `json:"exe_path,omitempty"`
	Connected      bool          `json:"connected"`
	CompaniesCount int           `json:"companies_count,omitempty"`
	Companies      []CompanyItem `json:"companies,omitempty"`
	Error          string        `json:"error,omitempty"`
}

// FindTallyExecutable attempts to locate the Tally executable.
// If customPath is non-empty, it verifies that file.
// Otherwise it inspects the Windows registry (on Windows), standard directory candidates, and system PATH.
func FindTallyExecutable(customPath string) (string, error) {
	if customPath != "" {
		expanded := os.ExpandEnv(customPath)
		clean := strings.Trim(strings.TrimSpace(expanded), "\"")
		if fi, err := os.Stat(clean); err == nil && !fi.IsDir() {
			return clean, nil
		}
		if lp, err := exec.LookPath(clean); err == nil {
			return lp, nil
		}
		return "", fmt.Errorf("specified Tally executable not found at %s", customPath)
	}

	// 1. Check Windows Registry (Windows only)
	if regPath := findWindowsRegistryTally(); regPath != "" {
		clean := strings.Trim(strings.TrimSpace(regPath), "\"")
		if fi, err := os.Stat(clean); err == nil && !fi.IsDir() {
			return clean, nil
		}
	}

	// 2. Check standard directory locations
	for _, candidate := range getStandardWindowsPaths() {
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
	}

	// 3. Check system PATH
	for _, binName := range []string{"tally.exe", "tally", "TallyPrime.exe"} {
		if lp, err := exec.LookPath(binName); err == nil {
			return lp, nil
		}
	}

	return "", fmt.Errorf("tally executable not found in standard installation directories; please configure 'exe_path' in config.yaml or pass --tally-path")
}

// isLocalURL returns true if rawURL points to localhost or a local IP interface.
func isLocalURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := parsed.Hostname()
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "0.0.0.0" {
		return true
	}
	// Check local network interfaces
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ipnet.IP.String() == h {
					return true
				}
			}
		}
	}
	return false
}

// isConnectionRefused returns true if err indicates Tally's HTTP port is not reachable.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no connection could be made") ||
		strings.Contains(s, "actively refused") ||
		strings.Contains(s, "connectex") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "dial tcp")
}

// ExePath returns the configured Tally executable path.
func (c *Client) ExePath() string {
	return c.exePath
}

// SetExePath updates the configured Tally executable path.
func (c *Client) SetExePath(path string) {
	c.exePath = path
}

// AutoStartEnabled returns true if auto-start is configured.
func (c *Client) AutoStartEnabled() bool {
	return c.autoStart
}

// SetAutoStart updates the auto-start behavior.
func (c *Client) SetAutoStart(enabled bool) {
	c.autoStart = enabled
}

// FindExecutable resolves the Tally executable path for this client.
func (c *Client) FindExecutable(customPath string) (string, error) {
	target := customPath
	if target == "" {
		target = c.exePath
	}
	return FindTallyExecutable(target)
}

// directPing performs a direct HTTP check against Tally without acquiring client query semaphores.
// This is used for polling readiness during startup.
func (c *Client) directPing(ctx context.Context) (*ServerStatus, error) {
	quickClient := &http.Client{
		Timeout:   3 * time.Second,
		Transport: c.httpClient.Transport,
	}

	xmlQuery := BuildListCompaniesXML()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewBufferString(xmlQuery))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml;charset=utf-8")

	resp, err := quickClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				Companies  []CompanyItem `xml:"COMPANY"`
				Collection struct {
					Companies []CompanyItem `xml:"COMPANY"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	companies := []CompanyItem{}
	if err := xml.Unmarshal(body, &env); err == nil {
		companies = env.Body.Data.Companies
		if len(companies) == 0 && len(env.Body.Data.Collection.Companies) > 0 {
			companies = env.Body.Data.Collection.Companies
		}
	}

	return &ServerStatus{
		Connected:      true,
		TallyURL:       c.baseURL,
		TargetCompany:  c.defaultCompany,
		Companies:      companies,
		CompaniesCount: len(companies),
	}, nil
}

// IsRunning returns true if Tally is currently accepting HTTP queries.
func (c *Client) IsRunning(ctx context.Context) bool {
	status, err := c.directPing(ctx)
	return err == nil && status != nil && status.Connected
}

// WaitForReady polls Tally's HTTP endpoint until it responds or the timeout expires.
func (c *Client) WaitForReady(ctx context.Context, timeout time.Duration) (*ServerStatus, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			break
		}

		status, err := c.directPing(ctx)
		if err == nil && status != nil && status.Connected {
			return status, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}

	return nil, fmt.Errorf("timeout waiting for Tally to respond at %s (waited %v)", c.baseURL, timeout)
}

// StartTally launches Tally if it is not currently running and waits for it to become ready.
func (c *Client) StartTally(ctx context.Context, customPath string, waitTimeout time.Duration) (*StartResult, error) {
	if waitTimeout <= 0 {
		waitTimeout = 30 * time.Second
	}

	// 1. Ensure Tally is local
	if !isLocalURL(c.baseURL) {
		msg := fmt.Sprintf("Tally URL (%s) is remote. Starting Tally is only supported for local installations (localhost / 127.0.0.1).", c.baseURL)
		return &StartResult{
			Status:    "failed",
			Connected: false,
			Message:   msg,
			Error:     "remote Tally instance cannot be started locally",
		}, fmt.Errorf("cannot start remote Tally instance at %s", c.baseURL)
	}

	// 2. Check if already running
	if c.IsRunning(ctx) {
		status, _ := c.directPing(ctx)
		count := 0
		var companies []CompanyItem
		if status != nil {
			count = status.CompaniesCount
			companies = status.Companies
		}
		return &StartResult{
			Status:         "already_running",
			Connected:      true,
			Message:        fmt.Sprintf("Tally ERP / TallyPrime is already running and connected at %s.", c.baseURL),
			CompaniesCount: count,
			Companies:      companies,
		}, nil
	}

	// Acquire launch mutex to ensure strictly one launch process is executed
	c.launchMu.Lock()
	defer c.launchMu.Unlock()

	// Double-check if another caller started it while acquiring lock
	if c.IsRunning(ctx) {
		status, _ := c.directPing(ctx)
		count := 0
		var companies []CompanyItem
		if status != nil {
			count = status.CompaniesCount
			companies = status.Companies
		}
		return &StartResult{
			Status:         "already_running",
			Connected:      true,
			Message:        fmt.Sprintf("Tally ERP / TallyPrime is already running and connected at %s.", c.baseURL),
			CompaniesCount: count,
			Companies:      companies,
		}, nil
	}

	// 3. Find executable
	targetExe := customPath
	if targetExe == "" {
		targetExe = c.exePath
	}
	resolvedExe, err := FindTallyExecutable(targetExe)
	if err != nil {
		return &StartResult{
			Status:    "failed",
			Connected: false,
			Message:   err.Error(),
			Error:     err.Error(),
		}, err
	}

	// 4. Launch OS process
	if err := launchOSProcess(resolvedExe); err != nil {
		return &StartResult{
			Status:    "failed",
			Connected: false,
			ExePath:   resolvedExe,
			Message:   fmt.Sprintf("Failed to launch Tally process: %v", err),
			Error:     err.Error(),
		}, err
	}

	// 5. Wait for readiness
	status, err := c.WaitForReady(ctx, waitTimeout)
	if err != nil {
		return &StartResult{
			Status:    "started_waiting",
			Connected: false,
			ExePath:   resolvedExe,
			Message:   fmt.Sprintf("Tally process (%s) was launched, but port did not respond within %v. Tally may still be initializing or company needs selection.", resolvedExe, waitTimeout),
			Error:     err.Error(),
		}, nil
	}

	return &StartResult{
		Status:         "started",
		Connected:      true,
		ExePath:        resolvedExe,
		Message:        fmt.Sprintf("Tally ERP / TallyPrime started successfully and is accepting requests on %s.", c.baseURL),
		CompaniesCount: status.CompaniesCount,
		Companies:      status.Companies,
	}, nil
}

// EnsureRunning checks if Tally is running and starts it if necessary.
func (c *Client) EnsureRunning(ctx context.Context, waitTimeout time.Duration) (*StartResult, error) {
	return c.StartTally(ctx, c.exePath, waitTimeout)
}
