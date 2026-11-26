package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// ServeRelayMCP dispatches a single JSON-RPC MCP message received from the 1erp.work relay.
// Unless allowWrites is true, mutating operations are blocked via read-only context enforcement.
func (s *Server) ServeRelayMCP(ctx context.Context, rawJSON []byte, allowWrites bool) []byte {
	if !allowWrites {
		ctx = WithReadOnlyContext(ctx)
	}
	resp := s.mcpServer.HandleMessage(ctx, json.RawMessage(rawJSON))
	if resp == nil {
		return nil
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return b
}

const mcpServerInstructions = `TallyMCP connects to Tally ERP 9 / TallyPrime (responses use compact TOON format by default).
- Skip tally_status/tally_list_companies warmup; call domain tools directly.
- Tally sign convention: negative (-) = Debit (assets/receivables/expenses), positive (+) = Credit (liabilities/payables/income).
- Fast shortcuts: tally_get_financial_summary (KPIs), tally_get_party_overview (customer/vendor 360: profile + unpaid bills + recent vouchers in 1 call), tally_list_vouchers(aggregate_only=true) (counts/totals), tally_get_outstandings (unpaid bills with sort/overdue_only).
- Dates accept YYYYMMDD, YYYY-MM-DD, DD-MM-YYYY, or keywords: today, yesterday, this_month, last_month, this_fy, last_fy.`

// NewServer initializes a new MCP Server with all tools registered.
func NewServer(cfg *config.Config, tallyClient *tally.Client, version string) *Server {
	handlers := NewToolHandlers(tallyClient, version, cfg.Updater.GitHubRepo, cfg.Server.PayloadFormat)
	handlers.SetReadOnly(cfg.Server.ReadOnly)

	s := &Server{
		cfg:         cfg,
		tallyClient: tallyClient,
		version:     version,
		handlers:    handlers,
		mcpServer:   server.NewMCPServer("tally-mcp", version, server.WithInstructions(mcpServerInstructions)),
	}

	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	return s
}

func (s *Server) registerTools() {
	// 1. tally_status
	s.mcpServer.AddTool(
		mcp.NewTool("tally_status",
			mcp.WithDescription("Check Tally connection and loaded companies; optionally start Tally."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithBoolean("start_if_stopped", mcp.Description("Start Tally automatically if stopped")),
			mcp.WithNumber("wait_seconds", mcp.Description("Max seconds to wait on start (default 30)")),
		),
		s.handlers.HandleTallyStatus,
	)

	// 2. tally_start
	s.mcpServer.AddTool(
		mcp.NewTool("tally_start",
			mcp.WithDescription("Start Tally ERP 9 / TallyPrime and wait for XML server readiness."),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("exe_path", mcp.Description("Optional custom tally.exe path")),
			mcp.WithNumber("wait_seconds", mcp.Description("Max wait seconds (default 30)")),
		),
		s.handlers.HandleStartTally,
	)

	// 3. tally_list_companies
	s.mcpServer.AddTool(
		mcp.NewTool("tally_list_companies",
			mcp.WithDescription("List loaded companies in Tally."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
		),
		s.handlers.HandleListCompanies,
	)

	// 4. tally_list_ledgers
	s.mcpServer.AddTool(
		mcp.NewTool("tally_list_ledgers",
			mcp.WithDescription("List ledgers (customers, vendors, bank, cash) with group aliases, search, sort, and pagination."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("group", mcp.Description("Group or alias (e.g. 'customers', 'vendors', 'Bank Accounts')")),
			mcp.WithString("search", mcp.Description("Case-insensitive substring filter")),
			mcp.WithString("sort", mcp.Enum("balance_desc", "balance_asc", "name_asc"), mcp.Description("Sort order")),
			mcp.WithNumber("limit", mcp.Description("Page size (default 25, max 250)")),
			mcp.WithNumber("offset", mcp.Description("Zero-based offset")),
			mcp.WithBoolean("summary_only", mcp.Description("Compact fields only (default true)")),
			mcp.WithString("fields", mcp.Description("Comma-separated fields to keep (e.g. 'name,balance')")),
		),
		s.handlers.HandleListLedgers,
	)

	// 5. tally_get_ledger
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_ledger",
			mcp.WithDescription("Get profile and closing balance for a ledger (auto-corrects minor name mismatches)."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("name", mcp.Description("Ledger name"), mcp.Required()),
		),
		s.handlers.HandleGetLedger,
	)

	// 5b. tally_get_party_overview
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_party_overview",
			mcp.WithDescription("1-call party 360: profile, closing balance, top pending/overdue bills, and recent vouchers."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("party", mcp.Description("Customer or vendor ledger name (fuzzy matched)"), mcp.Required()),
			mcp.WithString("from_date", mcp.Description("Recent voucher start date or keyword (default FY start)")),
			mcp.WithString("to_date", mcp.Description("End date")),
			mcp.WithNumber("recent_limit", mcp.Description("Max recent vouchers (default 10, max 50)")),
		),
		s.handlers.HandleGetPartyOverview,
	)

	// 6. tally_get_ledger_statement
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_ledger_statement",
			mcp.WithDescription("Get paginated transaction statement with running balance for a ledger."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("ledger_name", mcp.Required(), mcp.Description("Ledger name")),
			mcp.WithString("from_date", mcp.Description("Start date (YYYYMMDD/YYYY-MM-DD; default FY start)")),
			mcp.WithString("to_date", mcp.Description("End date (default FY end)")),
			mcp.WithNumber("limit", mcp.Description("Page size (default 50, max 500)")),
			mcp.WithNumber("offset", mcp.Description("Zero-based offset")),
			mcp.WithString("sort", mcp.Enum("asc", "desc"), mcp.Description("Date sort order (default asc)")),
			mcp.WithBoolean("summary_only", mcp.Description("Compact signed amount + truncated narration (default true)")),
			mcp.WithNumber("max_narration_len", mcp.Description("Max narration chars when summary_only=true (default 80, 0=full)")),
			mcp.WithString("fields", mcp.Description("Comma-separated entry fields to keep")),
		),
		s.handlers.HandleGetLedgerStatement,
	)

	// 7. tally_list_vouchers
	s.mcpServer.AddTool(
		mcp.NewTool("tally_list_vouchers",
			mcp.WithDescription("Search/list vouchers or aggregate totals. TIP: set aggregate_only=true for counts/sums; pass recent from_date for speed."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("voucher_type", mcp.Description("Receipt, Payment, Sales, Purchase, Journal, Contra")),
			mcp.WithString("party", mcp.Description("Party ledger substring")),
			mcp.WithString("ledger", mcp.Description("Filter touching any debit/credit split")),
			mcp.WithNumber("amount", mcp.Description("Exact gross amount")),
			mcp.WithNumber("amount_tolerance", mcp.Description("Amount tolerance (default 1.0)")),
			mcp.WithNumber("min_amount", mcp.Description("Min gross amount")),
			mcp.WithNumber("max_amount", mcp.Description("Max gross amount")),
			mcp.WithString("search", mcp.Description("Search party, voucher_number, reference, narration")),
			mcp.WithString("from_date", mcp.Description("Start date (default FY start; pass 'all' for full history)")),
			mcp.WithString("to_date", mcp.Description("End date")),
			mcp.WithString("sort", mcp.Enum("date_desc", "date_asc", "amount_desc", "amount_asc"), mcp.Description("Sort order (default date_desc)")),
			mcp.WithBoolean("include_entries", mcp.Description("Include compact split summary (default false)")),
			mcp.WithNumber("max_narration_len", mcp.Description("Max narration chars (default 120, 0=full)")),
			mcp.WithBoolean("aggregate_only", mcp.Description("Return count and total_amount only (no row list)")),
			mcp.WithString("group_by", mcp.Enum("voucher_type", "party", "month"), mcp.Description("Bucket aggregation when aggregate_only=true")),
			mcp.WithString("fields", mcp.Description("Comma-separated voucher fields to keep")),
			mcp.WithNumber("limit", mcp.Description("Page size (default 25, max 250)")),
			mcp.WithNumber("offset", mcp.Description("Zero-based offset")),
		),
		s.handlers.HandleListVouchers,
	)

	// 8. tally_get_voucher
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_voucher",
			mcp.WithDescription("Get full voucher drilldown (ledger splits, inventory, bills, bank allocations)."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("voucher_no", mcp.Description("Voucher/invoice number")),
			mcp.WithString("master_id", mcp.Description("Tally Master ID")),
			mcp.WithString("voucher_type", mcp.Description("Disambiguate voucher type")),
			mcp.WithString("date", mcp.Description("Disambiguate voucher date")),
			mcp.WithBoolean("include_taxes", mcp.Description("Include duplicate taxes summary (default false)")),
		),
		s.handlers.HandleGetVoucher,
	)

	// 9. tally_get_stock_summary
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_stock_summary",
			mcp.WithDescription("List inventory items, closing balances, and valuations with search and pagination."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("group", mcp.Description("Stock group filter")),
			mcp.WithString("search", mcp.Description("Substring search")),
			mcp.WithNumber("limit", mcp.Description("Page size (default 25, max 250)")),
			mcp.WithNumber("offset", mcp.Description("Zero-based offset")),
			mcp.WithBoolean("summary_only", mcp.Description("Compact fields only (default true)")),
			mcp.WithString("fields", mcp.Description("Comma-separated fields to keep")),
		),
		s.handlers.HandleListStockItems,
	)

	// 10. tally_get_financial_summary
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_financial_summary",
			mcp.WithDescription("Executive KPI snapshot (Cash/Bank, Receivables, Payables, Top 5 Debtors/Creditors, Revenue, Net Profit)."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("as_on_date", mcp.Description("Cutoff date (default today)")),
			mcp.WithString("from_date", mcp.Description("Optional FY start date")),
		),
		s.handlers.HandleGetFinancialSummary,
	)

	// 11. tally_get_report (consolidated Trial Balance, P&L, Balance Sheet, Day Book)
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_report",
			mcp.WithDescription("Fetch normalized, paginated financial report (trial_balance, profit_and_loss, balance_sheet, day_book)."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("report_type", mcp.Required(), mcp.Enum("trial_balance", "profit_and_loss", "balance_sheet", "day_book"), mcp.Description("Report kind")),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("from_date", mcp.Description("Start date")),
			mcp.WithString("to_date", mcp.Description("End / as-on date")),
			mcp.WithString("date", mcp.Description("Single date for day_book")),
			mcp.WithBoolean("explode", mcp.Description("Expand sub-groups in P&L/Balance Sheet (default false)")),
			mcp.WithString("search", mcp.Description("Filter rows by label/particulars")),
			mcp.WithString("voucher_type", mcp.Description("Filter day_book by voucher type")),
			mcp.WithString("sort", mcp.Enum("amount_desc", "amount_asc", "name_asc", "date_asc", "date_desc"), mcp.Description("Sort order")),
			mcp.WithNumber("min_amount", mcp.Description("Minimum absolute row amount")),
			mcp.WithBoolean("include_zero", mcp.Description("Include zero-value rows (default false)")),
			mcp.WithNumber("limit", mcp.Description("Page size (default 50, max 250)")),
			mcp.WithNumber("offset", mcp.Description("Zero-based offset")),
		),
		s.handlers.HandleGetReport,
	)

	// 12. tally_get_outstandings (consolidated Bills Receivable / Payable)
	s.mcpServer.AddTool(
		mcp.NewTool("tally_get_outstandings",
			mcp.WithDescription("Fetch paginated Bills Receivable or Bills Payable aging report."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithString("type", mcp.Required(), mcp.Enum("receivable", "payable"), mcp.Description("receivable (debtors) or payable (creditors)")),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("party", mcp.Description("Optional party filter (fuzzy matched)")),
			mcp.WithString("from_date", mcp.Description("Start date")),
			mcp.WithString("to_date", mcp.Description("End date")),
			mcp.WithBoolean("overdue_only", mcp.Description("Only overdue bills (overdue_days > 0)")),
			mcp.WithString("sort", mcp.Enum("overdue_desc", "amount_desc", "amount_asc", "date_asc", "date_desc"), mcp.Description("Sort order")),
			mcp.WithNumber("min_amount", mcp.Description("Minimum pending amount")),
			mcp.WithString("search", mcp.Description("Filter by party or bill ref")),
			mcp.WithNumber("limit", mcp.Description("Page size (default 50, max 250)")),
			mcp.WithNumber("offset", mcp.Description("Zero-based offset")),
		),
		s.handlers.HandleGetOutstandings,
	)

	// 13. tally_generate_pdf
	s.mcpServer.AddTool(
		mcp.NewTool("tally_generate_pdf",
			mcp.WithDescription("Export a Tally report or voucher to PDF and return its path and download URL."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("report_type", mcp.Description("Balance Sheet, Profit and Loss, Day Book, Trial Balance, Bills Receivable, Bills Payable, Voucher, Invoice"), mcp.Required()),
			mcp.WithString("voucher_no", mcp.Description("Voucher/invoice number")),
			mcp.WithString("voucher_type", mcp.Description("Voucher type")),
			mcp.WithString("master_id", mcp.Description("Tally Master ID")),
			mcp.WithString("party", mcp.Description("Party filter")),
			mcp.WithString("from_date", mcp.Description("Start date")),
			mcp.WithString("to_date", mcp.Description("End / as-on date")),
			mcp.WithString("date", mcp.Description("Single date")),
			mcp.WithString("filename", mcp.Description("Custom PDF filename")),
			mcp.WithBoolean("include_base64", mcp.Description("Include base64_pdf in text payload (default false)")),
			mcp.WithBoolean("include_blob", mcp.Description("Attach PDF as MCP binary resource (default false)")),
		),
		s.handlers.HandleGeneratePDF,
	)

	// 14. tally_execute_xml
	s.mcpServer.AddTool(
		mcp.NewTool("tally_execute_xml",
			mcp.WithDescription("Execute raw XML/TDL query against Tally (pre-flight guarded)."),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithString("xml_content", mcp.Description("XML request envelope"), mcp.Required()),
		),
		s.handlers.HandleExecuteXML,
	)

	// 15. tally_create_ledger
	s.mcpServer.AddTool(
		mcp.NewTool("tally_create_ledger",
			mcp.WithDescription("Create a new ledger account in Tally."),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithString("company", mcp.Description("Target company")),
			mcp.WithString("name", mcp.Description("Ledger name"), mcp.Required()),
			mcp.WithString("parent", mcp.Description("Parent group or alias (e.g. 'customers', 'vendors')"), mcp.Required()),
			mcp.WithNumber("opening_balance", mcp.Description("Optional opening balance")),
		),
		s.handlers.HandleCreateLedger,
	)

	if s.cfg.Updater.ExposeMCPTools {
		s.mcpServer.AddTool(
			mcp.NewTool("system_check_update",
				mcp.WithDescription("Check GitHub Releases for a newer version of tally-mcp."),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(true),
			),
			s.handlers.HandleCheckUpdate,
		)

		s.mcpServer.AddTool(
			mcp.NewTool("system_apply_update",
				mcp.WithDescription("Download and apply the latest release binary from GitHub."),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(true),
			),
			s.handlers.HandleApplyUpdate,
		)
	}
}

func (s *Server) registerResources() {
	s.mcpServer.AddResource(
		mcp.NewResource("tally://status", "tally_status",
			mcp.WithResourceTitle("Tally Connection & Queue Status"),
			mcp.WithResourceDescription("Real-time connectivity status, loaded company count, queue metrics, and Tally executable detection in TOON format."),
			mcp.WithMIMEType("text/plain"),
		),
		s.handlers.HandleResourceStatus,
	)

	s.mcpServer.AddResource(
		mcp.NewResource("tally://companies", "tally_companies",
			mcp.WithResourceTitle("Loaded Tally Companies"),
			mcp.WithResourceDescription("List of companies currently loaded and active in Tally ERP 9 / TallyPrime in TOON format."),
			mcp.WithMIMEType("text/plain"),
		),
		s.handlers.HandleResourceCompanies,
	)

	s.mcpServer.AddResource(
		mcp.NewResource("tally://financial-summary", "tally_financial_summary",
			mcp.WithResourceTitle("Executive Financial Summary Snapshot"),
			mcp.WithResourceDescription("Compact executive KPI snapshot (< 350 tokens) containing Cash & Bank, Receivables, Top 5 Debtors, Payables, Top 5 Creditors, Working Capital, Revenue, and Net Profit in TOON format."),
			mcp.WithMIMEType("text/plain"),
		),
		s.handlers.HandleResourceFinancialSummary,
	)
}

func (s *Server) registerPrompts() {
	s.mcpServer.AddPrompt(
		mcp.NewPrompt("tally_accounting_rules",
			mcp.WithPromptTitle("Tally Accounting Rules & TOON Guide"),
			mcp.WithPromptDescription("Essential accounting rules, Indian GAAP & Tally terminology (Sundry Debtors, Creditors, Contra, FY), and TOON format guide for AI agents."),
		),
		s.handlers.HandlePromptAccountingRules,
	)

	s.mcpServer.AddPrompt(
		mcp.NewPrompt("financial_health_audit",
			mcp.WithPromptTitle("Financial Health & Cash Flow Audit Workflow"),
			mcp.WithPromptDescription("Step-by-step workflow for auditing company financial health, cash flow, receivables aging, and profitability."),
			mcp.WithArgument("as_on_date", mcp.ArgumentDescription("Optional cutoff date in YYYYMMDD format")),
		),
		s.handlers.HandlePromptFinancialHealthAudit,
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
// It also spins up a background HTTP listener (trying port 8080 first, incrementing one by one if occupied)
// so that generated PDF downloads (/files/) and health check (/health) remain accessible over HTTP.
func (s *Server) RunStdio(ctx context.Context) error {
	log.Println("[INFO] Starting MCP server over stdio transport...")

	listenAddr := s.cfg.Server.ListenAddress
	if listenAddr == "" {
		listenAddr = "0.0.0.0:8080"
	}
	httpListener, _, activePort, _, err := BindAvailableListener(listenAddr)
	if err == nil {
		s.mu.Lock()
		s.activePort = activePort
		s.mu.Unlock()

		s.handlers.SetBaseURL(fmt.Sprintf("http://localhost:%d", activePort))

		mux := http.NewServeMux()
		authMiddleware := auth.NewMiddleware(s.cfg.Auth)
		pdfDir := filepath.Join(os.TempDir(), "tally-mcp-pdf")
		_ = os.MkdirAll(pdfDir, 0755)
		PruneTempPDFs(pdfDir, 24*time.Hour)
		mux.Handle("/files/", authMiddleware.Wrap(http.StripPrefix("/files/", noDirListingFileServer(pdfDir))))

		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"transport": "stdio+http-files",
				"port":      activePort,
				"endpoints": map[string]string{
					"files":  "/files",
					"health": "/health",
				},
			})
		})

		httpServer := &http.Server{
			Handler: mux,
		}

		go func() {
			<-ctx.Done()
			_ = httpServer.Shutdown(context.Background())
		}()

		go func() {
			if serveErr := httpServer.Serve(httpListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				log.Printf("[WARN] Background HTTP file server error: %v", serveErr)
			}
		}()
		log.Printf("[INFO] Background HTTP file server active on http://localhost:%d/files/ for PDF delivery.", activePort)
	} else {
		log.Printf("[WARN] Could not bind HTTP port for PDF serving: %v (PDFs will still be saved to disk)", err)
	}

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

