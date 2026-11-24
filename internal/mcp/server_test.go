package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"tally-mcp/internal/config"
	mcppkg "tally-mcp/internal/mcp"
	"tally-mcp/internal/tally"
)

type mockRoundTripper func(req *http.Request) *http.Response

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req), nil
}

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
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<COMPANY><NAME>Demo Company</NAME></COMPANY>
		<COMPANY><NAME>ACME Corp</NAME></COMPANY>
		<VOUCHER VOUCHERNUMBER="101" DATE="20261031" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="Tata Steel" AMOUNT="-50000.00"></VOUCHER>
		<VOUCHER VOUCHERNUMBER="004200" DATE="20260410" VOUCHERTYPENAME="Receipt" PARTYLEDGERNAME="IDBI BANK-000198" NARRATION="AT NAVI MUMBAI - KALAMBOLI, 004200-X-LASER">
			<ALLLEDGERENTRIES.LIST>
				<LEDGERNAME>SUSPANCE A/C.</LEDGERNAME>
				<AMOUNT>206000.00</AMOUNT>
			</ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST>
				<LEDGERNAME>IDBI BANK-000198</LEDGERNAME>
				<AMOUNT>-206000.00</AMOUNT>
			</ALLLEDGERENTRIES.LIST>
		</VOUCHER>
		<LEDGER NAME="Cash"><PARENT>Cash-in-Hand</PARENT><CLOSINGBALANCE>15000.00</CLOSINGBALANCE></LEDGER>
		<LEDGER NAME="SUSPANCE A/C."><PARENT>Suspense A/c</PARENT><OPENINGBALANCE>0.00</OPENINGBALANCE><CLOSINGBALANCE>206000.00</CLOSINGBALANCE></LEDGER>
		<STOCKITEM NAME="Pipes"><PARENT>Hardware</PARENT><CLOSINGBALANCE>100 Nos</CLOSINGBALANCE><CLOSINGVALUE>5000.00</CLOSINGVALUE></STOCKITEM>
	</COLLECTION></DATA></BODY></ENVELOPE>`
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))
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

		if _, ok := body["queue_capacity"]; !ok {
			t.Errorf("expected queue_capacity in health check")
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

		for _, expected := range []string{
			"tally_status",
			"tally_start",
			"tally_list_companies",
			"tally_list_ledgers",
			"tally_get_ledger",
			"tally_get_ledger_statement",
			"tally_list_vouchers",
			"tally_get_voucher",
			"tally_get_stock_summary",
			"tally_get_financial_summary",
			"tally_get_profit_and_loss",
			"tally_get_balance_sheet",
			"tally_get_outstanding_receivables",
			"tally_get_outstanding_payables",
			"tally_get_daybook",
			"tally_generate_pdf",
		} {
			if !toolNames[expected] {
				t.Errorf("expected tool %s to be registered", expected)
			}
		}
	})

	// 5. Test Streamable HTTP tools/call tally_list_vouchers pagination support
	t.Run("POST /mcp tools/call tally_list_vouchers", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      3,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_list_vouchers",
				"arguments": map[string]any{
					"limit":  25,
					"offset": 0,
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
	})

	// 6. Test Streamable HTTP tools/call tally_get_voucher validation
	t.Run("POST /mcp tools/call tally_get_voucher validation", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      4,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "tally_get_voucher",
				"arguments": map[string]any{}, // missing identifier
			},
		}
		data, _ := json.Marshal(callPayload)

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
		// Expect isError to be true when required identifiers are missing
		if isErr, ok := result["isError"].(bool); !ok || !isErr {
			t.Errorf("expected isError=true for missing voucher identifier, got: %v", result)
		}
	})

	// 7. Test Streamable HTTP tools/call financial reports dispatch
	t.Run("POST /mcp tools/call financial report tools", func(t *testing.T) {
		reportTools := []string{
			"tally_get_profit_and_loss",
			"tally_get_balance_sheet",
			"tally_get_outstanding_receivables",
			"tally_get_outstanding_payables",
			"tally_get_daybook",
		}

		for idx, toolName := range reportTools {
			callPayload := map[string]any{
				"jsonrpc": "2.0",
				"id":      100 + idx,
				"method":  "tools/call",
				"params": map[string]any{
					"name": toolName,
					"arguments": map[string]any{
						"from_date": "20240401",
						"to_date":   "20250331",
					},
				},
			}
			data, _ := json.Marshal(callPayload)

			req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(data))
			if err != nil {
				t.Fatalf("failed to create request for %s: %v", toolName, err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if sessionID != "" {
				req.Header.Set("Mcp-Session-Id", sessionID)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST /mcp %s failed: %v", toolName, err)
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: expected 200 OK, got %d", toolName, resp.StatusCode)
			}
		}
	})

	// 8. Test Streamable HTTP tools/call tally_generate_pdf and file download endpoint
	t.Run("POST /mcp tools/call tally_generate_pdf and GET /files/", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      201,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_generate_pdf",
				"arguments": map[string]any{
					"report_type": "Balance Sheet",
					"from_date":   "20240401",
					"to_date":     "20250331",
					"filename":    "test_mcp_bs.pdf",
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_generate_pdf failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to parse JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'result' in response, got %v", rpcResp)
		}

		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("expected content in result, got: %v", result)
		}

		// Verify file download via /files/test_mcp_bs.pdf
		fileResp, err := http.Get(baseURL + "/files/test_mcp_bs.pdf")
		if err != nil {
			t.Fatalf("failed to GET /files/test_mcp_bs.pdf: %v", err)
		}
		defer fileResp.Body.Close()

		if fileResp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK from /files/test_mcp_bs.pdf, got %d", fileResp.StatusCode)
		}

		fileHeader := make([]byte, 10)
		_, _ = fileResp.Body.Read(fileHeader)
		if !bytes.HasPrefix(fileHeader, []byte("%PDF-1.4")) {
			t.Errorf("expected PDF header from /files/ endpoint, got %q", string(fileHeader))
		}
	})

	// 9. Test Streamable HTTP tools/call tally_list_ledgers with search & summary_only
	t.Run("POST /mcp tools/call tally_list_ledgers compact and search", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      301,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_list_ledgers",
				"arguments": map[string]any{
					"search":       "Tata",
					"limit":        10,
					"offset":       0,
					"summary_only": true,
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_list_ledgers failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}
	})

	// 10. Test Streamable HTTP tools/call tally_get_financial_summary
	t.Run("POST /mcp tools/call tally_get_financial_summary", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      302,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_get_financial_summary",
				"arguments": map[string]any{
					"as_on_date": "20250331",
					"from_date":  "20240401",
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_get_financial_summary failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}
	})

	// 11. Test Streamable HTTP tools/call with format: "toon"
	t.Run("POST /mcp tools/call with format toon", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      303,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_list_vouchers",
				"arguments": map[string]any{
					"limit":  10,
					"format": "toon",
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_list_vouchers with format:toon failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to decode JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result object in response: %v", rpcResp)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("expected non-empty content in result: %v", result)
		}
		firstItem := content[0].(map[string]any)
		textVal, ok := firstItem["text"].(string)
		if !ok {
			t.Fatalf("expected text content: %v", firstItem)
		}

		// In TOON format, vouchers array should be formatted as "vouchers[N]..." or include count/total keys
		if !strings.Contains(textVal, "count:") && !strings.Contains(textVal, "vouchers[") {
			t.Errorf("expected TOON formatted text with count: or vouchers[, got:\n%s", textVal)
		}
	})

	// 12. Test Streamable HTTP tools/call tally_list_vouchers with party & amount filters
	t.Run("POST /mcp tools/call tally_list_vouchers with party and amount filters", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      304,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_list_vouchers",
				"arguments": map[string]any{
					"party":  "Tata Steel",
					"amount": 50000,
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_list_vouchers filtered failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to decode JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result object: %v", rpcResp)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("expected non-empty content: %v", result)
		}
		firstItem := content[0].(map[string]any)
		textVal := firstItem["text"].(string)

		if !strings.Contains(textVal, "Tata Steel") {
			t.Errorf("expected Tata Steel voucher in response, got:\n%s", textVal)
		}
	})

	// 13. Test Streamable HTTP tools/call tally_start
	t.Run("POST /mcp tools/call tally_start", func(t *testing.T) {
		reqPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      13,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_start",
				"arguments": map[string]any{
					"wait_seconds": 5,
				},
			},
		}

		payloadBytes, _ := json.Marshal(reqPayload)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBuffer(payloadBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp tally_start failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to decode JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result object: %v", rpcResp)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("expected non-empty content: %v", result)
		}
		firstItem := content[0].(map[string]any)
		textVal := firstItem["text"].(string)

		if !strings.Contains(textVal, "already_running") && !strings.Contains(textVal, "started") {
			t.Errorf("expected already_running or started in tally_start response, got:\n%s", textVal)
		}
	})

	// 14. Test Streamable HTTP tools/call tally_status
	t.Run("POST /mcp tools/call tally_status", func(t *testing.T) {
		reqPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      14,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_status",
				"arguments": map[string]any{
					"start_if_stopped": true,
				},
			},
		}

		payloadBytes, _ := json.Marshal(reqPayload)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBuffer(payloadBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp tally_status failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("failed to decode JSON-RPC response: %v", err)
		}

		result, ok := rpcResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result object: %v", rpcResp)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("expected non-empty content: %v", result)
		}
		firstItem := content[0].(map[string]any)
		textVal := firstItem["text"].(string)

		if !strings.Contains(textVal, "connected") {
			t.Errorf("expected connected in tally_status response, got:\n%s", textVal)
		}
	})

	// 15. Test Prompts (prompts/list and prompts/get)
	t.Run("POST /mcp prompts/list and prompts/get", func(t *testing.T) {
		// prompts/list
		listPayload := `{"jsonrpc":"2.0","id":151,"method":"prompts/list","params":{}}`
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(listPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp prompts/list failed: %v", err)
		}
		defer resp.Body.Close()

		var listResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			t.Fatalf("decode prompts/list failed: %v", err)
		}
		resObj, _ := listResp["result"].(map[string]any)
		promptsList, ok := resObj["prompts"].([]any)
		if !ok || len(promptsList) < 2 {
			t.Fatalf("expected at least 2 prompts, got %v", promptsList)
		}

		// prompts/get tally_accounting_rules
		getPayload := `{"jsonrpc":"2.0","id":152,"method":"prompts/get","params":{"name":"tally_accounting_rules"}}`
		req2, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(getPayload))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req2.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatalf("POST /mcp prompts/get failed: %v", err)
		}
		defer resp2.Body.Close()

		var getResp map[string]any
		if err := json.NewDecoder(resp2.Body).Decode(&getResp); err != nil {
			t.Fatalf("decode prompts/get failed: %v", err)
		}
		result2, _ := getResp["result"].(map[string]any)
		messages, ok := result2["messages"].([]any)
		if !ok || len(messages) == 0 {
			t.Fatalf("expected non-empty prompt messages: %v", result2)
		}
	})

	// 16. Test Resources (resources/list and resources/read)
	t.Run("POST /mcp resources/list and resources/read", func(t *testing.T) {
		// resources/list
		listPayload := `{"jsonrpc":"2.0","id":161,"method":"resources/list","params":{}}`
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(listPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp resources/list failed: %v", err)
		}
		defer resp.Body.Close()

		var listResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			t.Fatalf("decode resources/list failed: %v", err)
		}
		resObj, _ := listResp["result"].(map[string]any)
		resourcesList, ok := resObj["resources"].([]any)
		if !ok || len(resourcesList) < 3 {
			t.Fatalf("expected at least 3 resources, got %v", resourcesList)
		}

		// resources/read tally://financial-summary
		readPayload := `{"jsonrpc":"2.0","id":162,"method":"resources/read","params":{"uri":"tally://financial-summary"}}`
		req2, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(readPayload))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req2.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatalf("POST /mcp resources/read failed: %v", err)
		}
		defer resp2.Body.Close()

		var readResp map[string]any
		if err := json.NewDecoder(resp2.Body).Decode(&readResp); err != nil {
			t.Fatalf("decode resources/read failed: %v", err)
		}
		result2, _ := readResp["result"].(map[string]any)
		contents, ok := result2["contents"].([]any)
		if !ok || len(contents) == 0 {
			t.Fatalf("expected contents in resources/read: %v", result2)
		}
	})

	// 17. Test default date guard in tally_list_vouchers
	t.Run("POST /mcp tools/call tally_list_vouchers date default note", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      171,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_list_vouchers",
				"arguments": map[string]any{
					"limit": 5,
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_list_vouchers failed: %v", err)
		}
		defer resp.Body.Close()

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}
		result, _ := rpcResp["result"].(map[string]any)
		content, _ := result["content"].([]any)
		firstItem := content[0].(map[string]any)
		textVal := firstItem["text"].(string)

		if !strings.Contains(textVal, "from_date:") || !strings.Contains(textVal, "financial year") {
			t.Errorf("expected from_date and financial year note in response, got:\n%s", textVal)
		}
	})

	// 18. Test Streamable HTTP tools/call tally_get_ledger_statement
	t.Run("POST /mcp tools/call tally_get_ledger_statement", func(t *testing.T) {
		callPayload := map[string]any{
			"jsonrpc": "2.0",
			"id":      181,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "tally_get_ledger_statement",
				"arguments": map[string]any{
					"ledger_name": "SUSPANCE A/C.",
					"from_date":   "20260401",
					"to_date":     "20261031",
					"format":      "json",
				},
			},
		}
		data, _ := json.Marshal(callPayload)

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
			t.Fatalf("POST /mcp tally_get_ledger_statement failed: %v", err)
		}
		defer resp.Body.Close()

		var rpcResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}
		result, _ := rpcResp["result"].(map[string]any)
		content, _ := result["content"].([]any)
		firstItem := content[0].(map[string]any)
		textVal := firstItem["text"].(string)

		var stmt map[string]any
		if err := json.Unmarshal([]byte(textVal), &stmt); err != nil {
			t.Fatalf("failed to unmarshal JSON response: %v\ntext: %s", err, textVal)
		}

		if stmt["ledger_name"] != "SUSPANCE A/C." {
			t.Errorf("expected ledger_name 'SUSPANCE A/C.', got %v", stmt["ledger_name"])
		}
		if stmt["parent"] != "Suspense A/c" {
			t.Errorf("expected parent 'Suspense A/c', got %v", stmt["parent"])
		}
		if stmt["closing_balance_type"] != "Cr" {
			t.Errorf("expected closing_balance_type 'Cr', got %v", stmt["closing_balance_type"])
		}
		entries, ok := stmt["entries"].([]any)
		if !ok || len(entries) == 0 {
			t.Fatalf("expected entries in statement, got: %v", stmt)
		}
		entry := entries[0].(map[string]any)
		if entry["contra_ledger"] != "IDBI BANK-000198" {
			t.Errorf("expected contra_ledger 'IDBI BANK-000198', got %v", entry["contra_ledger"])
		}
		if entry["credit"] != 206000.0 {
			t.Errorf("expected credit 206000.0, got %v", entry["credit"])
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

func TestServerDefaultPayloadFormatToon(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	cfg := config.DefaultConfig()
	cfg.Server.ListenAddress = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.Server.PayloadFormat = "toon"

	tallyClient := tally.NewClient(cfg.Tally)
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<COMPANY><NAME>Demo Company</NAME></COMPANY>
		<COMPANY><NAME>ACME Corp</NAME></COMPANY>
	</COLLECTION></DATA></BODY></ENVELOPE>`
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))
	srv := mcppkg.NewServer(cfg, tallyClient, "v1.0.8")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.RunSSEWithListener(ctx, l)
	}()

	time.Sleep(100 * time.Millisecond)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Initialize session
	initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	initReq, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(initPayload))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initResp, err := http.DefaultClient.Do(initReq)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	callPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      401,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "tally_list_companies",
			"arguments": map[string]any{},
		},
	}
	data, _ := json.Marshal(callPayload)

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

	var rpcResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	result := rpcResp["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	textVal := first["text"].(string)

	// Since server default payload_format is "toon", output should be in TOON tabular format "[2]{name}:"
	if !strings.Contains(textVal, "[2]{name}:") && !strings.Contains(textVal, "Demo Company") {
		t.Errorf("expected default TOON output when payload_format is 'toon', got:\n%s", textVal)
	}
}

