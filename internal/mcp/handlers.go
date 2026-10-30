package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"tally-mcp/internal/tally"
	"tally-mcp/internal/updater"
)

// ToolHandlers encapsulates dependencies for handling MCP tool calls.
type ToolHandlers struct {
	tallyClient    *tally.Client
	appVersion     string
	githubRepo     string
}

// NewToolHandlers creates a new ToolHandlers instance.
func NewToolHandlers(tallyClient *tally.Client, appVersion, githubRepo string) *ToolHandlers {
	return &ToolHandlers{
		tallyClient: tallyClient,
		appVersion:  appVersion,
		githubRepo:  githubRepo,
	}
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

// HandleListVouchers queries accounting and inventory vouchers.
func (h *ToolHandlers) HandleListVouchers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	voucherType := req.GetString("voucher_type", "")
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	vouchers, err := h.tallyClient.ListVouchers(ctx, voucherType, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list vouchers: %v", err)), nil
	}

	jsonStr, err := tally.PrettyJSON(vouchers)
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