// RunSSEWithListener serves MCP over Streamable HTTP (/mcp) and SSE (/sse) using an already bound net.Listener.
func (s *Server) RunSSEWithListener(ctx context.Context, listener net.Listener) error {
	addr := listener.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	s.mu.Lock()
	s.activePort = port
	s.mu.Unlock()

	s.handlers.SetBaseURL(fmt.Sprintf("http://localhost:%d", port))

	// Disable localhost protection so other machines on the LAN can connect cleanly
	sseServer := server.NewSSEServer(s.mcpServer, server.WithSSEDisableLocalhostProtection(true))
	streamableServer := server.NewStreamableHTTPServer(s.mcpServer, server.WithDisableLocalhostProtection(true))

	authMiddleware := auth.NewMiddleware(s.cfg.Auth)

	mux := http.NewServeMux()

	// Streamable HTTP endpoint (/mcp) - MCP Spec 2025-03-26
	mux.Handle("/mcp", withRequestBaseURL(authMiddleware.Wrap(streamableServer)))
	mux.Handle("/mcp/", withRequestBaseURL(authMiddleware.Wrap(streamableServer)))

	// Legacy SSE endpoint (/sse and /message)
	mux.Handle("/sse", withRequestBaseURL(authMiddleware.Wrap(sseServer)))
	mux.Handle("/message", withRequestBaseURL(authMiddleware.Wrap(sseServer)))

	// PDF file downloads endpoint (/files/) - directory listing disabled
	pdfDir := filepath.Join(os.TempDir(), "tally-mcp-pdf")
	_ = os.MkdirAll(pdfDir, 0755)
	PruneTempPDFs(pdfDir, 24*time.Hour)
	fileServer := http.StripPrefix("/files/", noDirListingFileServer(pdfDir))
	mux.Handle("/files/", authMiddleware.Wrap(fileServer))

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		qLen, qCap := s.tallyClient.QueueStats()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"version":    s.version,
			"transport":  "streamable-http,sse",
			"transports": []string{"streamable-http", "sse"},
			"endpoints": map[string]string{
				"streamable_http": "/mcp",
				"sse":             "/sse",
				"message":         "/message",
				"files":           "/files",
			},
			"port":           port,
			"tally_url":      s.tallyClient.BaseURL(),
			"tally_running":  s.tallyClient.IsRunning(r.Context()),
			"auto_start":     s.tallyClient.AutoStartEnabled(),
			"queue_length":   qLen,
			"queue_capacity": qCap,
			"auth":           s.cfg.Auth.Enabled,
			"timestamp":      time.Now().Format(time.RFC3339),
		})
	})

	// Web status / info page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
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
    .card { background: #1e293b; border-radius: 12px; padding: 24px; max-width: 680px; margin: 0 auto; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.3); }
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
      <li><strong>Streamable HTTP Endpoint:</strong> <code>/mcp</code> <span style="color:#38bdf8;">(Recommended - MCP Spec 2025-03-26)</span></li>
      <li><strong>MCP SSE Endpoint:</strong> <code>/sse</code> (Legacy SSE)</li>
      <li><strong>MCP Message Endpoint:</strong> <code>/message</code></li>
      <li><strong>PDF Downloads Endpoint:</strong> <code>/files/</code></li>
      <li><strong>Health Check:</strong> <code>/health</code></li>
    </ul>

    <h3>Configured Target</h3>
    <ul>
      <li><strong>Tally URL:</strong> <code>%s</code></li>
      <li><strong>Tally Status:</strong> %s</li>
      <li><strong>Auto-start Tally:</strong> %s</li>
      <li><strong>Authentication:</strong> %s</li>
    </ul>

    <h3>Modern MCP Client Configuration (Cursor / Windsurf / VS Code):</h3>
    <pre>{
  "mcpServers": {
    "tally": {
      "url": "http://&lt;server-ip&gt;:%d/mcp"
    }
  }
}</pre>

    <h3>Legacy SSE Client Configuration (Claude Desktop):</h3>
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
			if s.tallyClient.IsRunning(r.Context()) {
				return `<span style="color:#10b981;">● Connected (Running)</span>`
			}
			return `<span style="color:#f59e0b;">○ Not Running (Use 'tally_start' tool or tray to launch)</span>`
		}(), func() string {
			if s.tallyClient.AutoStartEnabled() {
				return `<span style="color:#10b981;">Enabled (auto-launches when needed)</span>`
			}
			return "Disabled"
		}(), func() string {
			if s.cfg.Auth.Enabled {
				return "Enabled (API Key required)"
			}
			return "Disabled (Open LAN)"
		}(), port, port)
	})

	httpServer := &http.Server{
		Handler: mux,
	}

	// Server shutdown channel
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("[INFO] MCP Server listening on http://%s (Streamable HTTP: http://%s/mcp, SSE: http://%s/sse)", addr, addr, addr)
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

type ctxKeyRequestBaseURL struct{}

func withRequestBaseURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "" {
			scheme := "http"
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				scheme = "https"
			}
			ctx := context.WithValue(r.Context(), ctxKeyRequestBaseURL{}, scheme+"://"+r.Host)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func requestBaseURLFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKeyRequestBaseURL{}).(string); ok {
		return v
	}
	return ""
}

// noDirListingFileServer serves files from dir while blocking directory listings (returning 404).
func noDirListingFileServer(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		fullPath := filepath.Join(dir, filepath.FromSlash(cleanPath))
		if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// PruneTempPDFs removes PDF files in dir older than maxAge.
func PruneTempPDFs(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".pdf") {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
