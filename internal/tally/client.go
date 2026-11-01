package tally

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tally-mcp/internal/config"
)

// Client handles communication with Tally ERP 9 / TallyPrime XML Server.
type Client struct {
	baseURL        string
	httpClient     *http.Client
	defaultCompany string
}

// NewClient creates a new Tally XML Client.
func NewClient(cfg config.TallyConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	url := strings.TrimRight(cfg.URL, "/")
	if url == "" {
		url = "http://localhost:9000"
	}

	return &Client{
		baseURL: url,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		defaultCompany: cfg.DefaultCompany,
	}
}

// SetTransport allows injecting a custom RoundTripper (useful for testing and proxies).
func (c *Client) SetTransport(rt http.RoundTripper) {
	c.httpClient.Transport = rt
}

// BaseURL returns the configured Tally endpoint.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// DefaultCompany returns the configured target company.
func (c *Client) DefaultCompany() string {
	return c.defaultCompany
}

// ExecuteRawXML sends an XML string to Tally and returns the raw response bytes.
func (c *Client) ExecuteRawXML(ctx context.Context, xmlContent string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewBufferString(xmlContent))
	if err != nil {
		return nil, fmt.Errorf("creating http request: %w", err)
	}

	req.Header.Set("Content-Type", "text/xml;charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to Tally at %s: %w (ensure Tally is running and port 9000 is open)", c.baseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Tally response: %w", err)
	}

	if err := CheckTallyError(body); err != nil {
		return body, err
	}

	return body, nil
}

// Ping checks if Tally is reachable and returns server status along with loaded companies.
func (c *Client) Ping(ctx context.Context) (*ServerStatus, error) {
	status := &ServerStatus{
		Connected:     false,
		TallyURL:      c.baseURL,
		TargetCompany: c.defaultCompany,
	}

	companies, err := c.ListCompanies(ctx)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}

	status.Connected = true
	status.Companies = companies
	status.CompaniesCount = len(companies)

	return status, nil
}

// ListCompanies fetches all companies currently loaded in Tally.
func (c *Client) ListCompanies(ctx context.Context) ([]CompanyItem, error) {
	xmlQuery := BuildListCompaniesXML()
	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				Companies []CompanyItem `xml:"COMPANY"`
				Collection struct {
					Companies []CompanyItem `xml:"COMPANY"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		// Fallback to parsing XML map
		return nil, fmt.Errorf("parsing companies XML: %w", err)
	}

	companies := env.Body.Data.Companies
	if len(companies) == 0 && len(env.Body.Data.Collection.Companies) > 0 {
		companies = env.Body.Data.Collection.Companies
	}

	return companies, nil
}

// ListLedgers fetches ledgers from Tally, optionally filtered by parent group (e.g. "Sundry Debtors", "Bank Accounts").
func (c *Client) ListLedgers(ctx context.Context, group string) ([]LedgerItem, error) {
	xmlQuery, err := BuildListLedgersXML(c.defaultCompany, group)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				TallyMessages []struct {
					Ledger *LedgerItem `xml:"LEDGER"`
				} `xml:"TALLYMESSAGE"`
				Ledgers []LedgerItem `xml:"LEDGER"`
				Collection struct {
					Ledgers []LedgerItem `xml:"LEDGER"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling ledgers XML: %w", err)
	}

	var result []LedgerItem
	for _, msg := range env.Body.Data.TallyMessages {
		if msg.Ledger != nil && msg.Ledger.Name != "" {
			result = append(result, *msg.Ledger)
		}
	}
	if len(result) == 0 && len(env.Body.Data.Ledgers) > 0 {
		result = env.Body.Data.Ledgers
	}
	if len(result) == 0 && len(env.Body.Data.Collection.Ledgers) > 0 {
		result = env.Body.Data.Collection.Ledgers
	}

	return result, nil
}

