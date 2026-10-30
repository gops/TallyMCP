package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"tally-mcp/internal/config"
)

// Middleware provides authentication and CORS wrapping for HTTP handlers.
type Middleware struct {
	cfg config.AuthConfig
}

// NewMiddleware creates a new auth middleware handler.
func NewMiddleware(cfg config.AuthConfig) *Middleware {
	return &Middleware{cfg: cfg}
}

// Wrap wraps an existing http.Handler with CORS and authentication verification.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers so web-based MCP clients can connect across LAN
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")

		// Handle preflight CORS OPTIONS requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// If auth is not enabled, allow request directly
		if !m.cfg.Enabled || m.cfg.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Extract token from request
		token := m.extractToken(r)

		// Secure constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(token), []byte(m.cfg.APIKey)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "Unauthorized",
				"message": "Valid API key or Bearer token is required.",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractToken looks for token in Authorization header, X-API-Key header, or query parameters.
func (m *Middleware) extractToken(r *http.Request) string {
	// 1. Authorization: Bearer <token>
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
		// In case user passed raw token in Authorization header
		return strings.TrimSpace(authHeader)
	}

	// 2. X-API-Key: <token>
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		return strings.TrimSpace(apiKey)
	}

	// 3. Query param: ?token=<token> or ?api_key=<token>
	if qToken := r.URL.Query().Get("token"); qToken != "" {
		return strings.TrimSpace(qToken)
	}
	if qKey := r.URL.Query().Get("api_key"); qKey != "" {
		return strings.TrimSpace(qKey)
	}

	return ""
}