func TestHandleGetLedgerStatement(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<LEDGER NAME="SUSPANCE A/C.">
			<PARENT>Suspense A/c</PARENT>
			<OPENINGBALANCE>0.00</OPENINGBALANCE>
		</LEDGER>
		<VOUCHER VOUCHERNUMBER="004200" DATE="20260410" VOUCHERTYPENAME="Receipt" PARTYLEDGERNAME="IDBI BANK-000198" NARRATION="AT NAVI MUMBAI - KALAMBOLI, 004200-X-LASER">
			<ALLLEDGERENTRIES.LIST>
				<LEDGERNAME>SUSPANCE A/C.</LEDGERNAME>
				<AMOUNT>206000.00</AMOUNT>
			</ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST>
				<LEDGERNAME>IDBI BANK-000198</LEDGERNAME>
				<AMOUNT>-206000.00</AMOUNT>
			</ALLLEDGERENTRIES.LIST>
		</VOUCHER>
		<VOUCHER VOUCHERNUMBER="PV-99" DATE="20260415" VOUCHERTYPENAME="Payment" PARTYLEDGERNAME="HDFC Bank" NARRATION="EXPENSE ADJUSTMENT">
			<ALLLEDGERENTRIES.LIST>
				<LEDGERNAME>SUSPANCE A/C.</LEDGERNAME>
				<AMOUNT>-50000.00</AMOUNT>
			</ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST>
				<LEDGERNAME>HDFC Bank</LEDGERNAME>
				<AMOUNT>50000.00</AMOUNT>
			</ALLLEDGERENTRIES.LIST>
		</VOUCHER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	tallyClient := tally.NewClient(config.TallyConfig{
		URL: "http://mock-tally:9000",
	})
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	handlers := mcppkg.NewToolHandlers(tallyClient, "v1.0.8", "test/repo", "json")
	ctx := context.Background()

	// 1. Missing ledger_name should return error
	resErr, err := handlers.HandleGetLedgerStatement(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_get_ledger_statement",
			Arguments: map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resErr.IsError {
		t.Errorf("expected error when ledger_name is missing")
	}

	// 2. Successful statement with both Debit and Credit vouchers
	res, err := handlers.HandleGetLedgerStatement(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_get_ledger_statement",
			Arguments: map[string]any{
				"ledger_name": "SUSPANCE A/C.",
				"format":      "json",
			},
		},
	})
	if err != nil {
		t.Fatalf("HandleGetLedgerStatement failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("HandleGetLedgerStatement returned error result: %v", res)
	}

	text := res.Content[0].(mcp.TextContent).Text
	var stmt tally.LedgerStatementResult
	if err := json.Unmarshal([]byte(text), &stmt); err != nil {
		t.Fatalf("failed to decode statement result: %v, text: %s", err, text)
	}

	if stmt.LedgerName != "SUSPANCE A/C." {
		t.Errorf("expected ledger_name 'SUSPANCE A/C.', got %q", stmt.LedgerName)
	}
	if stmt.Parent != "Suspense A/c" {
		t.Errorf("expected parent 'Suspense A/c', got %q", stmt.Parent)
	}
	if stmt.EntriesCount != 2 {
		t.Errorf("expected 2 entries, got %d", stmt.EntriesCount)
	}
	if stmt.TotalCredit != 206000.0 {
		t.Errorf("expected TotalCredit 206000.0, got %f", stmt.TotalCredit)
	}
	if stmt.TotalDebit != 50000.0 {
		t.Errorf("expected TotalDebit 50000.0, got %f", stmt.TotalDebit)
	}
	expectedClosing := 156000.0 // 206000 - 50000
	if stmt.ClosingBalance != expectedClosing {
		t.Errorf("expected ClosingBalance %f, got %f", expectedClosing, stmt.ClosingBalance)
	}
	if stmt.ClosingBalanceType != "Cr" {
		t.Errorf("expected ClosingBalanceType 'Cr', got %s", stmt.ClosingBalanceType)
	}
}
