package mcp

import (
	"context"
	"fmt"
	"math"
	"sort"
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

// formatOutput serializes data into the requested format ("toon" or "json").
func (h *ToolHandlers) formatOutput(v any, reqFormat string) (string, error) {
	fmtChoice := strings.ToLower(strings.TrimSpace(reqFormat))
	if fmtChoice == "" {
		fmtChoice = strings.ToLower(strings.TrimSpace(h.defaultFormat))
	}
	if fmtChoice == "json" || fmtChoice == "compact_json" {
		return tally.CompactJSON(v)
	}
	if fmtChoice == "pretty_json" {
		return tally.PrettyJSON(v)
	}
	return toon.Marshal(v)
}

// SetBaseURL configures the base URL for generating absolute download links.
func (h *ToolHandlers) SetBaseURL(baseURL string) {
	h.baseURL = baseURL
}

// HandleTallyStatus checks connectivity to Tally ERP.
func (h *ToolHandlers) HandleTallyStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	group := req.GetString("group", "")
	search := req.GetString("search", "")
	limit := req.GetInt("limit", 50)
	offset := req.GetInt("offset", 0)
	summaryOnly := req.GetBool("summary_only", false)
	if !summaryOnly {
		summaryOnly = req.GetBool("compact", false)
	}

	paginated, err := h.tallyClient.FilterLedgers(ctx, group, search, limit, offset, summaryOnly)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch ledgers: %v", err)), nil
	}

	outStr, err := h.formatOutput(paginated, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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

	outStr, err := h.formatOutput(details, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetLedgerStatement retrieves a detailed transaction statement for a specific ledger account.
func (h *ToolHandlers) HandleGetLedgerStatement(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ledgerName, err := req.RequireString("ledger_name")
	if err != nil {
		return mcp.NewToolResultError("Ledger argument 'ledger_name' is required"), nil
	}
	ledgerName = strings.TrimSpace(ledgerName)
	if ledgerName == "" {
		return mcp.NewToolResultError("Ledger argument 'ledger_name' cannot be empty"), nil
	}

	fromDate := req.GetString("from_date", "")
	if fromDate == "" {
		fromDate = tally.CurrentFinancialYearStart(time.Now())
	} else {
		fromDate = tally.CleanDate(fromDate)
	}

	toDate := req.GetString("to_date", "")
	if toDate == "" {
		toDate = tally.CurrentFinancialYearEnd(time.Now())
	} else {
		toDate = tally.CleanDate(toDate)
	}

	// 1. Fetch ledger details (opening balance, parent)
	var parent string
	var openingBalance float64

	ledgerDetails, err := h.tallyClient.GetLedger(ctx, ledgerName)
	if err == nil && ledgerDetails != nil {
		parent, openingBalance = extractLedgerParentAndOpening(ledgerDetails, ledgerName)
	}

	// 2. Fetch vouchers matching ledger
	vouchers, err := h.tallyClient.GetLedgerStatementVouchers(ctx, ledgerName, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch vouchers for ledger %q: %v", ledgerName, err)), nil
	}

	entries := make([]tally.LedgerStatementEntry, 0)
	var totalDebit, totalCredit float64

	isTarget := func(name string) bool {
		return strings.EqualFold(strings.TrimSpace(name), ledgerName)
	}

	for _, v := range vouchers {
		var matchedAmount float64
		var matched bool
		var contraLedgers []string

		for _, e := range v.LedgerEntries {
			if isTarget(e.LedgerName) {
				matchedAmount += e.Amount
				matched = true
			} else if strings.TrimSpace(e.LedgerName) != "" {
				contraLedgers = append(contraLedgers, strings.TrimSpace(e.LedgerName))
			}
		}

		if !matched {
			if isTarget(v.PartyLedgerName) {
				matchedAmount = v.Amount
				matched = true
			}
		}

		if !matched {
			continue
		}

		if len(contraLedgers) == 0 && !isTarget(v.PartyLedgerName) && strings.TrimSpace(v.PartyLedgerName) != "" {
			contraLedgers = append(contraLedgers, strings.TrimSpace(v.PartyLedgerName))
		}
		seenContra := make(map[string]bool)
		var uniqueContras []string
		for _, c := range contraLedgers {
			if !seenContra[c] {
				seenContra[c] = true
				uniqueContras = append(uniqueContras, c)
			}
		}
		contraStr := strings.Join(uniqueContras, ", ")

		var debit, credit float64
		if matchedAmount < 0 {
			debit = math.Abs(matchedAmount)
		} else {
			credit = matchedAmount
		}

		totalDebit += debit
		totalCredit += credit

		dateStr := v.Date
		if len(dateStr) == 8 && !strings.Contains(dateStr, "-") {
			dateStr = fmt.Sprintf("%s-%s-%s", dateStr[0:4], dateStr[4:6], dateStr[6:8])
		}

		entries = append(entries, tally.LedgerStatementEntry{
			Date:          dateStr,
			VoucherType:   v.VoucherTypeName,
			VoucherNumber: v.VoucherNumber,
			ContraLedger:  contraStr,
			Debit:         math.Round(debit*100) / 100,
			Credit:        math.Round(credit*100) / 100,
			Narration:     v.Narration,
		})
	}

	// Sort entries by Date ascending, then VoucherNumber
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date < entries[j].Date
		}
		return entries[i].VoucherNumber < entries[j].VoucherNumber
	})

	totalDebit = math.Round(totalDebit*100) / 100
	totalCredit = math.Round(totalCredit*100) / 100

	net := openingBalance + totalCredit - totalDebit
	closingBalance := math.Round(math.Abs(net)*100) / 100
	closingBalanceType := "Cr"
	if net < 0 {
		closingBalanceType = "Dr"
	}

	result := tally.LedgerStatementResult{
		LedgerName:         ledgerName,
		Parent:             parent,
		FromDate:           fromDate,
		ToDate:             toDate,
		OpeningBalance:     math.Round(math.Abs(openingBalance)*100) / 100,
		TotalDebit:         totalDebit,
		TotalCredit:        totalCredit,
		ClosingBalance:     closingBalance,
		ClosingBalanceType: closingBalanceType,
		EntriesCount:       len(entries),
		Entries:            entries,
	}

	outStr, err := h.formatOutput(result, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleListStockItems lists stock items from Tally inventory with optional group filter, search query, pagination, and compact mode.
func (h *ToolHandlers) HandleListStockItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	group := req.GetString("group", "")
	search := req.GetString("search", "")
	limit := req.GetInt("limit", 50)
	offset := req.GetInt("offset", 0)
	summaryOnly := req.GetBool("summary_only", false)
	if !summaryOnly {
		summaryOnly = req.GetBool("compact", false)
	}

	paginated, err := h.tallyClient.FilterStockItems(ctx, group, search, limit, offset, summaryOnly)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list stock items: %v", err)), nil
	}

	outStr, err := h.formatOutput(paginated, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleListVouchers queries accounting and inventory vouchers with TDL pushdown filters, search, and pagination.
func (h *ToolHandlers) HandleListVouchers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts := tally.VoucherFilterOptions{
		VoucherType: req.GetString("voucher_type", ""),
		FromDate:    req.GetString("from_date", ""),
		ToDate:      req.GetString("to_date", ""),
		Party:       req.GetString("party", ""),
		Ledger:      req.GetString("ledger", ""),
		Search:      req.GetString("search", ""),
		Limit:       req.GetInt("limit", 50),
		Offset:      req.GetInt("offset", 0),
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

	// Safe date defaulting to protect against unbounded multi-year table scans
	var defaultedFromDate string
	trimmedFromDate := strings.TrimSpace(opts.FromDate)
	if trimmedFromDate == "" {
		defaultedFromDate = tally.CurrentFinancialYearStart(time.Now())
		opts.FromDate = defaultedFromDate
	} else if strings.EqualFold(trimmedFromDate, "all") {
		opts.FromDate = ""
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

	paginated, err := h.tallyClient.FilterVouchersPaginated(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list vouchers: %v", err)), nil
	}

	if defaultedFromDate != "" {
		paginated.FromDate = defaultedFromDate
		paginated.Note = fmt.Sprintf("'from_date' was omitted; defaulted to current financial year start (%s) to protect query latency. Specify 'from_date' (e.g. '20200401' or 'all') to search historical records.", defaultedFromDate)
	} else if opts.FromDate != "" {
		paginated.FromDate = opts.FromDate
	}
	if opts.ToDate != "" {
		paginated.ToDate = opts.ToDate
	}

	outStr, err := h.formatOutput(paginated, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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

	outStr, err := h.formatOutput(detail, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetTrialBalance returns the Trial Balance.
func (h *ToolHandlers) HandleGetTrialBalance(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	tb, err := h.tallyClient.GetTrialBalance(ctx, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get trial balance: %v", err)), nil
	}

	outStr, err := h.formatOutput(tb, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetProfitAndLoss returns the Profit & Loss statement.
func (h *ToolHandlers) HandleGetProfitAndLoss(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fromDate := req.GetString("from_date", "")
	toDate := req.GetString("to_date", "")

	pl, err := h.tallyClient.GetProfitAndLoss(ctx, fromDate, toDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get profit and loss: %v", err)), nil
	}

	outStr, err := h.formatOutput(pl, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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

	outStr, err := h.formatOutput(bs, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetFinancialSummary returns an executive snapshot of key financial metrics (Cash/Bank, Receivables, Payables, Top Debtors/Creditors, Working Capital) in < 350 tokens.
func (h *ToolHandlers) HandleGetFinancialSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	asOnDate := req.GetString("as_on_date", "")
	if asOnDate == "" {
		asOnDate = req.GetString("to_date", "")
	}
	if asOnDate == "" {
		asOnDate = req.GetString("date", "")
	}
	fromDate := req.GetString("from_date", "")

	summary, err := h.tallyClient.GetFinancialSummary(ctx, asOnDate, fromDate)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to generate financial summary: %v", err)), nil
	}

	outStr, err := h.formatOutput(summary, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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

	outStr, err := h.formatOutput(recv, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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

	outStr, err := h.formatOutput(pay, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
}

// HandleGetDayBook returns all vouchers entered on a specific date.
func (h *ToolHandlers) HandleGetDayBook(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	date := req.GetString("date", "")
	if date == "" {
		date = req.GetString("for_date", "")
	}
	if date == "" {
		date = time.Now().Format("20060102")
	}

	db, err := h.tallyClient.GetDayBook(ctx, date)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get day book: %v", err)), nil
	}

	outStr, err := h.formatOutput(db, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(outStr), nil
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
	if date == "" {
		date = time.Now().Format("20060102")
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

	outStr, err := h.formatOutput(result, req.GetString("format", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Return tool result with embedded binary resource as well as structured TOON text
	return mcp.NewToolResultResource(outStr, mcp.BlobResourceContents{
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

	// Attempt to convert XML to map and format as TOON for optimal LLM consumption
	m, err := tally.XMLToMap(raw)
	if err == nil && len(m) > 0 {
		toonStr, err := toon.Marshal(m)
		if err == nil {
			return mcp.NewToolResultText(toonStr), nil
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

## 3. Best Practices for Calling Tools
- **Date Scoping**: For tally_list_vouchers, providing a recent from_date (e.g. current month or FY) significantly reduces query latency. If omitted, queries safely default to the current Financial Year start. To search historical years, explicitly specify from_date.
- **Finding Names**: If an exact customer or vendor name is unknown, use tally_list_ledgers with search='<substring>' first rather than guessing exact ledger names.
- **Compact Data**: Set summary_only: true on tally_list_ledgers and tally_get_stock_summary for ultra-compact token consumption unless complete postal/tax/contact records are required.
- **High-Level KPIs**: Call tally_get_financial_summary to answer executive questions about Cash, Bank, Receivables, Payables, and Net Profit in < 350 tokens.`

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
   - Call 'tally_get_outstanding_receivables' to inspect overdue bills from customers. Identify high-risk clients exceeding 30/60/90 days credit periods.

4. **Upcoming Payables (Liquidity Risk)**:
   - Call 'tally_get_outstanding_payables' to review upcoming payment obligations to suppliers and avoid payment defaults.

5. **Profitability & Cost Structure**:
   - Call 'tally_get_profit_and_loss' to analyze gross margins, indirect expense ratios, and net profitability.
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
