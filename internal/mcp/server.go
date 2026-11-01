package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"tally-mcp/internal/auth"
	"tally-mcp/internal/config"
	"tally-mcp/internal/tally"
)

// Server coordinates the MCP server, tools, and transports.
type Server struct {
	cfg         *config.Config
	tallyClient *tally.Client
	mcpServer   *server.MCPServer
	handlers    *ToolHandlers
	version     string
	activePort  int
	mu          sync.Mutex
}

// ActivePort returns the currently bound network port.
func (s *Server) ActivePort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activePort
}

// NewServer initializes a new MCP Server with all tools registered.
func NewServer(cfg *config.Config, tallyClient *tally.Client, version string) *Server {
	s := &Server{
		cfg:         cfg,
		tallyClient: tallyClient,
		version:     version,
		handlers:    NewToolHandlers(tallyClient, version, cfg.Updater.GitHubRepo),
		mcpServer:   server.NewMCPServer("tally-mcp", version),
	}

	s.registerTools()
	return s
}

func (s *Server) registerTools() {
	// 1. tally_status
	s.mcpServer.AddTool(
		mcp.NewTool("tally_status",
			mcp.WithDescription("Check connection to Tally ERP 9 / TallyPrime and list loaded companies."),
		),
		s.handlers.HandleTallyStatus,
	)

	// 2. tally_list_companies
	s.mcpServer.AddTool(
		mcp.NewTool("tally_list_companies",
			mcp.WithDescription("List all companies currently loaded and active in Tally."),
		),
		s.handlers.HandleListCompanies,
	)

	// 3. tally_list_ledgers
	s.mcpServer.AddTool(
		mcp.NewTool("tally_list_ledgers",
			mcp.WithDescription("List account ledgers (customers, vendors, bank, cash) with balances and contact info."),
			mcp.WithString("group", mcp.Description("Optional parent group name to filter ledgers (e.g. 'Sundry Debtors', 'Sundry Creditors', 'Bank Accounts', 'Cash-in-Hand')")),
		),
		s.handlers.HandleListLedgers,
	)

	// 4. tally_get_ledger
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_ledger",
			mcp.WithDescription("Get full details and master information for a specific ledger account."),
			mcp.WithString("name", mcp.Description("Exact name of the ledger"), mcp.Required()),
		),
		s.handlers.HandleGetLedger,
	)

	// 5. tally_list_vouchers
	s.mcpServer.AddTool(
		mcp.NewTool("tally_list_vouchers",
			mcp.WithDescription("List accounting and inventory vouchers (Sales, Purchase, Payment, Receipt, Journal)."),
			mcp.WithString("voucher_type", mcp.Description("Optional voucher type (e.g. 'Sales', 'Purchase', 'Payment', 'Receipt', 'Journal')")),
			mcp.WithString("from_date", mcp.Description("Start date in YYYYMMDD format (e.g. '20240401')")),
			mcp.WithString("to_date", mcp.Description("End date in YYYYMMDD format (e.g. '20250331')")),
		),
		s.handlers.HandleListVouchers,
	)

	// 6. tally_get_stock_summary
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_stock_summary",
			mcp.WithDescription("Get inventory items, stock closing balances, units, rates, and valuations."),
			mcp.WithString("group", mcp.Description("Optional stock group name filter")),
		),
		s.handlers.HandleListStockItems,
	)

	// 7. tally_get_trial_balance
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_trial_balance",
			mcp.WithDescription("Fetch the Trial Balance report with debit/credit totals."),
			mcp.WithString("from_date", mcp.Description("Start date in YYYYMMDD format")),
			mcp.WithString("to_date", mcp.Description("End date in YYYYMMDD format")),
		),
		s.handlers.HandleGetTrialBalance,
	)

	// 8. tally_execute_xml
	s.mcpServer.AddTool(
		mcp.NewTool("tally_execute_xml",
			mcp.WithDescription("Execute raw XML / TDL query or action directly against Tally ERP HTTP interface."),
			mcp.WithString("xml_content", mcp.Description("Full XML request envelope to send to Tally"), mcp.Required()),
		),
		s.handlers.HandleExecuteXML,
	)

	// 9. tally_create_ledger
	s.mcpServer.AddTool(
		mcp.NewTool("tally_create_ledger",
			mcp.WithDescription("Create a new ledger account in Tally ERP / TallyPrime."),
			mcp.WithString("name", mcp.Description("Name of the new ledger"), mcp.Required()),
			mcp.WithString("parent", mcp.Description("Parent group (e.g. 'Sundry Debtors', 'Sundry Creditors', 'Direct Expenses')"), mcp.Required()),
			mcp.WithNumber("opening_balance", mcp.Description("Optional opening balance amount")),
		),
		s.handlers.HandleCreateLedger,
	)

	// 10. system_check_update
	s.mcpServer.AddTool(
		mcp.NewTool("system_check_update",
			mcp.WithDescription("Check GitHub Releases to see if a newer version of tally-mcp is available."),
		),
		s.handlers.HandleCheckUpdate,
	)

	// 11. system_apply_update
	s.mcpServer.AddTool(
		mcp.NewTool("system_apply_update",
			mcp.WithDescription("Download and apply the latest release binary from GitHub directly to this executable."),
		),
		s.handlers.HandleApplyUpdate,
	)
}

