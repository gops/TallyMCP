package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"tally-mcp/internal/tally"
	"tally-mcp/internal/updater"
)

// ToolHandlers encapsulates dependencies for handling MCP tool calls.
type ToolHandlers struct {
	tallyClient *tally.Client
	appVersion  string
	githubRepo  string
	baseURL     string
}

// NewToolHandlers creates a new ToolHandlers instance.
func NewToolHandlers(tallyClient *tally.Client, appVersion, githubRepo string) *ToolHandlers {
	return &ToolHandlers{
		tallyClient: tallyClient,
		appVersion:  appVersion,
		githubRepo:  githubRepo,
	}
}

// SetBaseURL configures the base URL for generating absolute download links.
func (h *ToolHandlers) SetBaseURL(baseURL string) {
	h.baseURL = baseURL
}

// HandleTallyStatus checks connectivity to Tally ERP.
func (h *ToolHandlers) HandleTallyStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status, err := h.tallyClient.Ping(ctx)
	if err != nil {
		resp := map[string]any{
			"connected": false,
			"tally_url": h.tallyClient.BaseURL(),
			"error":     err.Error(),
			"hint":      "Verify Tally ERP 9 / TallyPrime is running and server port 9000 is enabled in F12 / Advanced Configuration.",
		}
		jsonStr, _ := tally.PrettyJSON(resp)
		return mcp.NewToolResultText(jsonStr), nil
	}

	jsonStr, err := tally.PrettyJSON(status)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleListCompanies lists loaded companies in Tally.
