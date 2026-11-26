package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"tally-mcp/internal/config"
	mcppkg "tally-mcp/internal/mcp"
	"tally-mcp/internal/tally"
)

func TestHandlerSelfCorrectionAndEnhancements(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<COMPANY NAME="Acme Corp"></COMPANY>
		<LEDGER NAME="G.R. Metalloys Pvt Ltd">
			<PARENT>Sundry Debtors</PARENT>
			<OPENINGBALANCE>-1000.00</OPENINGBALANCE>
			<CLOSINGBALANCE>-5000.00</CLOSINGBALANCE>
		</LEDGER>
		<LEDGER NAME="HDFC BANK A/C-322">
			<PARENT>Bank Accounts</PARENT>
		</LEDGER>
		<LEDGER NAME="HDFC Credit Card">
			<PARENT>Current Liabilities</PARENT>
		</LEDGER>
		<VOUCHER VOUCHERNUMBER="INV-1" DATE="20260410" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="G.R. Metalloys Pvt Ltd">
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>G.R. Metalloys Pvt Ltd</LEDGERNAME><AMOUNT>-4000.00</AMOUNT></ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>Sales</LEDGERNAME><AMOUNT>4000.00</AMOUNT></ALLLEDGERENTRIES.LIST>
		</VOUCHER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	var tallyCalls atomic.Int32
	tallyClient := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 30})
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		tallyCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	handlers := mcppkg.NewToolHandlers(tallyClient, "v1.0.8", "gops/TallyMCP", "json")
	ctx := context.Background()

	// 1. Auto-correct ledger punctuation in HandleGetLedgerStatement + running balance + OpeningBalanceType (full mode)
	res, err := handlers.HandleGetLedgerStatement(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_get_ledger_statement",
			Arguments: map[string]any{
				"ledger_name":  "GR Metalloys Pvt Ltd",
				"summary_only": false,
				"format":       "json",
			},
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("expected auto-corrected ledger statement to succeed, got err=%v res=%+v", err, res)
	}
	var stmt tally.LedgerStatementResult
	if err := json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &stmt); err != nil {
		t.Fatal(err)
	}
	if stmt.LedgerName != "G.R. Metalloys Pvt Ltd" || stmt.ResolvedFrom != "GR Metalloys Pvt Ltd" {
		t.Errorf("expected resolved ledger name and resolved_from, got %+v", stmt)
	}
	if stmt.OpeningBalance != 1000 || stmt.OpeningBalanceType != "Dr" {
		t.Errorf("expected OpeningBalance=1000 Dr, got %.2f %s", stmt.OpeningBalance, stmt.OpeningBalanceType)
	}
	if len(stmt.Entries) != 1 || stmt.Entries[0].Balance != 5000 || stmt.Entries[0].BalanceType != "Dr" {
		t.Errorf("expected running balance 5000 Dr, got %+v", stmt.Entries)
	}

	// 2. Ambiguous ledger returns did-you-mean candidates
	errRes, err := handlers.HandleGetLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_get_ledger",
			Arguments: map[string]any{"name": "HDFC"},
		},
	})
	if err != nil || !errRes.IsError {
		t.Fatalf("expected error result for ambiguous HDFC, got %+v", errRes)
	}
	errText := errRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(errText, "HDFC BANK A/C-322") || !strings.Contains(errText, "HDFC Credit Card") {
		t.Errorf("expected did-you-mean candidates in error, got: %s", errText)
	}

	// 3. HandleGeneratePDF omits base64_pdf and binary blob by default to save context tokens
	pdfRes, err := handlers.HandleGeneratePDF(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_generate_pdf",
			Arguments: map[string]any{
				"report_type": "Balance Sheet",
				"filename":    "compact_test.pdf",
			},
		},
	})
	if err != nil || pdfRes.IsError {
		t.Fatalf("HandleGeneratePDF failed: err=%v res=%+v", err, pdfRes)
	}
	var sawText, sawBlob bool
	for _, c := range pdfRes.Content {
		switch item := c.(type) {
		case mcp.TextContent:
			sawText = true
			if strings.Contains(item.Text, "base64_pdf") {
				t.Errorf("expected base64_pdf omitted from text payload by default, got: %s", item.Text)
			}
		case mcp.EmbeddedResource:
			sawBlob = true
		}
	}
	if !sawText || sawBlob {
		t.Errorf("expected TextContent only by default (no EmbeddedResource blob), got sawText=%v sawBlob=%v", sawText, sawBlob)
	}

	// 3b. HandleGeneratePDF attaches EmbeddedResource when include_blob=true
	pdfBlobRes, err := handlers.HandleGeneratePDF(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_generate_pdf",
			Arguments: map[string]any{
				"report_type":  "Balance Sheet",
				"filename":     "compact_test_blob.pdf",
				"include_blob": true,
			},
		},
	})
	if err != nil || pdfBlobRes.IsError {
		t.Fatalf("HandleGeneratePDF with include_blob failed: err=%v res=%+v", err, pdfBlobRes)
	}
	sawText, sawBlob = false, false
	for _, c := range pdfBlobRes.Content {
		switch item := c.(type) {
		case mcp.TextContent:
			sawText = true
		case mcp.EmbeddedResource:
			sawBlob = true
			blobRes := item.Resource.(mcp.BlobResourceContents)
			if blobRes.Blob == "" {
				t.Errorf("expected BlobResourceContents.Blob to contain PDF base64")
			}
		}
	}
	if !sawText || !sawBlob {
		t.Errorf("expected both TextContent and EmbeddedResource when include_blob=true, got sawText=%v sawBlob=%v", sawText, sawBlob)
	}

	// 4. HandleCreateLedger catches <ERRORS>1</ERRORS> properly
	errClient := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000"})
	errClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<RESPONSE><CREATED>0</CREATED><ERRORS>1</ERRORS></RESPONSE>`)),
			Header:     make(http.Header),
		}
	}))
	errHandlers := mcppkg.NewToolHandlers(errClient, "v1.0.8", "gops/TallyMCP", "json")
	createErrRes, err := errHandlers.HandleCreateLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_create_ledger",
			Arguments: map[string]any{
				"name":   "Test",
				"parent": "customers",
			},
		},
	})
	if err != nil || !createErrRes.IsError {
		t.Errorf("expected error when Tally returns <ERRORS>1</ERRORS>, got %+v", createErrRes)
	}
}

func TestFilesEndpointAuthAndToolAnnotations(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	cfg := config.DefaultConfig()
	cfg.Server.ListenAddress = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.Auth.Enabled = true
	cfg.Auth.APIKey = "secret-123"

	tallyClient := tally.NewClient(cfg.Tally)
	srv := mcppkg.NewServer(cfg, tallyClient, "v1.0.8")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = srv.RunSSEWithListener(ctx, l)
	}()
	time.Sleep(100 * time.Millisecond)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Unauthenticated /files/ request must be rejected with 401
	resp, err := http.Get(baseURL + "/files/nonexistent.pdf")
	if err != nil {
		t.Fatalf("GET /files failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized on /files/ without token, got %d", resp.StatusCode)
	}

	// Initialize session first
	initReq, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initReq.Header.Set("Authorization", "Bearer secret-123")
	initResp, err := http.DefaultClient.Do(initReq)
	if err != nil {
		t.Fatalf("POST /mcp initialize failed: %v", err)
	}
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	// Verify ReadOnlyHint annotation on tools/list
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer secret-123")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	listResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp tools/list failed: %v", err)
	}
	defer listResp.Body.Close()

	var rpcResp map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	tools := rpcResp["result"].(map[string]any)["tools"].([]any)
	var checked bool
	for _, item := range tools {
		tm := item.(map[string]any)
		if tm["name"] == "tally_list_ledgers" {
			ann, ok := tm["annotations"].(map[string]any)
			if !ok || ann["readOnlyHint"] != true {
				t.Errorf("expected readOnlyHint=true on tally_list_ledgers, got %+v", tm["annotations"])
			}
			checked = true
		}
	}
	if !checked {
		t.Errorf("tally_list_ledgers not found in tools/list")
	}
}

func TestTokenEfficiencyFeatures(t *testing.T) {
	longNarr := strings.Repeat("NEFT-UTR1234567890-", 12)
	mockXML := fmt.Sprintf(`<ENVELOPE><HEADER><VERSION>1</VERSION></HEADER><BODY><DATA><COLLECTION>
		<LEDGER NAME="G.R. Metalloys &amp; Co">
			<PARENT>Sundry Debtors</PARENT>
			<OPENINGBALANCE>-1000.00</OPENINGBALANCE>
			<CLOSINGBALANCE>-5000.00</CLOSINGBALANCE>
			<EMAIL>accounts@gr.example</EMAIL>
			<ADDRESS.LIST><ADDRESS>Plot 42</ADDRESS><ADDRESS>Mumbai</ADDRESS></ADDRESS.LIST>
		</LEDGER>
		<VOUCHER VOUCHERNUMBER="INV-1" DATE="20260410" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="G.R. Metalloys &amp; Co" AMOUNT="-4000.00" NARRATION="%s" GUID="12345678-1234-1234-1234-123456789abc">
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>G.R. Metalloys &amp; Co</LEDGERNAME><ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE><AMOUNT>-4000.00</AMOUNT></ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>CGST</LEDGERNAME><ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE><AMOUNT>4000.00</AMOUNT></ALLLEDGERENTRIES.LIST>
		</VOUCHER>
	</COLLECTION></DATA></BODY></ENVELOPE>`, longNarr)

	tallyClient := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 30})
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	handlers := mcppkg.NewToolHandlers(tallyClient, "v1.0.8", "gops/TallyMCP", "toon")
	ctx := context.Background()

	// 1. tally_get_ledger returns flat LedgerDetail without ENVELOPE wrapper
	glRes, err := handlers.HandleGetLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_get_ledger", Arguments: map[string]any{"name": "G.R. Metalloys & Co"}},
	})
	if err != nil || glRes.IsError {
		t.Fatalf("HandleGetLedger failed: err=%v res=%+v", err, glRes)
	}
	glText := glRes.Content[0].(mcp.TextContent).Text
	if strings.Contains(glText, "ENVELOPE") || !strings.Contains(glText, "Plot 42") {
		t.Errorf("expected flat LedgerDetail without ENVELOPE, got:\n%s", glText)
	}

	// 2. tally_list_ledgers defaults to summary_only=true, limit=25, and supports fields projection
	llRes, err := handlers.HandleListLedgers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_list_ledgers", Arguments: map[string]any{"fields": "name,balance"}},
	})
	if err != nil || llRes.IsError {
		t.Fatalf("HandleListLedgers failed: err=%v res=%+v", err, llRes)
	}
	llText := llRes.Content[0].(mcp.TextContent).Text
	if strings.Contains(llText, "email") || !strings.Contains(llText, "{balance,name}:") {
		t.Errorf("expected projected {balance,name} columns without email, got:\n%s", llText)
	}

	// 3. tally_list_vouchers aggregate_only + group_by
	aggRes, err := handlers.HandleListVouchers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_list_vouchers",
			Arguments: map[string]any{"aggregate_only": true, "group_by": "voucher_type", "from_date": "20260401"},
		},
	})
	if err != nil || aggRes.IsError {
		t.Fatalf("HandleListVouchers aggregate_only failed: err=%v res=%+v", err, aggRes)
	}
	aggText := aggRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(aggText, "total_count: 1") || !strings.Contains(aggText, "total_amount: 4000") {
		t.Errorf("expected aggregate totals in TOON, got:\n%s", aggText)
	}

	// 4. tally_list_vouchers with include_entries=true stays 2D tabular in TOON and truncates narration
	lvRes, err := handlers.HandleListVouchers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_list_vouchers",
			Arguments: map[string]any{"include_entries": true, "from_date": "20260401"},
		},
	})
	if err != nil || lvRes.IsError {
		t.Fatalf("HandleListVouchers include_entries failed: err=%v res=%+v", err, lvRes)
	}
	lvText := lvRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(lvText, "vouchers[1]{") || !strings.Contains(lvText, "entries_summary") {
		t.Errorf("expected 2D tabular vouchers with entries_summary, got:\n%s", lvText)
	}
	if !strings.Contains(lvText, "…") {
		t.Errorf("expected truncated narration with ellipsis, got:\n%s", lvText)
	}

	// 5. tally_get_voucher omits guid, is_deemed_positive, and taxes by default
	gvRes, err := handlers.HandleGetVoucher(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_get_voucher", Arguments: map[string]any{"voucher_no": "INV-1"}},
	})
	if err != nil || gvRes.IsError {
		t.Fatalf("HandleGetVoucher failed: err=%v res=%+v", err, gvRes)
	}
	gvText := gvRes.Content[0].(mcp.TextContent).Text
	if strings.Contains(gvText, "guid") || strings.Contains(gvText, "is_deemed_positive") || strings.Contains(gvText, "taxes[") {
		t.Errorf("expected guid, is_deemed_positive, and taxes omitted by default, got:\n%s", gvText)
	}
}

func TestAgenticSpeedFeatures(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<LEDGER NAME="G.R. Metalloys &amp; Co">
			<PARENT>Sundry Debtors</PARENT>
			<OPENINGBALANCE>-1000.00</OPENINGBALANCE>
			<CLOSINGBALANCE>-5000.00</CLOSINGBALANCE>
		</LEDGER>
		<LEDGER NAME="Small Vendor">
			<PARENT>Sundry Creditors</PARENT>
			<CLOSINGBALANCE>500.00</CLOSINGBALANCE>
		</LEDGER>
		<VOUCHER VOUCHERNUMBER="INV-1" DATE="20260410" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="G.R. Metalloys &amp; Co" AMOUNT="-4000.00">
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>G.R. Metalloys &amp; Co</LEDGERNAME><AMOUNT>-4000.00</AMOUNT></ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>Sales</LEDGERNAME><AMOUNT>4000.00</AMOUNT></ALLLEDGERENTRIES.LIST>
		</VOUCHER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	var httpCalls atomic.Int32
	tallyClient := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 120})
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		httpCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	handlers := mcppkg.NewToolHandlers(tallyClient, "v1.0.8", "gops/TallyMCP", "toon")
	ctx := context.Background()

	// 1. HandleGetLedgerStatement reuses cached master ledger list (2 Tally calls total: ListLedgers + Vouchers, NOT 3)
	stmtRes, err := handlers.HandleGetLedgerStatement(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_get_ledger_statement",
			Arguments: map[string]any{"ledger_name": "GR Metalloys & Co", "from_date": "20260401"},
		},
	})
	if err != nil || stmtRes.IsError {
		t.Fatalf("HandleGetLedgerStatement failed: err=%v res=%+v", err, stmtRes)
	}
	if got := httpCalls.Load(); got != 2 {
		t.Errorf("expected 2 Tally calls (ListLedgers + Vouchers) reusing cache for opening/parent, got %d", got)
	}

	// 2. tally_list_vouchers auto-resolves fuzzy party ("GR Metalloys & Co" -> "G.R. Metalloys & Co") without 0-result detour
	vRes, err := handlers.HandleListVouchers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_list_vouchers",
			Arguments: map[string]any{"party": "GR Metalloys & Co", "from_date": "20260401"},
		},
	})
	if err != nil || vRes.IsError {
		t.Fatalf("HandleListVouchers fuzzy failed: err=%v res=%+v", err, vRes)
	}
	if !strings.Contains(vRes.Content[0].(mcp.TextContent).Text, "INV-1") {
		t.Errorf("expected fuzzy party resolution to find INV-1, got:\n%s", vRes.Content[0].(mcp.TextContent).Text)
	}

	// 3. tally_list_vouchers returns actionable recovery hint when 0 matches due to typo
	missRes, err := handlers.HandleListVouchers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_list_vouchers",
			Arguments: map[string]any{"party": "Metalloys Typo", "from_date": "20260401"},
		},
	})
	if err != nil || missRes.IsError {
		t.Fatalf("HandleListVouchers miss failed: err=%v res=%+v", err, missRes)
	}
	missText := missRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(missText, "did you mean") || !strings.Contains(missText, "G.R. Metalloys & Co") {
		t.Errorf("expected did-you-mean hint on 0 voucher matches, got:\n%s", missText)
	}

	// 4. tally_get_party_overview returns 360 profile + recent vouchers in 1 call
	poRes, err := handlers.HandleGetPartyOverview(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_get_party_overview",
			Arguments: map[string]any{"party": "GR Metalloys & Co", "from_date": "20260401"},
		},
	})
	if err != nil || poRes.IsError {
		t.Fatalf("HandleGetPartyOverview failed: err=%v res=%+v", err, poRes)
	}
	poText := poRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(poText, "closing_balance: 5000") || !strings.Contains(poText, "recent_count: 1") {
		t.Errorf("expected PartyOverview with balance & recent_count, got:\n%s", poText)
	}
}

func TestReadOnlyModeHandlers(t *testing.T) {
	cfg := config.DefaultConfig()
	client := tally.NewClient(cfg.Tally)
	handlers := mcppkg.NewToolHandlers(client, "v1.0.0", "gops/TallyMCP", "toon")
	handlers.SetReadOnly(true)

	ctx := context.Background()

	// 1. tally_create_ledger must be blocked in read-only mode
	res, err := handlers.HandleCreateLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_create_ledger",
			Arguments: map[string]any{"name": "Test Ledger", "parent": "Sundry Debtors"},
		},
	})
	if err != nil {
		t.Fatalf("HandleCreateLedger returned unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(mcp.TextContent).Text, "read-only mode") {
		t.Errorf("expected read-only error for HandleCreateLedger, got %+v", res)
	}

	// 2. mutating tally_execute_xml must be blocked in read-only mode
	xmlRes, err := handlers.HandleExecuteXML(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_execute_xml",
			Arguments: map[string]any{"xml_content": `<ENVELOPE><HEADER><TALLYREQUEST>Import</TALLYREQUEST></HEADER></ENVELOPE>`},
		},
	})
	if err != nil {
		t.Fatalf("HandleExecuteXML returned unexpected error: %v", err)
	}
	if !xmlRes.IsError || !strings.Contains(xmlRes.Content[0].(mcp.TextContent).Text, "read-only mode") {
		t.Errorf("expected read-only error for mutating HandleExecuteXML, got %+v", xmlRes)
	}
}

func TestServeRelayMCPEnforcesReadOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.ReadOnly = false // LAN allows writes, but relay allowWrites=false must still block
	client := tally.NewClient(cfg.Tally)
	srv := mcppkg.NewServer(cfg, client, "v1.0.0")

	ctx := context.Background()
	callReq := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tally_create_ledger","arguments":{"name":"Relay Test","parent":"Sundry Debtors"}}}`)
	respBytes := srv.ServeRelayMCP(ctx, callReq, false)
	if !strings.Contains(string(respBytes), "read-only mode") {
		t.Errorf("expected ServeRelayMCP(allowWrites=false) to block tally_create_ledger, got: %s", string(respBytes))
	}
}

