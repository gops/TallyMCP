package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"tally-mcp/internal/tally"
	"tally-mcp/internal/toon"
	"tally-mcp/internal/updater"
)

// ToolHandlers encapsulates dependencies for handling MCP tool calls.
type ToolHandlers struct {
	tallyClient   *tally.Client
	appVersion    string
	githubRepo    string
	baseURL       string
	defaultFormat string
	readOnly      bool
}

// SetReadOnly configures whether mutating MCP operations are blocked.
func (h *ToolHandlers) SetReadOnly(readOnly bool) {
	h.readOnly = readOnly
}

type ctxKeyReadOnly struct{}

// WithReadOnlyContext marks a context as read-only (e.g. for relayed 1erp.work requests).
func WithReadOnlyContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyReadOnly{}, true)
}

func (h *ToolHandlers) isReadOnly(ctx context.Context) bool {
	if h.readOnly {
		return true
	}
	if ctx != nil {
		if v, ok := ctx.Value(ctxKeyReadOnly{}).(bool); ok && v {
			return true
		}
	}
	return false
}

// NewToolHandlers creates a new ToolHandlers instance.
func NewToolHandlers(tallyClient *tally.Client, appVersion, githubRepo, defaultFormat string) *ToolHandlers {
	return &ToolHandlers{
		tallyClient:   tallyClient,
		appVersion:    appVersion,
		githubRepo:    githubRepo,
		defaultFormat: defaultFormat,
	}
}

const maxToolOutputBytes = 16 * 1024

// capOutputSize enforces a soft byte budget so oversized payloads don't bloat the LLM context window.
func capOutputSize(out string) string {
	if len(out) <= maxToolOutputBytes {
		return out
	}
	cut := maxToolOutputBytes
	if idx := strings.LastIndex(out[:cut], "\n"); idx > maxToolOutputBytes/2 {
		cut = idx
	}
	return out[:cut] + "\n\n[TRUNCATED: response exceeded 16KB; pass smaller limit, narrower from_date/to_date, summary_only=true, or fields='...' to reduce size]"
}

// formatOutput serializes data into the requested format ("toon" or "json").
func (h *ToolHandlers) formatOutput(v any, reqFormat string) (string, error) {
	fmtChoice := strings.ToLower(strings.TrimSpace(reqFormat))
	if fmtChoice == "" {
		fmtChoice = strings.ToLower(strings.TrimSpace(h.defaultFormat))
	}
	var (
		out string
		err error
	)
	if fmtChoice == "json" || fmtChoice == "compact_json" {
		out, err = tally.CompactJSON(v)
	} else if fmtChoice == "pretty_json" {
		out, err = tally.PrettyJSON(v)
	} else {
		out, err = toon.Marshal(v)
	}
	if err != nil {
		return "", err
	}
	return capOutputSize(out), nil
}

func (h *ToolHandlers) isJSONFormat(reqFormat string) bool {
	fmtChoice := strings.ToLower(strings.TrimSpace(reqFormat))
	if fmtChoice == "" {
		fmtChoice = strings.ToLower(strings.TrimSpace(h.defaultFormat))
	}
	return fmtChoice == "json" || fmtChoice == "compact_json" || fmtChoice == "pretty_json"
}

func getBoolDefault(req mcp.CallToolRequest, key string, defaultVal bool) bool {
	args := req.GetArguments()
	if args != nil {
		if _, ok := args[key]; ok {
			return req.GetBool(key, defaultVal)
		}
	}
	return defaultVal
}

func getIntDefault(req mcp.CallToolRequest, key string, defaultVal int) int {
	args := req.GetArguments()
	if args != nil {
		if _, ok := args[key]; ok {
			return req.GetInt(key, defaultVal)
		}
	}
	return defaultVal
}

func parseDryRun(req mcp.CallToolRequest, defaultVal bool) bool {
	args := req.GetArguments()
	if args != nil {
		if _, ok := args["dry_run"]; ok {
			return req.GetBool("dry_run", defaultVal)
		}
		if _, ok := args["preview"]; ok {
			return req.GetBool("preview", defaultVal)
		}
	}
	return defaultVal
}

func decodeJSONArg[T any](rawVal any) (T, error) {
	var out T
	switch v := rawVal.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return out, nil
		}
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return out, err
		}
		return out, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return out, err
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return out, err
		}
		return out, nil
	}
}

func parseAddressArg(args map[string]any, key string) ([]string, bool) {
	if args == nil {
		return nil, false
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false
	}
	switch v := raw.(type) {
	case string:
		var lines []string
		for _, part := range strings.Split(strings.ReplaceAll(v, "\r\n", "\n"), "\n") {
			if t := strings.TrimSpace(part); t != "" {
				lines = append(lines, t)
			}
		}
		return lines, true
	case []string:
		return v, true
	case []any:
		var lines []string
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprintf("%v", item)); s != "" {
				lines = append(lines, s)
			}
		}
		return lines, true
	default:
		return nil, false
	}
}

func extractLedgerWriteOptions(req mcp.CallToolRequest, company string) tally.LedgerWriteOptions {
	args := req.GetArguments()
	opts := tally.LedgerWriteOptions{
		Company: company,
		Name:    req.GetString("name", ""),
		NewName: req.GetString("new_name", ""),
		Parent:  req.GetString("parent", ""),
	}
	if args != nil {
		if _, ok := args["opening_balance"]; ok {
			v := req.GetFloat("opening_balance", 0)
			opts.OpeningBalance = &v
		}
		if _, ok := args["state"]; ok {
			v := req.GetString("state", "")
			opts.State = &v
		}
		if _, ok := args["country"]; ok {
			v := req.GetString("country", "")
			opts.Country = &v
		}
		if _, ok := args["gstin"]; ok {
			v := req.GetString("gstin", "")
			opts.GSTIN = &v
		}
		if _, ok := args["gst_registration_type"]; ok {
			v := req.GetString("gst_registration_type", "")
			opts.GSTRegistrationType = &v
		}
		if _, ok := args["pan"]; ok {
			v := req.GetString("pan", "")
			opts.PAN = &v
		}
		if _, ok := args["maintain_bill_by_bill"]; ok {
			v := req.GetBool("maintain_bill_by_bill", false)
			opts.MaintainBillByBill = &v
		}
		if _, ok := args["credit_period"]; ok {
			v := req.GetString("credit_period", "")
			opts.CreditPeriod = &v
		}
	}
	opts.Address, opts.HasAddress = parseAddressArg(args, "address")
	return opts
}

