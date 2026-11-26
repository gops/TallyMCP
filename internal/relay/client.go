package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MCPDispatcher handles a single JSON-RPC MCP message on behalf of a relayed 1erp.work request.
// allowWrites indicates whether mutating operations are permitted over the relay.
type MCPDispatcher func(ctx context.Context, jsonRPCBody []byte, allowWrites bool) []byte

// RelayState represents the live connection status of the 1erp.work relay.
type RelayState string

const (
	StateDisconnected RelayState = "disconnected"
	StateAuthRequired RelayState = "auth_required"
	StateConnecting   RelayState = "connecting"
	StateConnected    RelayState = "connected"
)

// StatusSnapshot describes the current 1erp.work relay state for UI/CLI reporting.
type StatusSnapshot struct {
	State       RelayState `json:"state"`
	LoggedIn    bool       `json:"logged_in"`
	UserEmail   string     `json:"user_email,omitempty"`
	TenantID    string     `json:"tenant_id,omitempty"`
	DeviceID    string     `json:"device_id,omitempty"`
	ServerURL   string     `json:"server_url,omitempty"`
	AllowWrites bool       `json:"allow_writes"`
	LastError   string     `json:"last_error,omitempty"`
}

// RelayEnvelope is the JSON frame exchanged between tally-mcp and 1erp.work over WebSocket.
type RelayEnvelope struct {
	Type        string          `json:"type"`
	ID          string          `json:"id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Filename    string          `json:"filename,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	DataBase64  string          `json:"data_base64,omitempty"`
	Error       string          `json:"error,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	DeviceName  string          `json:"device_name,omitempty"`
	Version     string          `json:"version,omitempty"`
	ReadOnly    bool            `json:"read_only,omitempty"`
}

// Client maintains an authenticated, outbound-only WebSocket tunnel to 1erp.work.
type Client struct {
	credsPath   string
	allowWrites bool
	appVersion  string
	pdfDir      string
	dispatch    MCPDispatcher

	mu        sync.RWMutex
	state     RelayState
	userEmail string
	tenantID  string
	deviceID  string
	serverURL string
	lastError string
}

// NewClient creates a new 1erp.work relay client.
func NewClient(credsPath string, allowWrites bool, appVersion string, dispatch MCPDispatcher) *Client {
	return &Client{
		credsPath:   ResolveCredentialsPath(credsPath),
		allowWrites: allowWrites,
		appVersion:  appVersion,
		pdfDir:      filepath.Join(os.TempDir(), "tally-mcp-pdf"),
		dispatch:    dispatch,
		state:       StateDisconnected,
	}
}

// Status returns a thread-safe snapshot of the relay connection and auth state.
func (c *Client) Status() StatusSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return StatusSnapshot{
		State:       c.state,
		LoggedIn:    c.state != StateAuthRequired && c.deviceID != "",
		UserEmail:   c.userEmail,
		TenantID:    c.tenantID,
		DeviceID:    c.deviceID,
		ServerURL:   c.serverURL,
		AllowWrites: c.allowWrites,
		LastError:   c.lastError,
	}
}

func (c *Client) setState(state RelayState, creds *Credentials, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
	c.lastError = errMsg
	if creds != nil {
		c.userEmail = creds.UserEmail
		c.tenantID = creds.TenantID
		c.deviceID = creds.DeviceID
		c.serverURL = creds.ServerURL
	} else if state == StateAuthRequired {
		c.userEmail = ""
		c.tenantID = ""
		c.deviceID = ""
	}
}

// Run connects to the 1erp.work relay if logged in and maintains the tunnel until ctx is cancelled.
// If the user is not logged in, it does NOT connect and returns nil.
func (c *Client) Run(ctx context.Context) error {
	creds, err := LoadCredentials(c.credsPath)
	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) {
			c.setState(StateAuthRequired, nil, "")
			return nil
		}
		c.setState(StateAuthRequired, nil, err.Error())
		return err
	}

	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		if ctx.Err() != nil {
			c.setState(StateDisconnected, creds, "")
			return nil
		}

		// Reload & refresh token before each connect attempt
		creds, err = LoadCredentials(c.credsPath)
		if err != nil {
			c.setState(StateAuthRequired, nil, err.Error())
			return nil
		}
		creds, err = RefreshIfNeeded(ctx, creds, c.credsPath)
		if err != nil {
			c.setState(StateAuthRequired, creds, err.Error())
			log.Printf("[WARN] 1ERP Relay auth refresh failed: %v (run --1erp-login to re-authenticate)", err)
			return err
		}

		c.setState(StateConnecting, creds, "")
		connErr := c.connectAndServe(ctx, creds)
		if ctx.Err() != nil {
			c.setState(StateDisconnected, creds, "")
			return nil
		}
		if errors.Is(connErr, ErrWSUnauthorized) {
			c.setState(StateAuthRequired, creds, "1erp.work rejected credentials; please log in again")
			log.Printf("[WARN] 1ERP Relay rejected credentials (401/403). Stopping relay until --1erp-login.")
			return connErr
		}

		if connErr != nil {
			c.setState(StateDisconnected, creds, connErr.Error())
			log.Printf("[WARN] 1ERP Relay disconnected: %v. Reconnecting in %s...", connErr, backoff)
		}

		jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
		select {
		case <-time.After(backoff + jitter):
		case <-ctx.Done():
			c.setState(StateDisconnected, creds, "")
			return nil
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context, creds *Credentials) error {
	wsURL := creds.RelayWSURL
	if wsURL == "" {
		var err error
		wsURL, err = DeriveRelayWSURL(creds.ServerURL)
		if err != nil {
			return err
		}
	}
	if _, err := ValidateWSURL(wsURL); err != nil {
		return err
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+creds.AccessToken)
	headers.Set("X-Device-ID", creds.DeviceID)
	headers.Set("User-Agent", "TallyMCP-Relay/"+c.appVersion)

	ws, err := DialWS(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	defer ws.Close()

	// Send initial registration frame
	reg := RelayEnvelope{
		Type:       "register",
		DeviceID:   creds.DeviceID,
		DeviceName: creds.DeviceName,
		Version:    c.appVersion,
		ReadOnly:   !c.allowWrites,
	}
	regBytes, _ := json.Marshal(reg)
	if err := ws.WriteText(regBytes); err != nil {
		return fmt.Errorf("sending registration frame: %w", err)
	}

	c.setState(StateConnected, creds, "")
	log.Printf("[INFO] Connected to 1erp.work relay (%s, read_only=%v)", creds.UserEmail, !c.allowWrites)

	// Keepalive ping goroutine
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := ws.WritePing(); err != nil {
					return
				}
			case <-pingCtx.Done():
				return
			}
		}
	}()

	// Close socket on context cancellation so ReadMessage unblocks cleanly
	go func() {
		<-pingCtx.Done()
		_ = ws.Close()
	}()

	for {
		raw, err := ws.ReadMessage(ctx)
		if err != nil {
			return err
		}

		var env RelayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}

		switch env.Type {
		case "ping":
			pong, _ := json.Marshal(RelayEnvelope{Type: "pong", ID: env.ID})
			_ = ws.WriteText(pong)
		case "mcp_request":
			go c.handleMCPRequest(ctx, ws, env)
		case "file_request":
			go c.handleFileRequest(ws, env)
		}
	}
}

func (c *Client) handleMCPRequest(ctx context.Context, ws *WSConn, env RelayEnvelope) {
	if c.dispatch == nil {
		respBytes, _ := json.Marshal(RelayEnvelope{
			Type:  "mcp_response",
			ID:    env.ID,
			Error: "MCP dispatcher not configured",
		})
		_ = ws.WriteText(respBytes)
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	out := c.dispatch(reqCtx, env.Payload, c.allowWrites)
	respBytes, _ := json.Marshal(RelayEnvelope{
		Type:    "mcp_response",
		ID:      env.ID,
		Payload: json.RawMessage(out),
	})
	_ = ws.WriteText(respBytes)
}

func (c *Client) handleFileRequest(ws *WSConn, env RelayEnvelope) {
	data, err := ReadSafePDFFile(c.pdfDir, env.Filename)
	if err != nil {
		respBytes, _ := json.Marshal(RelayEnvelope{
			Type:  "file_response",
			ID:    env.ID,
			Error: err.Error(),
		})
		_ = ws.WriteText(respBytes)
		return
	}

	respBytes, _ := json.Marshal(RelayEnvelope{
		Type:        "file_response",
		ID:          env.ID,
		Filename:    filepath.Base(env.Filename),
		ContentType: "application/pdf",
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	})
	_ = ws.WriteText(respBytes)
}

// ReadSafePDFFile reads a generated PDF from pdfDir while strictly preventing path traversal.
func ReadSafePDFFile(pdfDir, filename string) ([]byte, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || strings.ContainsAny(filename, `/\`) || strings.Contains(filename, "..") {
		return nil, errors.New("invalid filename")
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		return nil, errors.New("only .pdf files are accessible via relay")
	}
	cleanBase := filepath.Base(filename)
	fullPath := filepath.Join(pdfDir, cleanBase)

	absDir, err := filepath.Abs(pdfDir)
	if err != nil {
		return nil, err
	}
	absFile, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absFile, absDir+string(os.PathSeparator)) {
		return nil, errors.New("access denied outside PDF directory")
	}

	info, err := os.Stat(absFile)
	if err != nil || info.IsDir() {
		return nil, errors.New("PDF file not found")
	}
	if info.Size() > 10*1024*1024 {
		return nil, errors.New("PDF file exceeds 10MB relay limit")
	}
	return os.ReadFile(absFile)
}
