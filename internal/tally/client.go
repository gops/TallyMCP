package tally

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
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

	return result, nil
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