func TestAgentSpeedFeatures(t *testing.T) {
	mockXML := `<ENVELOPE><HEADER><VERSION>1</VERSION></HEADER><BODY><DATA><COLLECTION>
		<LEDGER NAME="Alpha Traders"><PARENT>Sundry Debtors</PARENT><CLOSINGBALANCE>-1500.00</CLOSINGBALANCE></LEDGER>
		<LEDGER NAME="Beta Corp"><PARENT>Sundry Creditors</PARENT><CLOSINGBALANCE>2500.00</CLOSINGBALANCE></LEDGER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	tallyClient := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 30})
	tallyClient.SetTransport(mockRoundTripper(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	handlers := mcppkg.NewToolHandlers(tallyClient, "v1.0.20", "gops/TallyMCP", "toon")
	ctx := context.Background()

	// 1. Batch lookup via names in tally_get_ledger
	res, err := handlers.HandleGetLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_get_ledger",
			Arguments: map[string]any{"names": "Alpha Traders, Beta Corp, Missing Co"},
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("HandleGetLedger batch failed: err=%v res=%+v", err, res)
	}
	txt := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(txt, "Alpha Traders") || !strings.Contains(txt, "Beta Corp") || !strings.Contains(txt, "Missing Co") {
		t.Errorf("expected batch response to include both found ledgers and error for Missing Co, got:\n%s", txt)
	}

	// 2. ReadOnly & ExposeExpertTools=false hide tally_create_ledger and tally_execute_xml from tools/list
	cfg := config.DefaultConfig()
	cfg.Server.ReadOnly = true
	cfg.Server.ExposeExpertTools = false
	srv := mcppkg.NewServer(cfg, tallyClient, "v1.0.20")
	listReq := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	listResp := string(srv.ServeRelayMCP(ctx, listReq, false))
	if strings.Contains(listResp, `"tally_create_ledger"`) {
		t.Errorf("expected tally_create_ledger to be hidden when read_only=true")
	}
	if strings.Contains(listResp, `"tally_execute_xml"`) {
		t.Errorf("expected tally_execute_xml to be hidden when expose_expert_tools=false")
	}

	// 3. tally://context resource returns today and FY dates
	ctxRes, err := handlers.HandleResourceContext(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "tally://context"},
	})
	if err != nil || len(ctxRes) == 0 {
		t.Fatalf("HandleResourceContext failed: err=%v", err)
	}
	ctxText := ctxRes[0].(mcp.TextResourceContents).Text
	if !strings.Contains(ctxText, "current_fy_start") || !strings.Contains(ctxText, "current_fy_end") {
		t.Errorf("expected FY dates in tally://context, got:\n%s", ctxText)
	}
}