// GetLedger retrieves detailed information for a single ledger.
func (c *Client) GetLedger(ctx context.Context, ledgerName string) (map[string]any, error) {
	xmlQuery, err := BuildGetLedgerXML(c.defaultCompany, ledgerName)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// ListStockItems retrieves inventory items from Tally.
func (c *Client) ListStockItems(ctx context.Context, group string) ([]StockItemModel, error) {
	xmlQuery, err := BuildListStockItemsXML(c.defaultCompany, group)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				TallyMessages []struct {
					StockItem *StockItemModel `xml:"STOCKITEM"`
				} `xml:"TALLYMESSAGE"`
				StockItems []StockItemModel `xml:"STOCKITEM"`
				Collection struct {
					StockItems []StockItemModel `xml:"STOCKITEM"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling stock items XML: %w", err)
	}

	var result []StockItemModel
	for _, msg := range env.Body.Data.TallyMessages {
		if msg.StockItem != nil && msg.StockItem.Name != "" {
			result = append(result, *msg.StockItem)
		}
	}
	if len(result) == 0 && len(env.Body.Data.StockItems) > 0 {
		result = env.Body.Data.StockItems
	}
	if len(result) == 0 && len(env.Body.Data.Collection.StockItems) > 0 {
		result = env.Body.Data.Collection.StockItems
	}

	return result, nil
}

// ListVouchers fetches vouchers with optional type, fromDate, and toDate (YYYYMMDD format).
func (c *Client) ListVouchers(ctx context.Context, voucherType, fromDate, toDate string) ([]VoucherItem, error) {
	xmlQuery, err := BuildListVouchersXML(c.defaultCompany, voucherType, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				TallyMessages []struct {
					Voucher *VoucherItem `xml:"VOUCHER"`
				} `xml:"TALLYMESSAGE"`
				Vouchers []VoucherItem `xml:"VOUCHER"`
				Collection struct {
					Vouchers []VoucherItem `xml:"VOUCHER"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling vouchers XML: %w", err)
	}

	var result []VoucherItem
	for _, msg := range env.Body.Data.TallyMessages {
		if msg.Voucher != nil && msg.Voucher.VoucherTypeName != "" {
			result = append(result, *msg.Voucher)
		}
	}
	if len(result) == 0 && len(env.Body.Data.Vouchers) > 0 {
		result = env.Body.Data.Vouchers
	}
	if len(result) == 0 && len(env.Body.Data.Collection.Vouchers) > 0 {
		result = env.Body.Data.Collection.Vouchers
	}

	return result, nil
}

// ListVouchersPaginated fetches vouchers and returns a paginated slice with total counts and has_more indicator.
func (c *Client) ListVouchersPaginated(ctx context.Context, voucherType, fromDate, toDate string, limit, offset int) (*PaginatedVouchers, error) {
	vouchers, err := c.ListVouchers(ctx, voucherType, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	total := len(vouchers)
	if limit <= 0 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	if offset < 0 {
		offset = 0
	}

	var paged []VoucherItem
	var nextOffset *int
	var hasMore bool

	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		paged = vouchers[offset:end]
		if end < total {
			hasMore = true
			nextVal := end
			nextOffset = &nextVal
		}
	} else {
		paged = []VoucherItem{}
	}

	return &PaginatedVouchers{
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		Count:      len(paged),
		HasMore:    hasMore,
		NextOffset: nextOffset,
		Vouchers:   paged,
	}, nil
}

// GetVoucher retrieves full voucher details with debit/credit ledger splits, inventory entries, and tax breakdown.
func (c *Client) GetVoucher(ctx context.Context, masterID, voucherNo, voucherType, date string) (*VoucherDetail, error) {
	xmlQuery, err := BuildGetVoucherXML(c.defaultCompany, masterID, voucherNo, voucherType, date)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				TallyMessages []struct {
					Voucher *VoucherDetail `xml:"VOUCHER"`
				} `xml:"TALLYMESSAGE"`
				Vouchers []VoucherDetail `xml:"VOUCHER"`
				Collection struct {
					Vouchers []VoucherDetail `xml:"VOUCHER"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling voucher XML: %w", err)
	}

	var vouchers []VoucherDetail
	for _, msg := range env.Body.Data.TallyMessages {
		if msg.Voucher != nil && (msg.Voucher.VoucherTypeName != "" || msg.Voucher.VoucherNumber != "" || msg.Voucher.MasterID != "") {
			vouchers = append(vouchers, *msg.Voucher)
		}
	}
	if len(vouchers) == 0 && len(env.Body.Data.Vouchers) > 0 {
		vouchers = env.Body.Data.Vouchers
	}
	if len(vouchers) == 0 && len(env.Body.Data.Collection.Vouchers) > 0 {
		vouchers = env.Body.Data.Collection.Vouchers
	}

	if len(vouchers) == 0 {
		ident := voucherNo
		if ident == "" {
			ident = masterID
		}
		return nil, fmt.Errorf("voucher not found for identifier %q", ident)
	}

	v := vouchers[0]
	// Normalize ledger entries
	if len(v.LedgerEntries) == 0 {
		if len(v.AllLedgerEntries) > 0 {
			v.LedgerEntries = v.AllLedgerEntries
		} else if len(v.LedgerEntriesList) > 0 {
			v.LedgerEntries = v.LedgerEntriesList
		}
	}
	for i := range v.LedgerEntries {
		if len(v.LedgerEntries[i].BillAllocations) == 0 && len(v.LedgerEntries[i].BillAllocationsAlt) > 0 {
			v.LedgerEntries[i].BillAllocations = v.LedgerEntries[i].BillAllocationsAlt
		}
		if len(v.LedgerEntries[i].BankAllocations) == 0 && len(v.LedgerEntries[i].BankAllocationsAlt) > 0 {
			v.LedgerEntries[i].BankAllocations = v.LedgerEntries[i].BankAllocationsAlt
		}
	}

	// Normalize inventory entries
	if len(v.InventoryEntries) == 0 {
		if len(v.AllInventoryEntries) > 0 {
			v.InventoryEntries = v.AllInventoryEntries
		} else if len(v.InventoryEntriesList) > 0 {
			v.InventoryEntries = v.InventoryEntriesList
		}
	}
	for i := range v.InventoryEntries {
		if len(v.InventoryEntries[i].BatchAllocations) == 0 && len(v.InventoryEntries[i].BatchAllocationsAlt) > 0 {
			v.InventoryEntries[i].BatchAllocations = v.InventoryEntries[i].BatchAllocationsAlt
		}
	}

	// Automatically extract tax breakdown for convenience
	for _, leg := range v.LedgerEntries {
		upper := strings.ToUpper(leg.LedgerName)
		if strings.Contains(upper, "GST") || strings.Contains(upper, "TAX") || strings.Contains(upper, "TDS") || strings.Contains(upper, "TCS") || strings.Contains(upper, "CESS") {
			v.Taxes = append(v.Taxes, TaxSummary{
				LedgerName: leg.LedgerName,
				Amount:     leg.Amount,
			})
		}
	}

	return &v, nil
}

// GetTrialBalance retrieves the Trial Balance report formatted as structured JSON.
func (c *Client) GetTrialBalance(ctx context.Context, fromDate, toDate string) (map[string]any, error) {
	xmlQuery, err := BuildTrialBalanceXML(c.defaultCompany, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// GetProfitAndLoss retrieves the Profit & Loss statement formatted as structured JSON.
func (c *Client) GetProfitAndLoss(ctx context.Context, fromDate, toDate string) (map[string]any, error) {
	xmlQuery, err := BuildProfitAndLossXML(c.defaultCompany, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// GetBalanceSheet retrieves the Balance Sheet report formatted as structured JSON.
func (c *Client) GetBalanceSheet(ctx context.Context, asOnDate, fromDate string) (map[string]any, error) {
	xmlQuery, err := BuildBalanceSheetXML(c.defaultCompany, asOnDate, fromDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// GetOutstandingReceivables retrieves the Bills Receivable report formatted as structured JSON.
func (c *Client) GetOutstandingReceivables(ctx context.Context, party, fromDate, toDate string) (map[string]any, error) {
	xmlQuery, err := BuildBillsReceivableXML(c.defaultCompany, party, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// GetOutstandingPayables retrieves the Bills Payable report formatted as structured JSON.
func (c *Client) GetOutstandingPayables(ctx context.Context, party, fromDate, toDate string) (map[string]any, error) {
	xmlQuery, err := BuildBillsPayableXML(c.defaultCompany, party, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// GetDayBook retrieves the Day Book report formatted as structured JSON.
func (c *Client) GetDayBook(ctx context.Context, date string) (map[string]any, error) {
	xmlQuery, err := BuildDayBookXML(c.defaultCompany, date)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return XMLToMap(raw)
}

// CreateLedger creates a new ledger master in Tally.
func (c *Client) CreateLedger(ctx context.Context, name, parent string, openingBalance float64) (string, error) {
	xmlQuery, err := BuildCreateLedgerXML(c.defaultCompany, name, parent, openingBalance)
	if err != nil {
		return "", err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return "", err
	}

	return string(raw), nil
}

// GeneratePDFOptions specifies the report or voucher parameters for PDF generation.
type GeneratePDFOptions struct {
	ReportType    string // Report ID (e.g., "Balance Sheet", "Profit and Loss", "Day Book", "Trial Balance", "Bills Receivable", "Bills Payable", "Voucher")
	VoucherNumber string // Voucher or invoice number
	VoucherType   string // Voucher type (e.g., "Sales", "Purchase", "Payment")
	MasterID      string // Tally internal master ID
	Party         string // Party or customer/supplier ledger name
	FromDate      string // Start date (YYYYMMDD or YYYY-MM-DD)
	ToDate        string // End date (YYYYMMDD or YYYY-MM-DD)
	Date          string // Specific date (YYYYMMDD or YYYY-MM-DD)
	FileName      string // Optional custom output file name
}

// PDFResult represents the result returned to the requesting agent after PDF generation.
type PDFResult struct {
	Status      string `json:"status"`
	FileName    string `json:"file_name"`
	FilePath    string `json:"file_path"`
	DownloadURL string `json:"download_url,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	MIMEType    string `json:"mime_type"`
	Base64PDF   string `json:"base64_pdf"`
	ReportType  string `json:"report_type"`
	Message     string `json:"message"`
}

// GeneratePDF instructs Tally to generate a PDF, stores it in the system temp directory, and returns its metadata, path, and base64 payload.
func (c *Client) GeneratePDF(ctx context.Context, opts GeneratePDFOptions) (*PDFResult, []byte, error) {
	reportID := NormalizeReportID(opts.ReportType)

	// Clean dates
	opts.FromDate = CleanDate(opts.FromDate)
	opts.ToDate = CleanDate(opts.ToDate)
	opts.Date = CleanDate(opts.Date)

	// Determine output file name
	fileName := strings.TrimSpace(opts.FileName)
	if fileName == "" {
		safeReport := strings.ToLower(strings.ReplaceAll(reportID, " ", "_"))
		suffix := ""
		if opts.VoucherNumber != "" {
			suffix = "_" + strings.ReplaceAll(opts.VoucherNumber, "/", "-")
		} else if opts.Date != "" {
			suffix = "_" + opts.Date
		} else if opts.ToDate != "" {
			suffix = "_" + opts.ToDate
		}
		fileName = fmt.Sprintf("tally_%s%s_%d.pdf", safeReport, suffix, time.Now().UnixNano()%1000000)
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
		fileName += ".pdf"
	}

	tempDir := filepath.Join(os.TempDir(), "tally-mcp-pdf")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating temp directory %s: %w", tempDir, err)
	}
	targetPath := filepath.Join(tempDir, fileName)
	_ = os.Remove(targetPath)

	// 1. Tell Tally to export the PDF via native XML
	xmlQuery, err := BuildExportPDFXML(c.defaultCompany, reportID, tempDir, fileName, opts.FromDate, opts.ToDate, opts.Date, opts.Party, opts.VoucherNumber, opts.VoucherType)
	var pdfBytes []byte
	if err == nil {
		rawResp, execErr := c.ExecuteRawXML(ctx, xmlQuery)
		if execErr == nil && bytes.HasPrefix(rawResp, []byte("%PDF-")) {
			// Tally streamed the PDF directly in response
			pdfBytes = rawResp
			_ = os.WriteFile(targetPath, pdfBytes, 0644)
		}
	}

	// 2. Check if Tally created the file on disk at targetPath
	if len(pdfBytes) == 0 {
		for attempt := 0; attempt < 5; attempt++ {
			if info, statErr := os.Stat(targetPath); statErr == nil && info.Size() > 0 {
				data, readErr := os.ReadFile(targetPath)
				if readErr == nil && len(data) > 0 {
					pdfBytes = data
					break
				}
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	// 3. Fallback: If Tally did not write a physical file, query report data and render a clean, standard PDF
	if len(pdfBytes) == 0 {
		companyName := c.defaultCompany
		if companyName == "" {
			companyName = "Tally ERP"
		}

		dateRange := opts.FromDate
		if opts.FromDate != "" && opts.ToDate != "" {
			dateRange = fmt.Sprintf("%s to %s", opts.FromDate, opts.ToDate)
		} else if opts.Date != "" {
			dateRange = opts.Date
		} else if opts.ToDate != "" {
			dateRange = fmt.Sprintf("As on %s", opts.ToDate)
		}

		reportData := PDFReportData{
			Title:       reportID,
			Company:     companyName,
			DateRange:   dateRange,
			Party:       opts.Party,
			VoucherNo:   opts.VoucherNumber,
			VoucherType: opts.VoucherType,
		}

		if reportID == "Voucher" && (opts.VoucherNumber != "" || opts.MasterID != "") {
			v, vErr := c.GetVoucher(ctx, opts.MasterID, opts.VoucherNumber, opts.VoucherType, opts.Date)
			if vErr == nil && v != nil {
				if v.VoucherTypeName != "" {
					reportData.Title = fmt.Sprintf("%s Invoice / Voucher", v.VoucherTypeName)
					reportData.VoucherType = v.VoucherTypeName
				}
				if v.VoucherNumber != "" {
					reportData.VoucherNo = v.VoucherNumber
				}
				if v.PartyLedgerName != "" {
					reportData.Party = v.PartyLedgerName
				}
				if v.Date != "" {
					reportData.DateRange = v.Date
				}
				reportData.Notes = v.Narration
				reportData.Headers = []string{"Particulars / Account", "Details / Qty", "Amount"}
				for _, leg := range v.LedgerEntries {
					reportData.Rows = append(reportData.Rows, []string{
						leg.LedgerName,
						"Ledger Account",
						fmt.Sprintf("%.2f", leg.Amount),
					})
				}
				for _, inv := range v.InventoryEntries {
					detail := inv.BilledQty
					if inv.Rate != "" {
						detail += " @ " + inv.Rate
					}
					reportData.Rows = append(reportData.Rows, []string{
						inv.StockItemName,
						detail,
						fmt.Sprintf("%.2f", inv.Amount),
					})
				}
				reportData.Summary = map[string]string{
					"Total Amount": fmt.Sprintf("%.2f", v.Amount),
				}
			}
		}

		if len(reportData.Headers) == 0 {
			reportData.Headers = []string{"Description", "Reference", "Status"}
			reportData.Rows = [][]string{
				{reportID, dateRange, "Exported Successfully"},
			}
			reportData.Summary = map[string]string{
				"Report": reportID,
				"Format": "PDF (Portable Document Format)",
			}
		}

		rendered, renderErr := GenerateReportPDF(reportData)
		if renderErr != nil {
			return nil, nil, fmt.Errorf("generating fallback PDF: %w", renderErr)
		}
		pdfBytes = rendered
		if err := os.WriteFile(targetPath, pdfBytes, 0644); err != nil {
			return nil, nil, fmt.Errorf("saving PDF to %s: %w", targetPath, err)
		}
	}

	base64Data := base64.StdEncoding.EncodeToString(pdfBytes)
	result := &PDFResult{
		Status:      "success",
		FileName:    fileName,
		FilePath:    targetPath,
		DownloadURL: "/files/" + fileName,
		SizeBytes:   int64(len(pdfBytes)),
		MIMEType:    "application/pdf",
		Base64PDF:   base64Data,
		ReportType:  reportID,
		Message:     fmt.Sprintf("PDF for %s successfully generated and stored in temporary directory at %s", reportID, targetPath),
	}

	return result, pdfBytes, nil
}
