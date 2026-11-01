package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"tally-mcp/internal/config"
	mcppkg "tally-mcp/internal/mcp"
	"tally-mcp/internal/tally"
)

func TestStreamableHTTPEndpoint(t *testing.T) {
	// Find a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	cfg := config.DefaultConfig()
	cfg.Server.ListenAddress = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.Auth.Enabled = false

	tallyClient := tally.NewClient(cfg.Tally)
	srv := mcppkg.NewServer(cfg, tallyClient, "v1.0.8")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- srv.RunSSEWithListener(ctx, l)
	}()

	// Wait briefly for server to spin up
	time.Sleep(100 * time.Millisecond)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// 1. Test /health endpoint
	t.Run("Health Check", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/health")
		if err != nil {
			t.Fatalf("failed to GET /health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode health JSON: %v", err)
		}

		if body["status"] != "ok" {
			t.Errorf("expected status 'ok', got %v", body["status"])
		}

		endpoints, ok := body["endpoints"].(map[string]any)
		if !ok || endpoints["streamable_http"] != "/mcp" || endpoints["sse"] != "/sse" {
			t.Errorf("expected endpoints to include /mcp and /sse, got: %v", body["endpoints"])
		}
	})

	// 2. Test CORS OPTIONS on /mcp
	t.Run("CORS OPTIONS /mcp", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, baseURL+"/mcp", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed OPTIONS /mcp: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK for OPTIONS, got %d", resp.StatusCode)
		}

		if allowMethods := resp.Header.Get("Access-Control-Allow-Methods"); allowMethods == "" {
			t.Errorf("missing Access-Control-Allow-Methods")
		}
		if allowHeaders := resp.Header.Get("Access-Control-Allow-Headers"); allowHeaders == "" {
			t.Errorf("missing Access-Control-Allow-Headers")
		}
	})

	// 3. Test Streamable HTTP POST /mcp Initialize
	var sessionID string
	t.Run("POST /mcp Initialize", func(t *testing.T) {
		initPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo": map[string]any{
					"name":    "test-client",
					"version": "1.0",
				},
			},
		}
		data, _ := json.Marshal(initPayload)

		req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}

		sessionID = resp.Header.Get("Mcp-Session-Id")

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to parse JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'result' in response, got %v", rpcResp)
		}

		serverInfo, ok := result["serverInfo"].(map[string]any)
		if !ok || serverInfo["name"] != "tally-mcp" {
			t.Errorf("expected serverInfo.name='tally-mcp', got %v", serverInfo)
		}
	})

	// 4. Test Streamable HTTP POST /mcp tools/list
	t.Run("POST /mcp tools/list", func(t *testing.T) {
		listPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/list",
			"params":  map[string]any{},
		}
		data, _ := json.Marshal(listPayload)

		req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to parse JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'result' in response, got %v", rpcResp)
		}

		tools, ok := result["tools"].([]any)
		if !ok || len(tools) == 0 {
			t.Errorf("expected non-empty tools list, got %v", result)
		}

		// Verify expected tools are present
		toolNames := make(map[string]bool)
		for _, tool := range tools {
			if tm, ok := tool.(map[string]any); ok {
				if name, ok := tm["name"].(string); ok {
					toolNames[name] = true
				}
			}
		}

		for _, expected := range []string{"tally_status", "tally_list_companies", "tally_list_ledgers"} {
			if !toolNames[expected] {
				t.Errorf("expected tool %s to be registered", expected)
			}
		}
	})

	// Clean shutdown
	cancel()
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
	}
}

func TestStreamableHTTPWithAuth(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	cfg := config.DefaultConfig()
	cfg.Server.ListenAddress = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.Auth.Enabled = true
	cfg.Auth.APIKey = "super-secret-key"

	tallyClient := tally.NewClient(cfg.Tally)
	srv := mcppkg.NewServer(cfg, tallyClient, "v1.0.8")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.RunSSEWithListener(ctx, l)
	}()

	time.Sleep(100 * time.Millisecond)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	payload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`

	// 1. Missing auth token -> 401
	req1, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(payload))
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", resp1.StatusCode)
	}

	// 2. Valid Bearer token -> 200 OK
	req2, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer super-secret-key")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK with valid bearer token, got %d", resp2.StatusCode)
	}

	// 3. Valid Query parameter -> 200 OK
	req3, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp?token=super-secret-key", bytes.NewBufferString(payload))
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK with valid query token, got %d", resp3.StatusCode)
	}
}