func (h *ToolHandlers) HandleListCompanies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	companies, err := h.tallyClient.ListCompanies(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list companies: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(companies)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleListLedgers lists accounts/ledgers with optional group filter.
func (h *ToolHandlers) HandleListLedgers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	group := req.GetString("group", "")

	ledgers, err := h.tallyClient.ListLedgers(ctx, group)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch ledgers: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(ledgers)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetLedger fetches details of a specific ledger.
func (h *ToolHandlers) HandleGetLedger(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Ledger 'name' argument is required"), nil
	}

	details, err := h.tallyClient.GetLedger(ctx, name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get ledger %q: %v", name, err)), nil
	}

	jsonStr, err := tally.PrettyJSON(details)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleListStockItems lists stock items from Tally inventory.
func (h *ToolHandlers) HandleListStockItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	group := req.GetString("group", "")

	items, err := h.tallyClient.ListStockItems(ctx, group)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list stock items: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(items)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleListVouchers queries accounting and inventory vouchers with pagination.
func (h *ToolHandlers) HandleListVouchers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	voucherType := req.GetString("voucher_type", "")
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")
	limit := req.GetInt("limit", 50)
	offset := req.GetInt("offset", 0)

	paginated, err := h.tallyClient.ListVouchersPaginated(ctx, voucherType, fromDate, toDate, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list vouchers: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(paginated)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetVoucher queries full voucher details with ledger splits, inventory, and tax breakdown.
func (h *ToolHandlers) HandleGetVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	masterID := req.GetString("master_id", "")
	if masterID == "" {
		if id := req.GetInt("master_id", 0); id > 0 {
			masterID = strconv.Itoa(id)
		}
	}
	voucherNo := req.GetString("voucher_no", "")
	if voucherNo == "" {
		voucherNo = req.GetString("voucher_number", "")
	}
	if voucherNo == "" {
		if num := req.GetInt("voucher_no", 0); num > 0 {
			voucherNo = strconv.Itoa(num)
		} else if num := req.GetInt("voucher_number", 0); num > 0 {
			voucherNo = strconv.Itoa(num)
		}
	}

	if masterID == "" && voucherNo == "" {
		return mcp.NewToolResultError("Either 'voucher_no' or 'master_id' is required to fetch voucher details."), nil
	}

	voucherType := req.GetString("voucher_type", "")
	date := req.GetString("date", "")

	detail, err := h.tallyClient.GetVoucher(ctx, masterID, voucherNo, voucherType, date)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get voucher: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(detail)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetTrialBalance returns the Trial Balance.
func (h *ToolHandlers) HandleGetTrialBalance(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	tb, err := h.tallyClient.GetTrialBalance(ctx, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get trial balance: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(tb)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetProfitAndLoss returns the Profit & Loss statement.
func (h *ToolHandlers) HandleGetProfitAndLoss(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	pl, err := h.tallyClient.GetProfitAndLoss(ctx, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get profit and loss: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(pl)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetBalanceSheet returns the Balance Sheet report.
func (h *ToolHandlers) HandleGetBalanceSheet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	asOnDate := req.GetString("as_on_date", "")
	if asOnDate == "" {
		asOnDate = req.GetString("to_date", "")
	}
	if asOnDate == "" {
		asOnDate = req.GetString("date", "")
	}
	fromDate := req.GetString("from_date", "")

	bs, err := h.tallyClient.GetBalanceSheet(ctx, asOnDate, fromDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get balance sheet: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(bs)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetOutstandingReceivables returns the Bills Receivable aging report.
func (h *ToolHandlers) HandleGetOutstandingReceivables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	party := req.GetString("party", "")
	if party == "" {
		party = req.GetString("party_name", "")
	}
	if party == "" {
		party = req.GetString("ledger_name", "")
	}
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	recv, err := h.tallyClient.GetOutstandingReceivables(ctx, party, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get outstanding receivables: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(recv)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetOutstandingPayables returns the Bills Payable aging report.
func (h *ToolHandlers) HandleGetOutstandingPayables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	party := req.GetString("party", "")
	if party == "" {
		party = req.GetString("party_name", "")
	}
	if party == "" {
		party = req.GetString("ledger_name", "")
	}
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	pay, err := h.tallyClient.GetOutstandingPayables(ctx, party, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get outstanding payables: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(pay)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGetDayBook returns all vouchers entered on a specific date.
func (h *ToolHandlers) HandleGetDayBook(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	date := req.GetString("date", "")
	if date == "" {
		date = req.GetString("for_date", "")
	}

	db, err := h.tallyClient.GetDayBook(ctx, date)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get day book: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(db)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleGeneratePDF generates a PDF for a specified report or voucher, stores it in temp directory, and sends it to the requesting agent.
func (h *ToolHandlers) HandleGeneratePDF(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reportType := req.GetString("report_type", "")
	if reportType == "" {
		reportType = req.GetString("report", "")
	}
	if reportType == "" {
		reportType = req.GetString("type", "Balance Sheet")
	}

	voucherNo := req.GetString("voucher_no", "")
	if voucherNo == "" {
		voucherNo = req.GetString("voucher_number", "")
	}
	if voucherNo == "" {
		voucherNo = req.GetString("invoice_no", "")
	}
	if voucherNo == "" {
		if num := req.GetInt("voucher_no", 0); num > 0 {
			voucherNo = strconv.Itoa(num)
		} else if num := req.GetInt("voucher_number", 0); num > 0 {
			voucherNo = strconv.Itoa(num)
		}
	}

	masterID := req.GetString("master_id", "")
	if masterID == "" {
		if id := req.GetInt("master_id", 0); id > 0 {
			masterID = strconv.Itoa(id)
		}
	}

	voucherType := req.GetString("voucher_type", "")
	party := req.GetString("party", "")
	if party == "" {
		party = req.GetString("party_name", "")
	}
	if party == "" {
		party = req.GetString("ledger_name", "")
	}

	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")
	date := req.GetString("date", "")
	if date == "" {
		date = req.GetString("for_date", "")
	}

	fileName := req.GetString("filename", "")
	if fileName == "" {
		fileName = req.GetString("file_name", "")
	}

	opts := tally.GeneratePDFOptions{
		ReportType:    reportType,
		VoucherNumber: voucherNo,
		VoucherType:   voucherType,
		MasterID:      masterID,
		Party:         party,
		FromDate:      fromDate,
		ToDate:        toDate,
		Date:          date,
		FileName:      fileName,
	}

	result, _, err := h.tallyClient.GeneratePDF(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to generate PDF: %v", err)), nil
	}

	if h.baseURL != "" {
		result.DownloadURL = strings.TrimRight(h.baseURL, "/") + result.DownloadURL
	}

	jsonStr, err := tally.PrettyJSON(result)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Return tool result with embedded binary resource as well as structured JSON text
	return mcp.NewToolResultResource(jsonStr, mcp.BlobResourceContents{
		URI:      "file://" + result.FilePath,
		MIMEType: "application/pdf",
		Blob:     result.Base64PDF,
	}), nil
}

// HandleExecuteXML executes an arbitrary TDL or XML query.
func (h *ToolHandlers) HandleExecuteXML(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xmlContent, err := req.RequireString("xml_content")
	if err != nil {
		return mcp.NewToolResultError("Argument 'xml_content' is required"), nil
	}

	raw, err := h.tallyClient.ExecuteRawXML(ctx, xmlContent)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Tally XML execution failed: %v", err)), nil
	}

	// Attempt to convert XML to JSON map for easier LLM consumption
	m, err := tally.XMLToMap(raw)
	if err == nil && len(m) > 0 {
		jsonStr, err := json.MarshalIndent(m, "", "  ")
		if err == nil {
			return mcp.NewToolResultText(string(jsonStr)), nil
		}
	}

	// Fallback to raw XML string
	return mcp.NewToolResultText(string(raw)), nil
}

// HandleCreateLedger creates a new ledger account in Tally.
func (h *ToolHandlers) HandleCreateLedger(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Ledger 'name' is required"), nil
	}
	parent, err := req.RequireString("parent")
	if err != nil {
		return mcp.NewToolResultError("Ledger 'parent' group is required (e.g., 'Sundry Debtors', 'Sundry Creditors')"), nil
	}
	openingBalance := req.GetFloat("opening_balance", 0.0)

	resp, err := h.tallyClient.CreateLedger(ctx, name, parent, openingBalance)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create ledger %q: %v", name, err)), nil
	}

	if strings.Contains(resp, "<CREATED>1</CREATED>") || !strings.Contains(resp, "<ERRORS>") {
		return mcp.NewToolResultText(fmt.Sprintf("Ledger %q under %q created successfully.", name, parent)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Tally Response:\n%s", resp)), nil
}

// HandleCheckUpdate queries GitHub Releases for updates.
func (h *ToolHandlers) HandleCheckUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	info, err := updater.CheckForUpdate(ctx, h.appVersion, h.githubRepo)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Update check failed: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(info)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// HandleApplyUpdate downloads and installs the latest version from GitHub.
func (h *ToolHandlers) HandleApplyUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	info, err := updater.CheckForUpdate(ctx, h.appVersion, h.githubRepo)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Checking for updates: %v", err)), nil
	}

	if !info.HasUpdate {
		return mcp.NewToolResultText(fmt.Sprintf("Already running the latest version (%s). No update required.", h.appVersion)), nil
	}

	err = updater.ApplyUpdate(ctx, info, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Applying update failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully updated to %s! Please restart the executable for changes to take effect.", info.LatestVersion)), nil
}