func truncateString(s string, maxLen int) string {
	if maxLen <= 0 || s == "" {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

// projectFields filters elements of a slice (or primary slice inside a paginated map/struct) to the requested comma-separated fields.
func projectFields(v any, fieldsCSV string) any {
	fieldsCSV = strings.TrimSpace(fieldsCSV)
	if fieldsCSV == "" {
		return v
	}
	keep := make(map[string]bool)
	for _, f := range strings.Split(fieldsCSV, ",") {
		if t := strings.TrimSpace(f); t != "" {
			keep[strings.ToLower(t)] = true
		}
	}
	if len(keep) == 0 {
		return v
	}

	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return v
	}

	filterRow := func(row any) any {
		m, ok := row.(map[string]any)
		if !ok {
			return row
		}
		out := make(map[string]any, len(keep))
		for k, val := range m {
			if keep[strings.ToLower(k)] {
				out[k] = val
			}
		}
		return out
	}

	switch t := generic.(type) {
	case []any:
		res := make([]any, len(t))
		for i, item := range t {
			res[i] = filterRow(item)
		}
		return res
	case map[string]any:
		for _, sliceKey := range []string{"ledgers", "stock_items", "vouchers", "entries", "rows", "bills"} {
			if rawSlice, ok := t[sliceKey].([]any); ok {
				filtered := make([]any, len(rawSlice))
				for i, item := range rawSlice {
					filtered[i] = filterRow(item)
				}
				t[sliceKey] = filtered
				return t
			}
		}
		return filterRow(t)
	default:
		return v
	}
}

// SetBaseURL configures the base URL for generating absolute download links.
func (h *ToolHandlers) SetBaseURL(baseURL string) {
	h.baseURL = baseURL
}

func (h *ToolHandlers) resolveCompany(ctx context.Context, req mcp.CallToolRequest) (string, error) {
	raw := req.GetString("company", "")
	resolved, _, err := h.tallyClient.ResolveCompany(ctx, raw)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// HandleTallyStatus checks connectivity to Tally ERP.
func (h *ToolHandlers) HandleTallyStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if rawCompany := strings.TrimSpace(req.GetString("company", "")); rawCompany != "" {
		if _, _, err := h.tallyClient.ResolveCompany(ctx, rawCompany); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	startIfStopped := req.GetBool("start_if_stopped", false)
	waitSeconds := req.GetInt("wait_seconds", 30)

	status, err := h.tallyClient.Ping(ctx)
	if err != nil && startIfStopped {
		waitDuration := time.Duration(waitSeconds) * time.Second
		if waitDuration <= 0 {
			waitDuration = 30 * time.Second
		}
		startRes, startErr := h.tallyClient.EnsureRunning(ctx, waitDuration)
		if startErr == nil && startRes != nil && startRes.Connected {
			status, err = h.tallyClient.Ping(ctx)
		}
	}

	if err != nil {
		qLen, qCap := h.tallyClient.QueueStats()
		detectedExe, _ := h.tallyClient.FindExecutable("")
		resp := map[string]any{
			"connected":         false,
			"tally_url":         h.tallyClient.BaseURL(),
			"queue_length":      qLen,
			"queue_capacity":    qCap,
			"error":             err.Error(),
			"detected_exe_path": detectedExe,
			"can_start":         detectedExe != "",
			"hint":              "Tally ERP 9 / TallyPrime is not running. Use the 'tally_start' MCP tool to launch it, or set auto_start: true in config.yaml.",
		}
		outStr, formatErr := h.formatOutput(resp, req.GetString("format", ""))
		if formatErr != nil {
			return mcp.NewToolResultError(formatErr.Error()), nil
		}
		return mcp.NewToolResultText(outStr), nil
	}

	outStr, formatErr := h.formatOutput(status, req.GetString("format", ""))
	if formatErr != nil {
		return mcp.NewToolResultError(formatErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleStartTally starts Tally ERP / TallyPrime if not currently running.
func (h *ToolHandlers) HandleStartTally(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customExe := req.GetString("exe_path", "")
	waitSeconds := req.GetInt("wait_seconds", 30)
	if waitSeconds <= 0 {
		waitSeconds = 30
	}

	waitDuration := time.Duration(waitSeconds) * time.Second
	res, err := h.tallyClient.StartTally(ctx, customExe, waitDuration)
	if err != nil && res == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to start Tally: %v", err)), nil
	}

	outStr, formatErr := h.formatOutput(res, req.GetString("format", ""))
	if formatErr != nil {
		return mcp.NewToolResultError(formatErr.Error()), nil
	}

	return mcp.NewToolResultText(outStr), nil
}

// HandleListCompanies lists loaded companies in Tally.
func (h *ToolHandlers) HandleListCompanies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	companies, err := h.tallyClient.ListCompanies(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list companies: %v", err)), nil
	}

	outStr, err := h.formatOutput(companies, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleListLedgers lists accounts/ledgers with optional group filter, search query, pagination, and compact mode.
func (h *ToolHandlers) HandleListLedgers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	group := tally.NormalizeGroupAlias(req.GetString("group", ""))
	search := req.GetString("search", "")
	limit := req.GetInt("limit", 25)
	offset := req.GetInt("offset", 0)
	summaryOnly := getBoolDefault(req, "summary_only", true)
	if args := req.GetArguments(); args != nil {
		if _, ok := args["compact"]; ok {
			summaryOnly = req.GetBool("compact", summaryOnly)
		}
	}

	sortMode := req.GetString("sort", "")
	paginated, err := h.tallyClient.FilterLedgersWithOptions(ctx, tally.LedgerFilterOptions{
		Company:     company,
		Group:       group,
		Search:      search,
		Sort:        sortMode,
		GSTIN:       req.GetString("gstin", ""),
		State:       req.GetString("state", ""),
		PAN:         req.GetString("pan", ""),
		NonZeroOnly: getBoolDefault(req, "non_zero_only", false),
		Limit:       limit,
		Offset:      offset,
		SummaryOnly: summaryOnly,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch ledgers: %v", err)), nil
	}

	out := projectFields(paginated, req.GetString("fields", ""))
	outStr, err := h.formatOutput(out, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetLedger fetches details of a specific ledger (or batch via comma-separated 'names') as a clean flat profile.
func (h *ToolHandlers) HandleGetLedger(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	namesCSV := strings.TrimSpace(req.GetString("names", ""))
	if namesCSV != "" {
		var rawNames []string
		for _, part := range strings.Split(namesCSV, ",") {
			if t := strings.TrimSpace(part); t != "" {
				rawNames = append(rawNames, t)
			}
		}
		if len(rawNames) == 0 {
			return mcp.NewToolResultError("Argument 'names' did not contain any valid ledger names"), nil
		}
		if len(rawNames) > 25 {
			rawNames = rawNames[:25]
		}
		ledgers := make([]*tally.LedgerDetail, 0, len(rawNames))
		errs := make(map[string]string)
		for _, raw := range rawNames {
			resolvedName, correctedFrom, resolveErr := h.tallyClient.ResolveLedgerName(ctx, company, raw)
			if resolveErr != nil {
				errs[raw] = resolveErr.Error()
				continue
			}
			details, dErr := h.tallyClient.GetLedgerDetailForCompany(ctx, company, resolvedName)
			if dErr != nil {
				errs[raw] = dErr.Error()
				continue
			}
			if correctedFrom != "" {
				details.ResolvedFrom = correctedFrom
				details.ResolvedLedger = resolvedName
			}
			ledgers = append(ledgers, details)
		}
		res := map[string]any{
			"company": company,
			"count":   len(ledgers),
			"ledgers": ledgers,
		}
		if len(errs) > 0 {
			res["errors"] = errs
		}
		outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
		if fmtErr != nil {
			return mcp.NewToolResultError(fmtErr.Error()), nil
		}
		return mcp.NewToolResultText(outStr), nil
	}

	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Ledger 'name' or 'names' argument is required"), nil
	}

	resolvedName, correctedFrom, resolveErr := h.tallyClient.ResolveLedgerName(ctx, company, name)
	if resolveErr != nil {
		return mcp.NewToolResultError(resolveErr.Error()), nil
	}

	details, err := h.tallyClient.GetLedgerDetailForCompany(ctx, company, resolvedName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get ledger %q: %v", resolvedName, err)), nil
	}
	if correctedFrom != "" {
		details.ResolvedFrom = correctedFrom
		details.ResolvedLedger = resolvedName
	}

	outStr, err := h.formatOutput(details, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetPartyOverview returns a 360-degree view (profile + pending bills + recent vouchers) in 1 tool call.
func (h *ToolHandlers) HandleGetPartyOverview(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	party, err := req.RequireString("party")
	if err != nil {
		party = req.GetString("name", "")
		if party == "" {
			return mcp.NewToolResultError("Argument 'party' is required"), nil
		}
	}
	fromDate, toDate := tally.ResolveDateRange(req.GetString("from_date", ""), req.GetString("to_date", ""), time.Now())
	recentLimit := req.GetInt("recent_limit", 10)

	overview, err := h.tallyClient.GetPartyOverviewForCompany(ctx, company, party, fromDate, toDate, recentLimit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch party overview: %v", err)), nil
	}

	outStr, err := h.formatOutput(overview, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetLedgerStatement retrieves a detailed transaction statement for a specific ledger account.
func (h *ToolHandlers) HandleGetLedgerStatement(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return h.handleLedgerTransactions(ctx, req, true)
}

// HandleGetLedgerVouchers fetches all vouchers touching a specific ledger across any debit/credit line with running balances.
func (h *ToolHandlers) HandleGetLedgerVouchers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return h.handleLedgerTransactions(ctx, req, false)
}

func (h *ToolHandlers) handleLedgerTransactions(ctx context.Context, req mcp.CallToolRequest, defaultSummary bool) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ledgerName, err := req.RequireString("ledger_name")
	if err != nil {
		return mcp.NewToolResultError("Ledger argument 'ledger_name' is required"), nil
	}
	ledgerName = strings.TrimSpace(ledgerName)
	if ledgerName == "" {
		return mcp.NewToolResultError("Ledger argument 'ledger_name' cannot be empty"), nil
	}

	limit := req.GetInt("limit", 50)
	offset := req.GetInt("offset", 0)
	sortOrder := req.GetString("sort", "asc")
	summaryOnly := getBoolDefault(req, "summary_only", defaultSummary)
	maxNarrLen := getIntDefault(req, "max_narration_len", 80)

	payload, err := h.tallyClient.BuildLedgerStatementResult(
		ctx,
		company,
		ledgerName,
		req.GetString("from_date", ""),
		req.GetString("to_date", ""),
		sortOrder,
		limit,
		offset,
		summaryOnly,
		maxNarrLen,
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	payload = projectFields(payload, req.GetString("fields", ""))
	outStr, err := h.formatOutput(payload, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleListGroups lists account groups with hierarchy and opening/closing balances.
func (h *ToolHandlers) HandleListGroups(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromDate, toDate := tally.ResolveDateRange(req.GetString("from_date", ""), req.GetString("to_date", ""), time.Now())
	groups, err := h.tallyClient.ListGroupsWithDates(ctx, company, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list groups: %v", err)), nil
	}

	parentFilter := strings.TrimSpace(req.GetString("parent", ""))
	if parentFilter != "" {
		parentFilter = tally.NormalizeGroupAlias(parentFilter)
	}
	search := strings.ToLower(strings.TrimSpace(req.GetString("search", "")))
	nonZeroOnly := getBoolDefault(req, "non_zero_only", false)

	filtered := make([]tally.GroupItem, 0, len(groups))
	for _, g := range groups {
		if nonZeroOnly && g.OpeningBalance == 0 && g.ClosingBalance == 0 {
			continue
		}
		if parentFilter != "" && !strings.EqualFold(strings.TrimSpace(g.Parent), parentFilter) {
			continue
		}
		if search != "" &&
			!strings.Contains(strings.ToLower(g.Name), search) &&
			!strings.Contains(strings.ToLower(g.Parent), search) {
			continue
		}
		filtered = append(filtered, g)
	}

	total := len(filtered)
	limit := req.GetInt("limit", 100)
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := req.GetInt("offset", 0)
	if offset < 0 {
		offset = 0
	}
	var paged []tally.GroupItem
	var hasMore bool
	var nextOffset *int
	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		paged = filtered[offset:end]
		if end < total {
			hasMore = true
			n := end
			nextOffset = &n
		}
	} else {
		paged = []tally.GroupItem{}
	}

	res := map[string]any{
		"company":  company,
		"total":    total,
		"count":    len(paged),
		"offset":   offset,
		"limit":    limit,
		"has_more": hasMore,
		"groups":   paged,
	}
	if nextOffset != nil {
		res["next_offset"] = *nextOffset
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetGroupSummary returns a group's balances, sub-groups, and child ledgers.
func (h *ToolHandlers) HandleGetGroupSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	groupName, err := req.RequireString("group")
	if err != nil || strings.TrimSpace(groupName) == "" {
		return mcp.NewToolResultError("Argument 'group' is required"), nil
	}
	includeSub := getBoolDefault(req, "include_sub_groups", true)
	nonZeroOnly := getBoolDefault(req, "non_zero_only", false)

	res, err := h.tallyClient.GetGroupSummaryForCompany(
		ctx,
		company,
		groupName,
		req.GetString("from_date", ""),
		req.GetString("to_date", ""),
		includeSub,
		nonZeroOnly,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get group summary: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleListStockItems lists stock items from Tally inventory with optional group filter, search query, pagination, and compact mode.
func (h *ToolHandlers) HandleListStockItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	group := req.GetString("group", "")
	search := req.GetString("search", "")
	limit := req.GetInt("limit", 25)
	offset := req.GetInt("offset", 0)
	summaryOnly := getBoolDefault(req, "summary_only", true)
	if args := req.GetArguments(); args != nil {
		if _, ok := args["compact"]; ok {
			summaryOnly = req.GetBool("compact", summaryOnly)
		}
	}

	paginated, err := h.tallyClient.FilterStockItemsForCompany(ctx, company, group, search, limit, offset, summaryOnly)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list stock items: %v", err)), nil
	}

	out := projectFields(paginated, req.GetString("fields", ""))
	outStr, err := h.formatOutput(out, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleListVouchers queries accounting and inventory vouchers with TDL pushdown filters, search, aggregation, and pagination.
func (h *ToolHandlers) HandleListVouchers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	opts := tally.VoucherFilterOptions{
		Company:        company,
		VoucherType:    req.GetString("voucher_type", ""),
		FromDate:       req.GetString("from_date", ""),
		ToDate:         req.GetString("to_date", ""),
		Party:          req.GetString("party", ""),
		Ledger:         req.GetString("ledger", ""),
		Search:         req.GetString("search", ""),
		Sort:           req.GetString("sort", ""),
		IncludeEntries: req.GetBool("include_entries", false),
		Limit:          req.GetInt("limit", 25),
		Offset:         req.GetInt("offset", 0),
	}

	if opts.Party == "" {
		opts.Party = req.GetString("party_name", "")
	}
	if opts.Party == "" {
		opts.Party = req.GetString("customer", "")
	}
	if opts.Party == "" {
		opts.Party = req.GetString("vendor", "")
	}
	if opts.Party == "" {
		opts.Party = req.GetString("ledger_name", "")
	}

	// Safe date defaulting + relative period resolution
	var defaultedFromDate string
	trimmedFromDate := strings.TrimSpace(opts.FromDate)
	if trimmedFromDate == "" {
		defaultedFromDate = tally.CurrentFinancialYearStart(time.Now())
		opts.FromDate = defaultedFromDate
		if opts.ToDate != "" {
			opts.ToDate = tally.CleanDate(opts.ToDate)
		}
	} else if strings.EqualFold(trimmedFromDate, "all") {
		opts.FromDate = ""
		if opts.ToDate != "" {
			opts.ToDate = tally.CleanDate(opts.ToDate)
		}
	} else {
		opts.FromDate, opts.ToDate = tally.ResolveDateRange(opts.FromDate, opts.ToDate, time.Now())
	}

	// Auto-resolve unambiguous party/ledger punctuation/casing mismatches to avoid 0-result retries
	var unresolvedQuery string
	var hintCandidates []string
	if strings.TrimSpace(opts.Party) != "" {
		if resolved, _, rErr := h.tallyClient.ResolveLedgerName(ctx, company, opts.Party); rErr == nil && resolved != "" {
			opts.Party = resolved
		} else if lnf, ok := rErr.(*tally.LedgerNotFoundError); ok && len(lnf.Candidates) > 0 {
			unresolvedQuery = opts.Party
			hintCandidates = lnf.Candidates
		}
	}
	if strings.TrimSpace(opts.Ledger) != "" {
		if resolved, _, rErr := h.tallyClient.ResolveLedgerName(ctx, company, opts.Ledger); rErr == nil && resolved != "" {
			opts.Ledger = resolved
		} else if lnf, ok := rErr.(*tally.LedgerNotFoundError); ok && len(lnf.Candidates) > 0 && unresolvedQuery == "" {
			unresolvedQuery = opts.Ledger
			hintCandidates = lnf.Candidates
		}
	}

	args := req.GetArguments()
	if args != nil {
		if _, ok := args["amount"]; ok {
			amt := req.GetFloat("amount", 0.0)
			opts.Amount = &amt
		}
		if _, ok := args["amount_tolerance"]; ok {
			tol := req.GetFloat("amount_tolerance", 1.0)
			opts.AmountTolerance = &tol
		}
		if _, ok := args["min_amount"]; ok {
			minAmt := req.GetFloat("min_amount", 0.0)
			opts.MinAmount = &minAmt
		}
		if _, ok := args["max_amount"]; ok {
			maxAmt := req.GetFloat("max_amount", 0.0)
			opts.MaxAmount = &maxAmt
		}
	}

	if req.GetBool("aggregate_only", false) {
		summary, err := h.tallyClient.SummarizeVouchers(ctx, opts, req.GetString("group_by", ""))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to summarize vouchers: %v", err)), nil
		}
		if defaultedFromDate != "" {
			summary.Note = "default: current financial year (pass from_date or 'all')"
		}
		outStr, err := h.formatOutput(summary, req.GetString("format", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(outStr), nil
	}

	paginated, err := h.tallyClient.FilterVouchersPaginated(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list vouchers: %v", err)), nil
	}

	maxNarrLen := getIntDefault(req, "max_narration_len", 120)
	reqFormat := req.GetString("format", "")
	isJSON := h.isJSONFormat(reqFormat)
	for i := range paginated.Vouchers {
		if maxNarrLen > 0 {
			paginated.Vouchers[i].Narration = truncateString(paginated.Vouchers[i].Narration, maxNarrLen)
		}
		// Keep TOON 2D tabular when include_entries=true by collapsing splits into entries_summary.
		if opts.IncludeEntries && !isJSON && len(paginated.Vouchers[i].LedgerEntries) > 0 {
			parts := make([]string, 0, len(paginated.Vouchers[i].LedgerEntries))
			for _, leg := range paginated.Vouchers[i].LedgerEntries {
				parts = append(parts, fmt.Sprintf("%s: %.2f", leg.LedgerName, leg.Amount))
			}
			paginated.Vouchers[i].EntriesSummary = strings.Join(parts, " | ")
			paginated.Vouchers[i].LedgerEntries = nil
		}
	}

	if paginated.Total == 0 {
		if len(hintCandidates) > 0 {
			paginated.Note = fmt.Sprintf("0 matches for %q; did you mean: %s", unresolvedQuery, strings.Join(hintCandidates, ", "))
		} else if defaultedFromDate != "" {
			paginated.Note = fmt.Sprintf("0 matches in current financial year (%s+); retry with from_date='all' for historical records", defaultedFromDate)
		}
	} else if defaultedFromDate != "" {
		paginated.FromDate = defaultedFromDate
		paginated.Note = "default: current financial year (pass from_date or 'all')"
	} else if opts.FromDate != "" {
		paginated.FromDate = opts.FromDate
	}
	if defaultedFromDate != "" {
		paginated.FromDate = defaultedFromDate
	}
	if opts.ToDate != "" {
		paginated.ToDate = opts.ToDate
	}

	out := projectFields(paginated, req.GetString("fields", ""))
	outStr, err := h.formatOutput(out, reqFormat)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetVoucher queries full voucher details with ledger splits, inventory, and allocations.
func (h *ToolHandlers) HandleGetVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
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

	detail, err := h.tallyClient.GetVoucherForCompany(ctx, company, masterID, voucherNo, voucherType, date)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get voucher: %v (hint: verify voucher_no/master_id via tally_list_vouchers or pass voucher_type/date to disambiguate)", err)), nil
	}
	if !req.GetBool("include_taxes", false) {
		detail.Taxes = nil
	}

	outStr, err := h.formatOutput(detail, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

func extractReportOptions(req mcp.CallToolRequest) tally.ReportQueryOptions {
	return tally.ReportQueryOptions{
		Limit:       req.GetInt("limit", 50),
		Offset:      req.GetInt("offset", 0),
		IncludeZero: req.GetBool("include_zero", false),
		MinAmount:   req.GetFloat("min_amount", 0),
		OverdueOnly: req.GetBool("overdue_only", false),
		Search:      req.GetString("search", ""),
		VoucherType: req.GetString("voucher_type", ""),
		Sort:        req.GetString("sort", ""),
	}
}

// HandleGetReport is the unified handler for trial_balance, profit_and_loss, balance_sheet, and day_book.
func (h *ToolHandlers) HandleGetReport(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reportType := strings.ToLower(strings.TrimSpace(req.GetString("report_type", "")))
	switch reportType {
	case "trial_balance", "tb":
		return h.HandleGetTrialBalance(ctx, req)
	case "profit_and_loss", "pl", "p&l":
		return h.HandleGetProfitAndLoss(ctx, req)
	case "balance_sheet", "bs":
		return h.HandleGetBalanceSheet(ctx, req)
	case "day_book", "daybook", "db":
		return h.HandleGetDayBook(ctx, req)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Unsupported report_type %q; expected trial_balance, profit_and_loss, balance_sheet, or day_book", reportType)), nil
	}
}

// HandleGetOutstandings is the unified handler for receivable and payable aging reports.
func (h *ToolHandlers) HandleGetOutstandings(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	kind := strings.ToLower(strings.TrimSpace(req.GetString("type", "")))
	switch kind {
	case "receivable", "receivables", "bills_receivable", "ar", "debtors":
		return h.HandleGetOutstandingReceivables(ctx, req)
	case "payable", "payables", "bills_payable", "ap", "creditors":
		return h.HandleGetOutstandingPayables(ctx, req)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Unsupported outstandings type %q; expected 'receivable' or 'payable'", kind)), nil
	}
}

// resolveCompareRange parses compare_to (e.g. "last_month", "last_fy", "q1", or "YYYYMMDD..YYYYMMDD") into (fromDate, toDate).
func resolveCompareRange(compareTo string, now time.Time) (string, string) {
	c := strings.TrimSpace(compareTo)
	if c == "" {
		return "", ""
	}
	if strings.Contains(c, "..") {
		parts := strings.SplitN(c, "..", 2)
		return tally.ResolveDateRange(parts[0], parts[1], now)
	}
	return tally.ResolveDateRange(c, "", now)
}

// HandleGetTrialBalance returns the Trial Balance.
func (h *ToolHandlers) HandleGetTrialBalance(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	now := time.Now()
	fromDate, toDate := tally.ResolveDateRange(req.GetString("from_date", ""), req.GetString("to_date", ""), now)
	opts := extractReportOptions(req)

	tb, err := h.tallyClient.GetTrialBalanceForCompany(ctx, company, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get trial balance: %v", err)), nil
	}
	normalized := tally.NormalizeReportWithOptions("trial_balance", tb, opts)

	var out any = normalized
	if cmpFrom, cmpTo := resolveCompareRange(req.GetString("compare_to", ""), now); cmpFrom != "" || cmpTo != "" {
		cmpTB, cmpErr := h.tallyClient.GetTrialBalanceForCompany(ctx, company, cmpFrom, cmpTo)
		if cmpErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to get comparison trial balance (%s..%s): %v", cmpFrom, cmpTo, cmpErr)), nil
		}
		out = map[string]any{
			"current": normalized,
			"compare": map[string]any{
				"from_date": cmpFrom,
				"to_date":   cmpTo,
				"data":      tally.NormalizeReportWithOptions("trial_balance", cmpTB, opts),
			},
		}
	}

	outStr, err := h.formatOutput(out, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetProfitAndLoss returns the Profit & Loss statement.
func (h *ToolHandlers) HandleGetProfitAndLoss(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	now := time.Now()
	fromDate, toDate := tally.ResolveDateRange(req.GetString("from_date", ""), req.GetString("to_date", ""), now)
	explode := getBoolDefault(req, "explode", false)
	opts := extractReportOptions(req)

	pl, err := h.tallyClient.GetProfitAndLossWithOptions(ctx, company, fromDate, toDate, explode)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get profit and loss: %v", err)), nil
	}
	normalized := tally.NormalizeReportWithOptions("profit_and_loss", pl, opts)

	var out any = normalized
	if cmpFrom, cmpTo := resolveCompareRange(req.GetString("compare_to", ""), now); cmpFrom != "" || cmpTo != "" {
		cmpPL, cmpErr := h.tallyClient.GetProfitAndLossWithOptions(ctx, company, cmpFrom, cmpTo, explode)
		if cmpErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to get comparison P&L (%s..%s): %v", cmpFrom, cmpTo, cmpErr)), nil
		}
		out = map[string]any{
			"current": normalized,
			"compare": map[string]any{
				"from_date": cmpFrom,
				"to_date":   cmpTo,
				"data":      tally.NormalizeReportWithOptions("profit_and_loss", cmpPL, opts),
			},
		}
	}

	outStr, err := h.formatOutput(out, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetBalanceSheet returns the Balance Sheet report.
func (h *ToolHandlers) HandleGetBalanceSheet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	now := time.Now()
	asOnDate := req.GetString("as_on_date", "")
	if asOnDate == "" {
		asOnDate = req.GetString("to_date", "")
	}
	if asOnDate == "" {
		asOnDate = req.GetString("date", "")
	}
	fromDate := req.GetString("from_date", "")
	explode := getBoolDefault(req, "explode", false)
	opts := extractReportOptions(req)

	bs, err := h.tallyClient.GetBalanceSheetWithOptions(ctx, company, asOnDate, fromDate, explode)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get balance sheet: %v", err)), nil
	}
	normalized := tally.NormalizeReportWithOptions("balance_sheet", bs, opts)

	var out any = normalized
	if cmpFrom, cmpTo := resolveCompareRange(req.GetString("compare_to", ""), now); cmpFrom != "" || cmpTo != "" {
		cmpBS, cmpErr := h.tallyClient.GetBalanceSheetWithOptions(ctx, company, cmpTo, cmpFrom, explode)
		if cmpErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to get comparison balance sheet (%s..%s): %v", cmpFrom, cmpTo, cmpErr)), nil
		}
		out = map[string]any{
			"current": normalized,
			"compare": map[string]any{
				"from_date": cmpFrom,
				"to_date":   cmpTo,
				"data":      tally.NormalizeReportWithOptions("balance_sheet", cmpBS, opts),
			},
		}
	}

	outStr, err := h.formatOutput(out, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetFinancialSummary returns an executive snapshot of key financial metrics (Cash/Bank, Receivables, Payables, Top Debtors/Creditors, Working Capital) in < 350 tokens.
func (h *ToolHandlers) HandleGetFinancialSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	now := time.Now()
	asOnDate := req.GetString("as_on_date", "")
	if asOnDate == "" {
		asOnDate = req.GetString("to_date", "")
	}
	if asOnDate == "" {
		asOnDate = req.GetString("date", "")
	}
	fromDate := req.GetString("from_date", "")

	summary, err := h.tallyClient.GetFinancialSummaryForCompany(ctx, company, asOnDate, fromDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to generate financial summary: %v", err)), nil
	}

	var out any = summary
	if cmpFrom, cmpTo := resolveCompareRange(req.GetString("compare_to", ""), now); cmpFrom != "" || cmpTo != "" {
		cmpSummary, cmpErr := h.tallyClient.GetFinancialSummaryForCompany(ctx, company, cmpTo, cmpFrom)
		if cmpErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to generate comparison financial summary (%s..%s): %v", cmpFrom, cmpTo, cmpErr)), nil
		}
		out = map[string]any{
			"current": summary,
			"compare": map[string]any{
				"from_date": cmpFrom,
				"to_date":   cmpTo,
				"data":      cmpSummary,
			},
		}
	}

	outStr, err := h.formatOutput(out, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetOutstandingReceivables returns the Bills Receivable aging report.
func (h *ToolHandlers) HandleGetOutstandingReceivables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	party := req.GetString("party", "")
	if party == "" {
		party = req.GetString("party_name", "")
	}
	if party == "" {
		party = req.GetString("ledger_name", "")
	}
	if strings.TrimSpace(party) != "" {
		resolved, _, resolveErr := h.tallyClient.ResolveLedgerName(ctx, company, party)
		if resolveErr != nil {
			return mcp.NewToolResultError(resolveErr.Error()), nil
		}
		party = resolved
	}
	fromDate, toDate := tally.ResolveDateRange(req.GetString("from_date", ""), req.GetString("to_date", ""), time.Now())

	recv, err := h.tallyClient.GetOutstandingReceivablesWithOptions(ctx, company, party, fromDate, toDate, getBoolDefault(req, "explode", true))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get outstanding receivables: %v", err)), nil
	}

	normalized := tally.NormalizeReportWithOptions("bills_receivable", recv, extractReportOptions(req))
	outStr, err := h.formatOutput(normalized, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetOutstandingPayables returns the Bills Payable aging report.
func (h *ToolHandlers) HandleGetOutstandingPayables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	party := req.GetString("party", "")
	if party == "" {
		party = req.GetString("party_name", "")
	}
	if party == "" {
		party = req.GetString("ledger_name", "")
	}
	if strings.TrimSpace(party) != "" {
		resolved, _, resolveErr := h.tallyClient.ResolveLedgerName(ctx, company, party)
		if resolveErr != nil {
			return mcp.NewToolResultError(resolveErr.Error()), nil
		}
		party = resolved
	}
	fromDate, toDate := tally.ResolveDateRange(req.GetString("from_date", ""), req.GetString("to_date", ""), time.Now())

	pay, err := h.tallyClient.GetOutstandingPayablesWithOptions(ctx, company, party, fromDate, toDate, getBoolDefault(req, "explode", true))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get outstanding payables: %v", err)), nil
	}

	normalized := tally.NormalizeReportWithOptions("bills_payable", pay, extractReportOptions(req))
	outStr, err := h.formatOutput(normalized, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetDayBook returns all vouchers entered on a specific date.
func (h *ToolHandlers) HandleGetDayBook(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	date := req.GetString("date", "")
	if date == "" {
		date = req.GetString("for_date", "")
	}
	if date == "" {
		date = time.Now().Format("20060102")
	}

	db, err := h.tallyClient.GetDayBookWithOptions(ctx, company, date, getBoolDefault(req, "explode", true))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get day book: %v", err)), nil
	}

	normalized := tally.NormalizeReportWithOptions("day_book", db, extractReportOptions(req))
	outStr, err := h.formatOutput(normalized, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGeneratePDF generates a PDF for a specified report or voucher, stores it in temp directory, and sends it to the requesting agent.
func (h *ToolHandlers) HandleGeneratePDF(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
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
	if date == "" {
		date = time.Now().Format("20060102")
	}

	fileName := req.GetString("filename", "")
	if fileName == "" {
		fileName = req.GetString("file_name", "")
	}
	includeBase64 := req.GetBool("include_base64", false)

	opts := tally.GeneratePDFOptions{
		Company:       company,
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
	if result.FilePath != "" {
		PruneTempPDFs(filepath.Dir(result.FilePath), 24*time.Hour)
	}

	baseURL := requestBaseURLFromContext(ctx)
	if baseURL == "" {
		baseURL = h.baseURL
	}
	if baseURL != "" {
		result.DownloadURL = strings.TrimRight(baseURL, "/") + result.DownloadURL
	}

	includeBlob := req.GetBool("include_blob", false) || req.GetBool("attach_resource", false)

	blobBase64 := result.Base64PDF
	displayResult := *result
	if !includeBase64 {
		// Prevent dumping hundreds of KB of base64 text into LLM context windows
		displayResult.Base64PDF = ""
	}

	outStr, err := h.formatOutput(displayResult, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if !includeBlob {
		return mcp.NewToolResultText(outStr), nil
	}

	// Return tool result with embedded binary resource as well as compact metadata text when explicitly requested
	return mcp.NewToolResultResource(outStr, mcp.BlobResourceContents{
		URI:      "file://" + result.FilePath,
		MIMEType: "application/pdf",
		Blob:     blobBase64,
	}), nil
}

// HandleCreateLedger creates a new ledger account in Tally (with optional GST, PAN, address, and bill-wise settings).
func (h *ToolHandlers) HandleCreateLedger(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dryRun := parseDryRun(req, false)
	if !dryRun && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (tally_create_ledger is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	name, err := req.RequireString("name")
	if err != nil || strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("Ledger 'name' is required"), nil
	}
	parent, err := req.RequireString("parent")
	if err != nil || strings.TrimSpace(parent) == "" {
		return mcp.NewToolResultError("Ledger 'parent' group is required (e.g., 'Sundry Debtors', 'Sundry Creditors')"), nil
	}

	opts := extractLedgerWriteOptions(req, company)
	opts.Name = name
	opts.Parent = parent

	res, err := h.tallyClient.CreateLedgerWithOptions(ctx, opts, dryRun)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create ledger %q: %v", name, err)), nil
	}

	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleUpdateLedger alters or renames an existing ledger in Tally (supports dry_run).
func (h *ToolHandlers) HandleUpdateLedger(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dryRun := parseDryRun(req, false)
	if !dryRun && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (tally_update_ledger is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	name, err := req.RequireString("name")
	if err != nil || strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("Ledger 'name' is required"), nil
	}

	opts := extractLedgerWriteOptions(req, company)
	opts.Name = name

	res, err := h.tallyClient.UpdateLedgerForCompany(ctx, opts, dryRun)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to update ledger %q: %v", name, err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleDeleteLedger deletes an unused ledger after verifying 0 balances and 0 vouchers.
func (h *ToolHandlers) HandleDeleteLedger(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dryRun := parseDryRun(req, false)
	if !dryRun && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (tally_delete_ledger is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	name, err := req.RequireString("name")
	if err != nil || strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("Ledger 'name' is required"), nil
	}

	res, err := h.tallyClient.DeleteLedgerForCompany(ctx, company, name, dryRun)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleMergeLedgers safely migrates vouchers and opening balances from source_ledger into target_ledger and deletes source_ledger.
func (h *ToolHandlers) HandleMergeLedgers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dryRun := parseDryRun(req, false)
	if !dryRun && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (tally_merge_ledgers is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	src, err := req.RequireString("source_ledger")
	if err != nil || strings.TrimSpace(src) == "" {
		return mcp.NewToolResultError("Argument 'source_ledger' is required"), nil
	}
	tgt, err := req.RequireString("target_ledger")
	if err != nil || strings.TrimSpace(tgt) == "" {
		return mcp.NewToolResultError("Argument 'target_ledger' is required"), nil
	}
	requireSameGSTIN := getBoolDefault(req, "require_same_gstin", true)

	res, err := h.tallyClient.MergeLedgersForCompany(ctx, company, src, tgt, requireSameGSTIN, dryRun)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ledger merge failed: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleCreateVoucher validates, resolves ledgers, and previews (default) or imports a voucher into Tally.
func (h *ToolHandlers) HandleCreateVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	preview := parseDryRun(req, true)
	if !preview && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (posting vouchers with preview=false is disabled)."), nil
	}

	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	vType, err := req.RequireString("voucher_type")
	if err != nil {
		return mcp.NewToolResultError("Argument 'voucher_type' is required (e.g. Sales, Purchase, Payment, Receipt, Journal)"), nil
	}

	args := req.GetArguments()
	rawEntriesVal, ok := args["entries"]
	if !ok || rawEntriesVal == nil {
		return mcp.NewToolResultError("Argument 'entries' is required (array of {ledger_name, amount, is_debit})"), nil
	}

	entries, err := decodeJSONArg[[]tally.VoucherEntryInput](rawEntriesVal)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid 'entries' format: %v", err)), nil
	}

	var inventoryEntries []tally.InventoryEntryInput
	if rawInvVal, hasInv := args["inventory_entries"]; hasInv && rawInvVal != nil {
		inventoryEntries, err = decodeJSONArg[[]tally.InventoryEntryInput](rawInvVal)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid 'inventory_entries' format: %v", err)), nil
		}
	}

	allowDuplicate := getBoolDefault(req, "allow_duplicate", false)

	res, err := h.tallyClient.CreateVoucherForCompany(
		ctx,
		company,
		vType,
		req.GetString("date", ""),
		req.GetString("voucher_number", ""),
		req.GetString("reference", ""),
		req.GetString("party_ledger", ""),
		req.GetString("narration", ""),
		entries,
		inventoryEntries,
		preview,
		allowDuplicate,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Voucher %s failed: %v", map[bool]string{true: "preview", false: "creation"}[preview], err)), nil
	}

	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetGSTSummary returns a single-call GST compliance and tax liability snapshot.
func (h *ToolHandlers) HandleGetGSTSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")
	if fromDate == "" && toDate == "" {
		fromDate = "this_month"
	}
	breakdown := getBoolDefault(req, "breakdown", false)

	res, err := h.tallyClient.GetGSTSummaryForCompany(ctx, company, fromDate, toDate, breakdown)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to generate GST summary: %v", err)), nil
	}

	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleUpdateVoucher updates an existing voucher by master_id (date, narration, reference, voucher_number, entries, or reclassify_ledger).
func (h *ToolHandlers) HandleUpdateVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dryRun := parseDryRun(req, false)
	if !dryRun && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (tally_update_voucher is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	masterID, err := req.RequireString("master_id")
	if err != nil || strings.TrimSpace(masterID) == "" {
		return mcp.NewToolResultError("Argument 'master_id' is required for tally_update_voucher"), nil
	}

	args := req.GetArguments()
	var entries []tally.VoucherEntryInput
	if args != nil {
		if rawVal, ok := args["entries"]; ok && rawVal != nil {
			entries, err = decodeJSONArg[[]tally.VoucherEntryInput](rawVal)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Invalid 'entries' format: %v", err)), nil
			}
		}
	}

	var reclassify *tally.ReclassifyLedgerSpec
	if args != nil {
		if rawRec, ok := args["reclassify_ledger"]; ok && rawRec != nil {
			switch rv := rawRec.(type) {
			case string:
				if strings.TrimSpace(rv) != "" {
					if strings.HasPrefix(strings.TrimSpace(rv), "{") {
						parsed, rErr := decodeJSONArg[tally.ReclassifyLedgerSpec](rv)
						if rErr != nil {
							return mcp.NewToolResultError(fmt.Sprintf("Invalid JSON in 'reclassify_ledger': %v", rErr)), nil
						}
						reclassify = &parsed
					} else {
						reclassify = &tally.ReclassifyLedgerSpec{
							FromLedger: rv,
							ToLedger:   req.GetString("to_ledger", ""),
						}
					}
				}
			default:
				parsed, rErr := decodeJSONArg[tally.ReclassifyLedgerSpec](rv)
				if rErr != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Invalid 'reclassify_ledger' structure: %v", rErr)), nil
				}
				reclassify = &parsed
			}
		} else if fromL := strings.TrimSpace(req.GetString("from_ledger", "")); fromL != "" {
			reclassify = &tally.ReclassifyLedgerSpec{
				FromLedger: fromL,
				ToLedger:   req.GetString("to_ledger", ""),
			}
		}
		if reclassify != nil {
			if rawBills, ok := args["bill_allocations"]; ok && rawBills != nil && len(reclassify.BillAllocations) == 0 {
				bills, bErr := decodeJSONArg[[]tally.BillAllocationInput](rawBills)
				if bErr != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Invalid 'bill_allocations' format: %v", bErr)), nil
				}
				reclassify.BillAllocations = bills
			}
		}
	}

	hasRef := false
	hasNarr := false
	if args != nil {
		_, hasRef = args["reference"]
		_, hasNarr = args["narration"]
	}

	res, err := h.tallyClient.UpdateVoucherForCompany(ctx, tally.UpdateVoucherOptions{
		Company:          company,
		MasterID:         masterID,
		NewDate:          req.GetString("new_date", ""),
		NewVoucherNumber: req.GetString("voucher_number", ""),
		Reference:        req.GetString("reference", ""),
		HasReference:     hasRef,
		PartyLedger:      req.GetString("party_ledger", ""),
		Narration:        req.GetString("narration", ""),
		HasNarration:     hasNarr,
		Entries:          entries,
		Reclassify:       reclassify,
		DryRun:           dryRun,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Voucher update failed: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleDeleteVoucher deletes a single voucher by master_id (supports dry_run=true).
func (h *ToolHandlers) HandleDeleteVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dryRun := parseDryRun(req, false)
	if !dryRun && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (tally_delete_voucher is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	masterID, err := req.RequireString("master_id")
	if err != nil || strings.TrimSpace(masterID) == "" {
		return mcp.NewToolResultError("Argument 'master_id' is required for tally_delete_voucher"), nil
	}

	res, err := h.tallyClient.DeleteVoucherForCompany(ctx, company, masterID, dryRun)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Voucher delete failed: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleAlterVoucher previews or alters an existing Tally voucher.
func (h *ToolHandlers) HandleAlterVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	preview := parseDryRun(req, true)
	if !preview && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (altering vouchers with preview=false is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var entries []tally.VoucherEntryInput
	if args := req.GetArguments(); args != nil {
		if rawVal, ok := args["entries"]; ok && rawVal != nil {
			entries, err = decodeJSONArg[[]tally.VoucherEntryInput](rawVal)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Invalid 'entries' format: %v", err)), nil
			}
		}
	}

	res, err := h.tallyClient.AlterVoucherForCompany(
		ctx,
		company,
		req.GetString("master_id", ""),
		req.GetString("voucher_number", ""),
		req.GetString("voucher_type", ""),
		req.GetString("date", ""),
		req.GetString("new_date", ""),
		req.GetString("reference", ""),
		req.GetString("party_ledger", ""),
		req.GetString("narration", ""),
		entries,
		preview,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Voucher alter failed: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleCancelVoucher previews or cancels an existing Tally voucher.
func (h *ToolHandlers) HandleCancelVoucher(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	preview := parseDryRun(req, true)
	if !preview && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (cancelling vouchers with preview=false is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res, err := h.tallyClient.CancelVoucherForCompany(
		ctx,
		company,
		req.GetString("master_id", ""),
		req.GetString("voucher_number", ""),
		req.GetString("voucher_type", ""),
		req.GetString("date", ""),
		req.GetString("narration", ""),
		preview,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Voucher cancel failed: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleCreateStockItem previews or creates a stock item master in Tally.
func (h *ToolHandlers) HandleCreateStockItem(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	preview := parseDryRun(req, true)
	if !preview && h.isReadOnly(ctx) {
		return mcp.NewToolResultError("Operation blocked: TallyMCP is running in read-only mode (creating stock items with preview=false is disabled)."), nil
	}
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res, err := h.tallyClient.CreateStockItemForCompany(
		ctx,
		company,
		req.GetString("name", ""),
		req.GetString("parent", ""),
		req.GetString("unit", ""),
		req.GetString("hsn_code", ""),
		req.GetFloat("gst_rate", 0),
		req.GetString("opening_qty", ""),
		req.GetString("opening_rate", ""),
		req.GetFloat("opening_value", 0),
		preview,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Stock item creation failed: %v", err)), nil
	}
	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetBankRecon returns reconciled vs unreconciled bank entries for a bank ledger.
func (h *ToolHandlers) HandleGetBankRecon(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	unreconciledOnly := getBoolDefault(req, "unreconciled_only", true)
	limit := req.GetInt("limit", 50)

	res, err := h.tallyClient.GetBankReconForCompany(
		ctx,
		company,
		req.GetString("bank_ledger", ""),
		req.GetString("from_date", ""),
		req.GetString("to_date", ""),
		unreconciledOnly,
		limit,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to generate bank reconciliation: %v", err)), nil
	}

	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetSalesAnalytics returns top parties and top stock items for Sales or Purchase vouchers.
func (h *ToolHandlers) HandleGetSalesAnalytics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	company, err := h.resolveCompany(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	topN := req.GetInt("top_n", 10)

	res, err := h.tallyClient.GetSalesAnalyticsForCompany(
		ctx,
		company,
		req.GetString("voucher_type", "Sales"),
		req.GetString("from_date", ""),
		req.GetString("to_date", ""),
		topN,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to generate sales analytics: %v", err)), nil
	}

	outStr, fmtErr := h.formatOutput(res, req.GetString("format", ""))
	if fmtErr != nil {
		return mcp.NewToolResultError(fmtErr.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleCheckUpdate queries GitHub Releases for updates.
func (h *ToolHandlers) HandleCheckUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	info, err := updater.CheckForUpdate(ctx, h.appVersion, h.githubRepo)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Update check failed: %v", err)), nil
	}

	outStr, err := h.formatOutput(info, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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

// HandleResourceStatus returns the Tally connection status as a text resource.
func (h *ToolHandlers) HandleResourceStatus(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	status, err := h.tallyClient.Ping(ctx)
	if err != nil {
		qLen, qCap := h.tallyClient.QueueStats()
		detectedExe, _ := h.tallyClient.FindExecutable("")
		resp := map[string]any{
			"connected":         false,
			"tally_url":         h.tallyClient.BaseURL(),
			"queue_length":      qLen,
			"queue_capacity":    qCap,
			"error":             err.Error(),
			"detected_exe_path": detectedExe,
			"can_start":         detectedExe != "",
			"hint":              "Tally ERP 9 / TallyPrime is not running. Use the 'tally_start' MCP tool to launch it.",
		}
		outStr, _ := h.formatOutput(resp, "")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     outStr,
			},
		}, nil
	}

	outStr, _ := h.formatOutput(status, "")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     outStr,
		},
	}, nil
}

// HandleResourceCompanies returns the list of loaded companies as a text resource.
func (h *ToolHandlers) HandleResourceCompanies(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	companies, err := h.tallyClient.ListCompanies(ctx)
	if err != nil {
		outStr, _ := h.formatOutput(map[string]any{
			"error": fmt.Sprintf("Failed to list companies: %v", err),
			"hint":  "Ensure Tally is running with an active company opened.",
		}, "")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     outStr,
			},
		}, nil
	}

	outStr, _ := h.formatOutput(companies, "")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     outStr,
		},
	}, nil
}

// HandleResourceContext returns a single-read context snapshot (connectivity, active company, companies, current FY dates, today, read_only).
func (h *ToolHandlers) HandleResourceContext(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	now := time.Now()
	payload := map[string]any{
		"today":            now.Format("20060102"),
		"current_fy_start": tally.CurrentFinancialYearStart(now),
		"current_fy_end":   tally.CurrentFinancialYearEnd(now),
		"read_only":        h.readOnly,
	}

	status, err := h.tallyClient.Ping(ctx)
	if err != nil {
		payload["connected"] = false
		payload["error"] = err.Error()
		payload["hint"] = "Tally is not running. Use 'tally_start' to launch it."
	} else {
		payload["connected"] = status.Connected
		if len(status.Companies) > 0 {
			payload["active_company"] = status.Companies[0]
			payload["companies"] = status.Companies
		}
	}

	outStr, _ := h.formatOutput(payload, "")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     outStr,
		},
	}, nil
}

// HandleResourceFinancialSummary returns the executive financial KPI summary as a text resource.
func (h *ToolHandlers) HandleResourceFinancialSummary(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	summary, err := h.tallyClient.GetFinancialSummary(ctx, "", "")
	if err != nil {
		outStr, _ := h.formatOutput(map[string]any{
			"error": fmt.Sprintf("Failed to generate financial summary: %v", err),
			"hint":  "Ensure Tally is connected and a company is loaded.",
		}, "")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     outStr,
			},
		}, nil
	}

	outStr, _ := h.formatOutput(summary, "")
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     outStr,
		},
	}, nil
}

// HandlePromptAccountingRules returns domain guidance on Indian GAAP, Tally terminology, and TOON format.
func (h *ToolHandlers) HandlePromptAccountingRules(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	promptText := `# Tally & Accounting Domain Knowledge for AI Agents

You are connected to an active Tally ERP 9 / TallyPrime accounting system via TallyMCP. Follow these key conventions:

## 1. Accounting Terminology (Indian GAAP & Tally)
- **Sundry Debtors**: Customers / Accounts Receivable. Parties that owe money to the business for sales.
- **Sundry Creditors**: Vendors / Suppliers / Accounts Payable. Parties that the business owes money to for purchases/expenses.
- **Voucher Types**:
  - Sales: Customer sales invoices (generates accounts receivable).
  - Purchase: Vendor bills / supplier invoices (generates accounts payable).
  - Receipt: Money received from customers into Bank/Cash accounts.
  - Payment: Money paid to vendors/suppliers out of Bank/Cash accounts.
  - Contra: Internal money movements between bank accounts or cash-in-hand (e.g. cash deposit/withdrawal, bank-to-bank transfer).
  - Journal: Non-cash adjustments, depreciation, and opening balances.
- **Debit vs Credit Balances**:
  - Assets & Expenses have normal DEBIT balances (positive in standard reports).
  - Liabilities, Capital & Revenue have normal CREDIT balances.
- **Financial Year (FY)**:
  - In India, the standard Financial Year runs from April 1 to March 31 (e.g., FY 2026-27 is 2026-04-01 to 2027-03-31).

## 2. Response Format: TOON (Token-Oriented Object Notation)
- All query responses from TallyMCP are formatted in TOON to save 40% to 60% of LLM prompt tokens.
- Arrays use tabular declarations with a single schema header:
  collection[N]{col1,col2,...}: followed by comma-separated records on indented lines.
- Values with commas or special characters are safely double-quoted.
- Read tabular lines by mapping comma-separated fields directly to the header column names.

## 3. Execution Priorities: Error-Free, Bug-Free & Token-Efficient
- **Prioritize accuracy & zero retries**: Never guess ledger names, dates, or balances. Rely on TallyMCP's fuzzy ledger resolution ('ResolveLedgerName'), relative date keywords ('today', 'this_month', 'last_month', 'this_fy', 'last_fy', 'q1'–'q4'), and 'tally://context'.
- **1-Call Composite Tools First (Minimum Turns & Tokens)**:
  - Executive KPIs -> tally_get_financial_summary (supports compare_to)
  - Customer/Vendor 360 -> tally_get_party_overview
  - Batch balances -> tally_get_ledger(names="A,B,C")
  - GST compliance -> tally_get_gst_summary
  - Bank reconciliation -> tally_get_bank_recon
  - Sales/Purchase rankings -> tally_get_sales_analytics
  - Counts/Totals -> tally_list_vouchers(aggregate_only=true)
- **Safe Write Workflow**: Always call tally_create_voucher with preview=true (default) first to verify resolved ledgers, bill allocations, inventory lines, Dr=Cr balance, and duplicate checks before posting with preview=false.
- **Token Discipline**: Keep summary_only: true, use fields='...' projection when only specific columns are needed, and pass bounded from_date/to_date and limit so responses stay well under the 16 KB output cap.`

	return &mcp.GetPromptResult{
		Description: "Essential accounting rules, Indian GAAP & Tally terminology (Sundry Debtors, Creditors, Contra, FY), and TOON format guide for AI agents.",
		Messages: []mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(promptText)),
		},
	}, nil
}

// HandlePromptFinancialHealthAudit provides a structured workflow for auditing financial performance and credit risks.
func (h *ToolHandlers) HandlePromptFinancialHealthAudit(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	asOnDate := ""
	if req.Params.Arguments != nil {
		asOnDate = req.Params.Arguments["as_on_date"]
	}

	dateClause := ""
	if asOnDate != "" {
		dateClause = fmt.Sprintf(" (as on date %s)", asOnDate)
	}

	promptText := fmt.Sprintf(`# Comprehensive Financial Health & Cash Flow Audit Workflow%s

Follow this systematic 5-step workflow to assess company performance, liquidity, and risk:

1. **Verify Connection**:
   - Call 'tally_status' to ensure Tally is open and verify the currently active company.

2. **Executive KPI Overview**:
   - Call 'tally_get_financial_summary' to review:
     - Cash & Bank liquidity
     - Total Accounts Receivable vs Total Accounts Payable
     - Net Profit & Revenue YTD
     - Top 5 Debtors and Top 5 Creditors

3. **Aging Receivables (Credit Risk)**:
   - Call 'tally_get_outstandings' (type: 'receivable') to inspect overdue bills from customers. Identify high-risk clients exceeding 30/60/90 days credit periods.

4. **Upcoming Payables (Liquidity Risk)**:
   - Call 'tally_get_outstandings' (type: 'payable') to review upcoming payment obligations to suppliers and avoid payment defaults.

5. **Profitability & Cost Structure**:
   - Call 'tally_get_report' (report_type: 'profit_and_loss') to analyze gross margins, indirect expense ratios, and net profitability.
   - Present a clear executive briefing with strengths, cash risks, and recommended collection priorities.`, dateClause)

	return &mcp.GetPromptResult{
		Description: "Step-by-step workflow for auditing company financial health, cash flow, receivables aging, and profitability.",
		Messages: []mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(promptText)),
		},
	}, nil
}

func extractLedgerParentAndOpening(m map[string]any, ledgerName string) (parent string, openingBalance float64) {
	var matchedLedger map[string]any

	var search func(curr any)
	search = func(curr any) {
		if matchedLedger != nil {
			return
		}
		switch val := curr.(type) {
		case map[string]any:
			nameVal := ""
			for _, k := range []string{"@NAME", "NAME", "name", "@name"} {
				if n, ok := val[k].(string); ok {
					nameVal = n
					break
				}
			}
			if nameVal != "" && strings.EqualFold(strings.TrimSpace(nameVal), strings.TrimSpace(ledgerName)) {
				matchedLedger = val
				return
			}
			for _, v := range val {
				search(v)
			}
		case []any:
			for _, item := range val {
				search(item)
			}
		}
	}

	search(m)

	targetMap := matchedLedger
	if targetMap == nil {
		targetMap = m
	}

	if p, ok := findInMap(targetMap, "PARENT"); ok {
		if s, ok := p.(string); ok {
			parent = s
		}
	}
	if ob, ok := findInMap(targetMap, "OPENINGBALANCE"); ok {
		openingBalance = parseBalance(ob)
	}
	return parent, openingBalance
}

func parseBalance(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0
		}
		isDr := strings.Contains(strings.ToLower(s), "dr")
		s = strings.TrimSuffix(strings.TrimSuffix(s, " Dr"), " Cr")
		s = strings.TrimSuffix(strings.TrimSuffix(s, " dr"), " cr")
		s = strings.TrimSpace(s)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		if isDr && f > 0 {
			return -f
		}
		return f
	default:
		return 0
	}
}

func findInMap(m map[string]any, targetKey string) (any, bool) {
	for k, v := range m {
		if strings.EqualFold(k, targetKey) {
			return v, true
		}
		if subMap, ok := v.(map[string]any); ok {
			if res, found := findInMap(subMap, targetKey); found {
				return res, true
			}
		}
		if slice, ok := v.([]any); ok {
			for _, item := range slice {
				if itemMap, ok := item.(map[string]any); ok {
					if res, found := findInMap(itemMap, targetKey); found {
						return res, true
					}
				}
			}
		}
	}
	return nil, false
}