// Run starts the MCP server according to the configured transport.
func (s *Server) Run(ctx context.Context) error {
	switch s.cfg.Server.Transport {
	case "stdio":
		return s.RunStdio(ctx)
	default:
		return s.RunSSE(ctx)
	}
}

// RunStdio starts serving MCP over Standard I/O.
func (s *Server) RunStdio(ctx context.Context) error {
	log.Println("[INFO] Starting MCP server over stdio transport...")
	stdioServer := server.NewStdioServer(s.mcpServer)
	return stdioServer.Listen(ctx, nil, nil)
}

// RunSSE starts serving MCP over HTTP Server-Sent Events (SSE) for Local Network access,
// automatically finding an available port if the configured port is busy.
func (s *Server) RunSSE(ctx context.Context) error {
	listener, _, _, _, err := BindAvailableListener(s.cfg.Server.ListenAddress)
	if err != nil {
		return err
	}
	return s.RunSSEWithListener(ctx, listener)
}

// RunSSEWithListener serves MCP over SSE using an already bound net.Listener.
func (s *Server) RunSSEWithListener(ctx context.Context, listener net.Listener) error {
	addr := listener.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	s.mu.Lock()
	s.activePort = port
	s.mu.Unlock()

	// Disable localhost protection so other machines on the LAN can connect cleanly
	sseServer := server.NewSSEServer(s.mcpServer, server.WithSSEDisableLocalhostProtection(true))

	authMiddleware := auth.NewMiddleware(s.cfg.Auth)

	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"version":   s.version,
			"transport": "sse",
			"port":      port,
			"tally_url": s.tallyClient.BaseURL(),
			"auth":      s.cfg.Auth.Enabled,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// Web status / info page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			// Forward other routes to SSE server (which handles /sse and /message)
			authMiddleware.Wrap(sseServer).ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Tally MCP Server</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 40px; background: #0f172a; color: #f8fafc; }
    .card { background: #1e293b; border-radius: 12px; padding: 24px; max-width: 650px; margin: 0 auto; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.3); }
    h1 { margin-top: 0; color: #38bdf8; font-size: 24px; }
    .badge { display: inline-block; padding: 4px 10px; border-radius: 6px; font-size: 12px; font-weight: bold; background: #0284c7; color: #fff; margin-bottom: 16px; margin-right: 6px; }
    .port-badge { background: #10b981; }
    code { background: #0f172a; padding: 3px 6px; border-radius: 4px; color: #38bdf8; font-size: 13px; }
    pre { background: #0f172a; padding: 12px; border-radius: 8px; overflow-x: auto; color: #cbd5e1; font-size: 13px; }
    ul { padding-left: 20px; line-height: 1.6; }
    li { margin-bottom: 6px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>🚀 Tally ERP MCP Server</h1>
    <span class="badge">Version %s</span>
    <span class="badge port-badge">Port %d</span>
    <p>This server bridges AI agents and MCP clients across your local network directly into Tally ERP 9 / TallyPrime.</p>
    
    <h3>Network Endpoints</h3>
    <ul>
      <li><strong>MCP SSE Endpoint:</strong> <code>/sse</code></li>
      <li><strong>MCP Message Endpoint:</strong> <code>/message</code></li>
      <li><strong>Health Check:</strong> <code>/health</code></li>
    </ul>

    <h3>Configured Target</h3>
    <ul>
      <li><strong>Tally URL:</strong> <code>%s</code></li>
      <li><strong>Authentication:</strong> %s</li>
    </ul>

    <h3>Claude Desktop / Cursor MCP Configuration:</h3>
    <pre>{
  "mcpServers": {
    "tally": {
      "url": "http://&lt;server-ip&gt;:%d/sse"
    }
  }
}</pre>
  </div>
</body>
</html>`, s.version, port, s.tallyClient.BaseURL(), func() string {
			if s.cfg.Auth.Enabled {
				return "Enabled (API Key required)"
			}
			return "Disabled (Open LAN)"
		}(), port)
	})

	httpServer := &http.Server{
		Handler: mux,
	}

	// Server shutdown channel
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("[INFO] MCP Server listening on http://%s (SSE: http://%s/sse)", addr, addr)
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Println("[INFO] Shutting down MCP server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-serverErr:
		return err
	}
}
