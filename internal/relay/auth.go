package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const DefaultServerURL = "https://1erp.work"

// ErrNotLoggedIn is returned when no valid 1erp.work credentials exist locally.
var ErrNotLoggedIn = errors.New("not logged in to 1erp.work")

// Credentials holds the OAuth2 / device token state for connecting to 1erp.work.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	TenantID     string    `json:"tenant_id,omitempty"`
	UserEmail    string    `json:"user_email,omitempty"`
	DeviceID     string    `json:"device_id"`
	DeviceName   string    `json:"device_name"`
	RelayWSURL   string    `json:"relay_ws_url,omitempty"`
	ServerURL    string    `json:"server_url"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DefaultCredentialsPath returns the default secure path for 1erp.work credentials.
func DefaultCredentialsPath() string {
	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		return filepath.Join(cfgDir, "tally-mcp", "1erp-credentials.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".tally-mcp", "1erp-credentials.json")
	}
	return "1erp-credentials.json"
}

// ResolveCredentialsPath returns customPath if non-empty, otherwise DefaultCredentialsPath().
func ResolveCredentialsPath(customPath string) string {
	if strings.TrimSpace(customPath) != "" {
		return strings.TrimSpace(customPath)
	}
	return DefaultCredentialsPath()
}

// ValidateServerURL ensures the 1erp.work server URL uses HTTPS for any non-localhost host.
func ValidateServerURL(rawURL string) (string, error) {
	rawURL = strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if rawURL == "" {
		rawURL = DefaultServerURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(isLocal && u.Scheme == "http") {
		return "", fmt.Errorf("server URL must use https:// for remote hosts (got %q)", rawURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("server URL missing host: %q", rawURL)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// ValidateWSURL ensures the relay WebSocket URL uses WSS for any non-localhost host.
func ValidateWSURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid WebSocket URL: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "wss" && !(isLocal && u.Scheme == "ws") {
		return "", fmt.Errorf("relay WebSocket URL must use wss:// for remote hosts (got %q)", rawURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("relay WebSocket URL missing host: %q", rawURL)
	}
	return u.String(), nil
}

// DeriveRelayWSURL builds the default wss:// relay URL from an https:// server URL.
func DeriveRelayWSURL(serverURL string) (string, error) {
	clean, err := ValidateServerURL(serverURL)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(clean)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/tally-relay/ws"
	return u.String(), nil
}

// SaveCredentials writes credentials atomically with strict 0600 file permissions.
func SaveCredentials(path string, creds *Credentials) error {
	if creds == nil {
		return fmt.Errorf("credentials cannot be nil")
	}
	path = ResolveCredentialsPath(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating credentials directory: %w", err)
	}

	creds.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".1erp-creds-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp credentials file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting credentials permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp credentials file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("saving credentials file: %w", err)
	}
	_ = os.Chmod(path, 0600)
	return nil
}

// LoadCredentials reads credentials from disk. Returns ErrNotLoggedIn if missing or empty.
func LoadCredentials(path string) (*Credentials, error) {
	path = ResolveCredentialsPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotLoggedIn
		}
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	if strings.TrimSpace(creds.AccessToken) == "" {
		return nil, ErrNotLoggedIn
	}
	return &creds, nil
}

// DeleteCredentials removes local credentials from disk.
func DeleteCredentials(path string) error {
	path = ResolveCredentialsPath(path)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LoginWithBrowser performs an OAuth2 + PKCE (S256) login via the user's browser on 1erp.work.
func LoginWithBrowser(ctx context.Context, serverURL, credsPath string) (*Credentials, error) {
	cleanServer, err := ValidateServerURL(serverURL)
	if err != nil {
		return nil, err
	}

	state, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		return nil, err
	}
	challenge := pkceChallengeS256(verifier)

	deviceID := getOrCreateDeviceID(credsPath)
	deviceName, _ := os.Hostname()
	if deviceName == "" {
		deviceName = "Tally-PC"
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting local OAuth callback listener: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	type cbResult struct {
		code string
		err  error
	}
	resCh := make(chan cbResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body><h2>1ERP Login Failed</h2><p>%s: %s</p><p>You may close this tab.</p></body></html>",
				html.EscapeString(errParam), html.EscapeString(desc))
			select {
			case resCh <- cbResult{err: fmt.Errorf("1erp.work authorization denied: %s (%s)", errParam, desc)}:
			default:
			}
			return
		}

		gotState := q.Get("state")
		if subtle.ConstantTimeCompare([]byte(gotState), []byte(state)) != 1 {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			select {
			case resCh <- cbResult{err: errors.New("OAuth state mismatch (possible CSRF)")}:
			default:
			}
			return
		}

		code := strings.TrimSpace(q.Get("code"))
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			select {
			case resCh <- cbResult{err: errors.New("missing authorization code in callback")}:
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;text-align:center;padding:40px;"><h2>&#x2705; TallyMCP Connected to 1erp.work</h2><p>Login successful. You can close this browser tab and return to TallyMCP.</p></body></html>`)
		select {
		case resCh <- cbResult{code: code}:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := fmt.Sprintf("%s/tally-relay/authorize?client_id=tally-mcp&response_type=code&redirect_uri=%s&state=%s&code_challenge=%s&code_challenge_method=S256&device_id=%s&device_name=%s",
		cleanServer,
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
		url.QueryEscape(challenge),
		url.QueryEscape(deviceID),
		url.QueryEscape(deviceName),
	)

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("\nOpen this URL in your browser to log in to 1erp.work:\n%s\n\n", authURL)
	}

	var code string
	select {
	case res := <-resCh:
		if res.err != nil {
			return nil, res.err
		}
		code = res.code
	case <-ctx.Done():
		return nil, fmt.Errorf("login timed out or cancelled: %w", ctx.Err())
	}

	creds, err := exchangeAuthCode(ctx, cleanServer, code, verifier, redirectURI, deviceID, deviceName)
	if err != nil {
		return nil, err
	}
	if err := SaveCredentials(credsPath, creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// LoginWithToken verifies a 1erp.work personal/device token and saves credentials locally.
func LoginWithToken(ctx context.Context, serverURL, token, credsPath string) (*Credentials, error) {
	cleanServer, err := ValidateServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("token cannot be empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cleanServer+"/api/tally-relay/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "TallyMCP-Relay")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("verifying token with 1erp.work: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("1erp.work rejected token (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("1erp.work returned HTTP %d", resp.StatusCode)
	}

	var me struct {
		TenantID   string `json:"tenant_id"`
		UserEmail  string `json:"user_email"`
		RelayWSURL string `json:"relay_ws_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me); err != nil {
		return nil, fmt.Errorf("decoding 1erp.work response: %w", err)
	}

	deviceName, _ := os.Hostname()
	if deviceName == "" {
		deviceName = "Tally-PC"
	}
	wsURL := me.RelayWSURL
	if wsURL == "" {
		wsURL, _ = DeriveRelayWSURL(cleanServer)
	} else if _, err := ValidateWSURL(wsURL); err != nil {
		return nil, err
	}

	creds := &Credentials{
		AccessToken: token,
		TokenType:   "Bearer",
		TenantID:    me.TenantID,
		UserEmail:   me.UserEmail,
		DeviceID:    getOrCreateDeviceID(credsPath),
		DeviceName:  deviceName,
		RelayWSURL:  wsURL,
		ServerURL:   cleanServer,
	}
	if err := SaveCredentials(credsPath, creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// RefreshIfNeeded refreshes the access token if it expires within 5 minutes and a refresh token exists.
func RefreshIfNeeded(ctx context.Context, creds *Credentials, credsPath string) (*Credentials, error) {
	if creds == nil {
		return nil, ErrNotLoggedIn
	}
	if creds.ExpiresAt.IsZero() || time.Until(creds.ExpiresAt) > 5*time.Minute {
		return creds, nil
	}
	if creds.RefreshToken == "" {
		return creds, nil
	}

	cleanServer, err := ValidateServerURL(creds.ServerURL)
	if err != nil {
		return nil, err
	}

	payload := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     "tally-mcp",
		"refresh_token": creds.RefreshToken,
		"device_id":     creds.DeviceID,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cleanServer+"/api/tally-relay/token", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TallyMCP-Relay")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refreshing 1erp.work token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("1erp.work refresh token expired or revoked (HTTP %d); please log in again", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with HTTP %d", resp.StatusCode)
	}

	var tokResp tokenEndpointResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokResp); err != nil {
		return nil, fmt.Errorf("decoding refresh response: %w", err)
	}
	if tokResp.AccessToken == "" {
		return nil, errors.New("1erp.work returned empty access_token on refresh")
	}

	updated := *creds
	updated.AccessToken = tokResp.AccessToken
	if tokResp.RefreshToken != "" {
		updated.RefreshToken = tokResp.RefreshToken
	}
	if tokResp.ExpiresIn > 0 {
		updated.ExpiresAt = time.Now().UTC().Add(time.Duration(tokResp.ExpiresIn) * time.Second)
	}
	if tokResp.RelayWSURL != "" {
		if _, err := ValidateWSURL(tokResp.RelayWSURL); err == nil {
			updated.RelayWSURL = tokResp.RelayWSURL
		}
	}
	_ = SaveCredentials(credsPath, &updated)
	return &updated, nil
}

// Logout revokes the token on 1erp.work (best-effort) and deletes local credentials.
func Logout(ctx context.Context, credsPath string) error {
	creds, err := LoadCredentials(credsPath)
	if err == nil && creds != nil && creds.ServerURL != "" {
		if cleanServer, urlErr := ValidateServerURL(creds.ServerURL); urlErr == nil {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, cleanServer+"/api/tally-relay/revoke", nil)
			if reqErr == nil {
				req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
				req.Header.Set("User-Agent", "TallyMCP-Relay")
				client := &http.Client{Timeout: 5 * time.Second}
				if resp, doErr := client.Do(req); doErr == nil {
					_ = resp.Body.Close()
				}
			}
		}
	}
	return DeleteCredentials(credsPath)
}

type tokenEndpointResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	TenantID     string `json:"tenant_id"`
	UserEmail    string `json:"user_email"`
	RelayWSURL   string `json:"relay_ws_url"`
}

func exchangeAuthCode(ctx context.Context, serverURL, code, verifier, redirectURI, deviceID, deviceName string) (*Credentials, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "tally-mcp",
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
		"device_id":     deviceID,
		"device_name":   deviceName,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/tally-relay/token", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TallyMCP-Relay")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging authorization code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var tokResp tokenEndpointResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	if tokResp.AccessToken == "" {
		return nil, errors.New("1erp.work returned empty access_token")
	}

	wsURL := tokResp.RelayWSURL
	if wsURL == "" {
		wsURL, _ = DeriveRelayWSURL(serverURL)
	} else if _, err := ValidateWSURL(wsURL); err != nil {
		return nil, err
	}

	var expiresAt time.Time
	if tokResp.ExpiresIn > 0 {
		expiresAt = time.Now().UTC().Add(time.Duration(tokResp.ExpiresIn) * time.Second)
	}

	return &Credentials{
		AccessToken:  tokResp.AccessToken,
		RefreshToken: tokResp.RefreshToken,
		TokenType:    tokResp.TokenType,
		ExpiresAt:    expiresAt,
		TenantID:     tokResp.TenantID,
		UserEmail:    tokResp.UserEmail,
		DeviceID:     deviceID,
		DeviceName:   deviceName,
		RelayWSURL:   wsURL,
		ServerURL:    serverURL,
	}, nil
}

func getOrCreateDeviceID(credsPath string) string {
	if existing, err := LoadCredentials(credsPath); err == nil && existing.DeviceID != "" {
		return existing.DeviceID
	}
	id, err := randomHex(16)
	if err != nil {
		return fmt.Sprintf("dev-%d", time.Now().UnixNano())
	}
	return id
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomBase64URL(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func openBrowser(targetURL string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL).Start()
	case "darwin":
		return exec.Command("open", targetURL).Start()
	default:
		return exec.Command("xdg-open", targetURL).Start()
	}
}
