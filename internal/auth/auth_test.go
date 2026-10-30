package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tally-mcp/internal/auth"
	"tally-mcp/internal/config"
)

func TestAuthMiddleware(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Run("Auth Disabled", func(t *testing.T) {
		m := auth.NewMiddleware(config.AuthConfig{Enabled: false})
		handler := m.Wrap(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/sse", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("Auth Enabled - Missing Token", func(t *testing.T) {
		m := auth.NewMiddleware(config.AuthConfig{Enabled: true, APIKey: "test-secret"})
		handler := m.Wrap(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/sse", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Auth Enabled - Valid Bearer Token", func(t *testing.T) {
		m := auth.NewMiddleware(config.AuthConfig{Enabled: true, APIKey: "test-secret"})
		handler := m.Wrap(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/sse", nil)
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("Auth Enabled - Valid X-API-Key Header", func(t *testing.T) {
		m := auth.NewMiddleware(config.AuthConfig{Enabled: true, APIKey: "test-secret"})
		handler := m.Wrap(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/sse", nil)
		req.Header.Set("X-API-Key", "test-secret")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("Auth Enabled - Valid Query Token", func(t *testing.T) {
		m := auth.NewMiddleware(config.AuthConfig{Enabled: true, APIKey: "test-secret"})
		handler := m.Wrap(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/sse?token=test-secret", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("CORS Preflight OPTIONS", func(t *testing.T) {
		m := auth.NewMiddleware(config.AuthConfig{Enabled: true, APIKey: "test-secret"})
		handler := m.Wrap(dummyHandler)

		req := httptest.NewRequest(http.MethodOptions, "/sse", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for OPTIONS preflight, got %d", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("missing CORS header")
		}
	})
}
