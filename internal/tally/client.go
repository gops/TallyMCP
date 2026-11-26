package tally

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"tally-mcp/internal/config"
)

var (
	reInvalidXMLCharRef = regexp.MustCompile(`&#(?:0*[0-8]|0*1[1-2]|0*1[4-9]|0*2[0-9]|0*3[0-1]);`)
	reInvalidHexCharRef = regexp.MustCompile(`(?i)&#x(?:0*[0-8]|0*[bB]|0*[cC]|0*[e-fE-F]|0*1[0-9a-fA-F]);`)
)

// SanitizeTallyResponseXML strips illegal XML 1.0 character entities and raw non-printable control bytes.
func SanitizeTallyResponseXML(data []byte) []byte {
	// 1. Remove illegal numeric entities (&#4;, &#x04;, etc.)
	data = reInvalidXMLCharRef.ReplaceAll(data, nil)
	data = reInvalidHexCharRef.ReplaceAll(data, nil)

	// 2. Strip raw ASCII control bytes except \t (0x09), \n (0x0A), and \r (0x0D)
	clean := make([]byte, 0, len(data))
	for _, b := range data {
		if b >= 0x20 || b == 0x09 || b == 0x0A || b == 0x0D {
			clean = append(clean, b)
		}
	}
	return clean
}

// Client handles communication with Tally ERP 9 / TallyPrime XML Server.
type Client struct {
	baseURL          string
	httpClient       *http.Client
	defaultCompany   string
	querySem         chan struct{} // Capacity 1: guarantees strictly 1 query executing at any time (no parallel queries to Tally)
	queueSem         chan struct{} // Capacity MaxQueueSize: buffers incoming queries before dropping/rejecting
	throttleDuration time.Duration // Cooldown sleep between consecutive queries
	cache            *MemoryCache  // In-memory TTL cache for instant lookup of static/master data
	flight           Singleflight  // Coalesces concurrent identical cache misses
	exePath          string        // Path to Tally executable
	autoStart        bool          // Automatically start Tally if not running
	launchMu         sync.Mutex    // Protects process start concurrency
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

	maxQueueSize := cfg.MaxQueueSize
	if maxQueueSize <= 0 {
		maxQueueSize = 100
	}

	throttleMs := cfg.ThrottleMs
	if cfg.DisableThrottle {
		throttleMs = 0
	} else if throttleMs <= 0 {
		throttleMs = 15
	}

	var cache *MemoryCache
	if !cfg.DisableCache {
		cacheTTL := time.Duration(cfg.CacheTTLSeconds) * time.Second
		if cacheTTL <= 0 {
			cacheTTL = 30 * time.Second
		}
		cache = NewMemoryCache(cacheTTL)
	}

	return &Client{
		baseURL: url,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		defaultCompany:   cfg.DefaultCompany,
		querySem:         make(chan struct{}, 1),
		queueSem:         make(chan struct{}, maxQueueSize),
		throttleDuration: time.Duration(throttleMs) * time.Millisecond,
		cache:            cache,
		exePath:          cfg.ExePath,
		autoStart:        cfg.AutoStart,
	}
}

// SetTransport allows injecting a custom RoundTripper (useful for testing and proxies).
func (c *Client) SetTransport(rt http.RoundTripper) {
	c.httpClient.Transport = rt
}

// Cache returns the internal MemoryCache instance.
func (c *Client) Cache() *MemoryCache {
	return c.cache
}

// InvalidateCache clears cached entries matching prefix (or all entries if prefix is empty).
func (c *Client) InvalidateCache(prefix string) {
	if c.cache != nil {
		c.cache.Invalidate(prefix)
	}
}

// BaseURL returns the configured Tally endpoint.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// DefaultCompany returns the configured target company.
func (c *Client) DefaultCompany() string {
	return c.defaultCompany
}

// QueueStats returns the current number of buffered requests and the maximum buffer capacity.
func (c *Client) QueueStats() (length, capacity int) {
	return len(c.queueSem), cap(c.queueSem)
}

// IsBusy returns true if a query is actively being processed by Tally.
func (c *Client) IsBusy() bool {
	return len(c.querySem) > 0
}

// ExecuteRawXML sends an XML string to Tally and returns the raw response bytes.
// It guarantees serial execution (no parallel queries) to prevent overloading Tally ERP / TallyPrime,
// buffering concurrent queries in a bounded queue up to MaxQueueSize.
func (c *Client) ExecuteRawXML(ctx context.Context, xmlContent string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Pre-flight Guard: Validate and sanitize XML before dispatching to Tally.
	// Catches malformed XML, unbalanced quotes, unescaped delimiters, and invalid TDL formulas
	// before they reach Tally's C++ core, preventing fatal "Can not understand, bad formula" crashes.
	safeXML, err := ValidateAndSanitizeTallyXML(xmlContent)
	if err != nil {
		return nil, fmt.Errorf("pre-flight XML guard: %w", err)
	}

	// Step 1: Request a slot in the incoming query buffer
	select {
	case c.queueSem <- struct{}{}:
		defer func() { <-c.queueSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, fmt.Errorf("tally query buffer is full (%d requests waiting); please try again shortly", cap(c.queueSem))
	}

	// Step 2: Acquire the single query execution slot (guarantees NO parallel queries reach Tally)
	select {
	case c.querySem <- struct{}{}:
		defer func() {
			if c.throttleDuration > 0 {
				time.Sleep(c.throttleDuration)
			}
			<-c.querySem
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Step 3: Execute the HTTP request against Tally
	body, err := c.doHTTP(ctx, safeXML)
	if err != nil && c.autoStart && isConnectionRefused(err) {
		startRes, startErr := c.EnsureRunning(ctx, 30*time.Second)
		if startErr == nil && startRes != nil && startRes.Connected {
			body, err = c.doHTTP(ctx, safeXML)
		}
	}
	if err == nil && IsMutatingXML(safeXML) {
		c.InvalidateCache("")
	}
	return body, err
}

func (c *Client) doHTTP(ctx context.Context, xmlContent string) ([]byte, error) {
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 256 {
			snippet = snippet[:256] + "..."
		}
		return nil, fmt.Errorf("tally HTTP %d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), snippet)
	}

	body = SanitizeTallyResponseXML(body)

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
		QueueLength:   len(c.queueSem),
		QueueCapacity: cap(c.queueSem),
	}

	companies, err := c.ListCompanies(ctx)
	if err != nil && c.autoStart && isConnectionRefused(err) {
		startRes, startErr := c.EnsureRunning(ctx, 30*time.Second)
		if startErr == nil && startRes != nil && startRes.Connected {
			companies, err = c.ListCompanies(ctx)
		}
	}
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
	cacheKey := "companies:" + c.baseURL
	if c.cache != nil {
		if cached, ok := c.cache.Get(cacheKey); ok {
			if companies, ok := cached.([]CompanyItem); ok {
				return slices.Clone(companies), nil
			}
		}
	}

	val, err := c.flight.Do(ctx, cacheKey, func(fetchCtx context.Context) (any, error) {
		if c.cache != nil {
			if cached, ok := c.cache.Get(cacheKey); ok {
				if companies, ok := cached.([]CompanyItem); ok {
					return companies, nil
				}
			}
		}

		xmlQuery := BuildListCompaniesXML()
		raw, err := c.ExecuteRawXML(fetchCtx, xmlQuery)
		if err != nil {
			return nil, err
		}

		var env struct {
			XMLName xml.Name `xml:"ENVELOPE"`
			Body    struct {
				Data struct {
					Companies  []CompanyItem `xml:"COMPANY"`
					Collection struct {
						Companies []CompanyItem `xml:"COMPANY"`
					} `xml:"COLLECTION"`
				} `xml:"DATA"`
			} `xml:"BODY"`
		}

		if err := xml.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("parsing companies XML: %w", err)
		}

		companies := env.Body.Data.Companies
		if len(companies) == 0 && len(env.Body.Data.Collection.Companies) > 0 {
			companies = env.Body.Data.Collection.Companies
		}

		if c.cache != nil && len(companies) > 0 {
			c.cache.Set(cacheKey, companies)
		}
		return companies, nil
	})
	if err != nil {
		return nil, err
	}
	companies, _ := val.([]CompanyItem)
	return slices.Clone(companies), nil
}

// ListLedgers fetches ledgers from Tally, optionally filtered by parent group (e.g. "Sundry Debtors", "Bank Accounts").
func (c *Client) ListLedgers(ctx context.Context, group string) ([]LedgerItem, error) {
	return c.ListLedgersForCompany(ctx, c.defaultCompany, group)
}

// ListLedgersForCompany fetches ledgers for a specific company, optionally filtered by parent group.
func (c *Client) ListLedgersForCompany(ctx context.Context, company, group string) ([]LedgerItem, error) {
	return c.ListLedgersWithDates(ctx, company, group, "", "")
}

// ListLedgersWithDates fetches ledgers for a company with optional group and date scope.
func (c *Client) ListLedgersWithDates(ctx context.Context, company, group, fromDate, toDate string) ([]LedgerItem, error) {
	group = NormalizeGroupAlias(group)
	cleanFrom := CleanDate(fromDate)
	cleanTo := CleanDate(toDate)
	cacheKey := fmt.Sprintf("ledgers:%s:%s:%s:%s", company, strings.ToLower(strings.TrimSpace(group)), cleanFrom, cleanTo)
	if c.cache != nil {
		if cached, ok := c.cache.Get(cacheKey); ok {
			if ledgers, ok := cached.([]LedgerItem); ok {
				return slices.Clone(ledgers), nil
			}
		}
	}

	val, err := c.flight.Do(ctx, cacheKey, func(fetchCtx context.Context) (any, error) {
		if c.cache != nil {
			if cached, ok := c.cache.Get(cacheKey); ok {
				if ledgers, ok := cached.([]LedgerItem); ok {
					return ledgers, nil
				}
			}
		}

		xmlQuery, err := BuildListLedgersXMLWithDates(company, group, cleanFrom, cleanTo)
		if err != nil {
			return nil, err
		}

		raw, err := c.ExecuteRawXML(fetchCtx, xmlQuery)
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
					Ledgers    []LedgerItem `xml:"LEDGER"`
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

		for i := range result {
			if result[i].Parent == "" && result[i].ParentAttr != "" {
				result[i].Parent = result[i].ParentAttr
			}
			result[i].OpeningBalance = round2(result[i].OpeningBalance)
			result[i].ClosingBalance = round2(result[i].ClosingBalance)
		}

		if c.cache != nil && len(result) > 0 {
			c.cache.Set(cacheKey, result)
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	ledgers, _ := val.([]LedgerItem)
	return slices.Clone(ledgers), nil
}

// ListGroupsForCompany fetches account groups for resolving sub-group hierarchies.
func (c *Client) ListGroupsForCompany(ctx context.Context, company string) ([]GroupItem, error) {
	cacheKey := fmt.Sprintf("groups:%s", company)
	if c.cache != nil {
		if cached, ok := c.cache.Get(cacheKey); ok {
			if groups, ok := cached.([]GroupItem); ok {
				return slices.Clone(groups), nil
			}
		}
	}

	val, err := c.flight.Do(ctx, cacheKey, func(fetchCtx context.Context) (any, error) {
		xmlQuery, err := BuildListGroupsXML(company)
		if err != nil {
			return nil, err
		}
		raw, err := c.ExecuteRawXML(fetchCtx, xmlQuery)
		if err != nil {
			return nil, err
		}

		var env struct {
			XMLName xml.Name `xml:"ENVELOPE"`
			Body    struct {
				Data struct {
					TallyMessages []struct {
						Group *GroupItem `xml:"GROUP"`
					} `xml:"TALLYMESSAGE"`
					Groups     []GroupItem `xml:"GROUP"`
					Collection struct {
						Groups []GroupItem `xml:"GROUP"`
					} `xml:"COLLECTION"`
				} `xml:"DATA"`
			} `xml:"BODY"`
		}
		if err := xml.Unmarshal(raw, &env); err != nil {
			return nil, err
		}

		var groups []GroupItem
		for _, msg := range env.Body.Data.TallyMessages {
			if msg.Group != nil && msg.Group.Name != "" {
				groups = append(groups, *msg.Group)
			}
		}
		if len(groups) == 0 && len(env.Body.Data.Groups) > 0 {
			groups = env.Body.Data.Groups
		}
		if len(groups) == 0 && len(env.Body.Data.Collection.Groups) > 0 {
			groups = env.Body.Data.Collection.Groups
		}
		for i := range groups {
			if groups[i].Parent == "" && groups[i].ParentAttr != "" {
				groups[i].Parent = groups[i].ParentAttr
			}
		}
		if c.cache != nil && len(groups) > 0 {
			c.cache.Set(cacheKey, groups)
		}
		return groups, nil
	})
	if err != nil {
		return nil, err
	}
	groups, _ := val.([]GroupItem)
	return slices.Clone(groups), nil
}

// FilterLedgers queries ledgers, applies case-insensitive substring search across name/parent/state, and returns paginated ledgers with optional compact projection.
func (c *Client) FilterLedgers(ctx context.Context, group, search string, limit, offset int, summaryOnly bool) (*PaginatedLedgers, error) {
	return c.FilterLedgersForCompany(ctx, c.defaultCompany, group, search, limit, offset, summaryOnly)
}

// FilterLedgersForCompany queries ledgers for a specific company with search, pagination, and compact projection.
func (c *Client) FilterLedgersForCompany(ctx context.Context, company, group, search string, limit, offset int, summaryOnly bool) (*PaginatedLedgers, error) {
	return c.FilterLedgersSortedForCompany(ctx, company, group, search, "", limit, offset, summaryOnly)
}

// FilterLedgersSortedForCompany queries ledgers with optional sorting ("balance_desc", "balance_asc", "name_asc"), search, pagination, and compact projection.
func (c *Client) FilterLedgersSortedForCompany(ctx context.Context, company, group, search, sortMode string, limit, offset int, summaryOnly bool) (*PaginatedLedgers, error) {
	allLedgers, err := c.ListLedgersForCompany(ctx, company, group)
	if err != nil {
		return nil, err
	}

	search = strings.TrimSpace(strings.ToLower(search))
	var filtered []LedgerItem
	if search == "" {
		filtered = slices.Clone(allLedgers)
	} else {
		for _, l := range allLedgers {
			if strings.Contains(strings.ToLower(l.Name), search) ||
				strings.Contains(strings.ToLower(l.Parent), search) ||
				strings.Contains(strings.ToLower(l.State), search) {
				filtered = append(filtered, l)
			}
		}
	}

	switch strings.ToLower(strings.TrimSpace(sortMode)) {
	case "balance_desc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return math.Abs(filtered[i].ClosingBalance) > math.Abs(filtered[j].ClosingBalance)
		})
	case "balance_asc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return math.Abs(filtered[i].ClosingBalance) < math.Abs(filtered[j].ClosingBalance)
		})
	case "name_asc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
		})
	}

	total := len(filtered)
	if limit <= 0 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	if offset < 0 {
		offset = 0
	}

	var pagedSlice []LedgerItem
	var nextOffset *int
	var hasMore bool

	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		pagedSlice = filtered[offset:end]
		if end < total {
			hasMore = true
			nextVal := end
			nextOffset = &nextVal
		}
	} else {
		pagedSlice = []LedgerItem{}
	}

	var payload any
	if summaryOnly {
		compact := make([]CompactLedgerItem, len(pagedSlice))
		for i, l := range pagedSlice {
			compact[i] = CompactLedgerItem{
				Name:           l.Name,
				Parent:         l.Parent,
				ClosingBalance: round2(l.ClosingBalance),
				State:          l.State,
			}
		}
		payload = compact
	} else {
		payload = pagedSlice
	}

	return &PaginatedLedgers{
		Total:      total,
		Count:      len(pagedSlice),
		Offset:     offset,
		Limit:      limit,
		HasMore:    hasMore,
		NextOffset: nextOffset,
		Ledgers:    payload,
	}, nil
}

// GetLedgerDetailForCompany retrieves a clean, flat LedgerDetail struct without raw XML envelope boilerplate.
func (c *Client) GetLedgerDetailForCompany(ctx context.Context, company, ledgerName string) (*LedgerDetail, error) {
	xmlQuery, err := BuildGetLedgerXML(company, ledgerName)
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
					Ledger *LedgerDetail `xml:"LEDGER"`
				} `xml:"TALLYMESSAGE"`
				Ledgers    []LedgerDetail `xml:"LEDGER"`
				Collection struct {
					Ledgers []LedgerDetail `xml:"LEDGER"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling ledger XML: %w", err)
	}

	var candidates []LedgerDetail
	for _, msg := range env.Body.Data.TallyMessages {
		if msg.Ledger != nil && msg.Ledger.Name != "" {
			candidates = append(candidates, *msg.Ledger)
		}
	}
	if len(candidates) == 0 && len(env.Body.Data.Ledgers) > 0 {
		candidates = env.Body.Data.Ledgers
	}
	if len(candidates) == 0 && len(env.Body.Data.Collection.Ledgers) > 0 {
		candidates = env.Body.Data.Collection.Ledgers
	}
	if len(candidates) == 0 {
		return nil, &LedgerNotFoundError{Query: ledgerName}
	}

	var matched *LedgerDetail
	trimmedTarget := strings.TrimSpace(ledgerName)
	for i := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidates[i].Name), trimmedTarget) {
			matched = &candidates[i]
			break
		}
	}
	if matched == nil {
		if len(candidates) == 1 {
			matched = &candidates[0]
		} else {
			return nil, &LedgerNotFoundError{Query: ledgerName}
		}
	}

	if matched.Parent == "" && matched.ParentAttr != "" {
		matched.Parent = matched.ParentAttr
	}
	matched.OpeningBalance = round2(matched.OpeningBalance)
	matched.ClosingBalance = round2(matched.ClosingBalance)
	var cleanAddr []string
	for _, line := range matched.Address {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			cleanAddr = append(cleanAddr, trimmed)
		}
	}
	matched.Address = cleanAddr
	return matched, nil
}

// GetLedger retrieves detailed information for a single ledger.
func (c *Client) GetLedger(ctx context.Context, ledgerName string) (map[string]any, error) {
	return c.GetLedgerForCompany(ctx, c.defaultCompany, ledgerName)
}

// GetLedgerForCompany retrieves detailed information for a single ledger in a specific company.
func (c *Client) GetLedgerForCompany(ctx context.Context, company, ledgerName string) (map[string]any, error) {
	xmlQuery, err := BuildGetLedgerXML(company, ledgerName)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	m, err := XMLToMap(raw)
	if err != nil {
		return nil, err
	}

	// Detect empty ledger result so callers get a clear error instead of an empty envelope
	if !containsKeyFold(m, "LEDGER") {
		return nil, &LedgerNotFoundError{Query: ledgerName}
	}
	return m, nil
}

func containsKeyFold(v any, key string) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if strings.EqualFold(k, key) {
				return true
			}
			if containsKeyFold(val, key) {
				return true
			}
		}
	case []any:
		for _, item := range t {
			if containsKeyFold(item, key) {
				return true
			}
		}
	}
	return false
}

// ListStockItems retrieves inventory items from Tally.
func (c *Client) ListStockItems(ctx context.Context, group string) ([]StockItemModel, error) {
	return c.ListStockItemsForCompany(ctx, c.defaultCompany, group)
}

// ListStockItemsForCompany retrieves inventory items for a specific company.
func (c *Client) ListStockItemsForCompany(ctx context.Context, company, group string) ([]StockItemModel, error) {
	cacheKey := fmt.Sprintf("stock_items:%s:%s", company, strings.ToLower(strings.TrimSpace(group)))
	if c.cache != nil {
		if cached, ok := c.cache.Get(cacheKey); ok {
			if items, ok := cached.([]StockItemModel); ok {
				return slices.Clone(items), nil
			}
		}
	}

	val, err := c.flight.Do(ctx, cacheKey, func(fetchCtx context.Context) (any, error) {
		if c.cache != nil {
			if cached, ok := c.cache.Get(cacheKey); ok {
				if items, ok := cached.([]StockItemModel); ok {
					return items, nil
				}
			}
		}

		xmlQuery, err := BuildListStockItemsXML(company, group)
		if err != nil {
			return nil, err
		}

		raw, err := c.ExecuteRawXML(fetchCtx, xmlQuery)
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

		for i := range result {
			if result[i].Parent == "" && result[i].ParentAttr != "" {
				result[i].Parent = result[i].ParentAttr
			}
			result[i].OpeningValue = round2(result[i].OpeningValue)
			result[i].ClosingValue = round2(result[i].ClosingValue)
		}

		if c.cache != nil && len(result) > 0 {
			c.cache.Set(cacheKey, result)
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	items, _ := val.([]StockItemModel)
	return slices.Clone(items), nil
}

// FilterStockItems queries inventory items, applies case-insensitive substring search, and returns paginated items with optional compact projection.
func (c *Client) FilterStockItems(ctx context.Context, group, search string, limit, offset int, summaryOnly bool) (*PaginatedStockItems, error) {
	return c.FilterStockItemsForCompany(ctx, c.defaultCompany, group, search, limit, offset, summaryOnly)
}

// FilterStockItemsForCompany queries inventory items for a specific company.
func (c *Client) FilterStockItemsForCompany(ctx context.Context, company, group, search string, limit, offset int, summaryOnly bool) (*PaginatedStockItems, error) {
	allItems, err := c.ListStockItemsForCompany(ctx, company, group)
	if err != nil {
		return nil, err
	}

	search = strings.TrimSpace(strings.ToLower(search))
	var filtered []StockItemModel
	if search == "" {
		filtered = allItems
	} else {
		for _, item := range allItems {
			if strings.Contains(strings.ToLower(item.Name), search) ||
				strings.Contains(strings.ToLower(item.Parent), search) {
				filtered = append(filtered, item)
			}
		}
	}

	total := len(filtered)
	if limit <= 0 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	if offset < 0 {
		offset = 0
	}

	var pagedSlice []StockItemModel
	var nextOffset *int
	var hasMore bool

	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		pagedSlice = filtered[offset:end]
		if end < total {
			hasMore = true
			nextVal := end
			nextOffset = &nextVal
		}
	} else {
		pagedSlice = []StockItemModel{}
	}

	var payload any
	if summaryOnly {
		compact := make([]CompactStockItem, len(pagedSlice))
		for i, item := range pagedSlice {
			compact[i] = CompactStockItem{
				Name:           item.Name,
				Parent:         item.Parent,
				ClosingBalance: item.ClosingBalance,
				ClosingValue:   item.ClosingValue,
			}
		}
		payload = compact
	} else {
		payload = pagedSlice
	}

	return &PaginatedStockItems{
		Total:      total,
		Count:      len(pagedSlice),
		Offset:     offset,
		Limit:      limit,
		HasMore:    hasMore,
		NextOffset: nextOffset,
		StockItems: payload,
	}, nil
}

type rawLedgerEntryXML struct {
	LedgerName         string           `xml:"LEDGERNAME"`
	LedgerNameAttr     string           `xml:"LEDGERNAME,attr"`
	Amount             float64          `xml:"AMOUNT"`
	AmountAttr         float64          `xml:"AMOUNT,attr"`
	BankAllocations    []BankAllocation `xml:"BANKALLOCATIONS.LIST"`
	BankAllocationsAlt []BankAllocation `xml:"BANKALLOCATIONS"`
}

func (r rawLedgerEntryXML) toLedgerEntryRecord() LedgerEntryRecord {
	name := r.LedgerName
	if name == "" {
		name = r.LedgerNameAttr
	}
	amt := r.Amount
	if amt == 0 && r.AmountAttr != 0 {
		amt = r.AmountAttr
	}
	bankAllocs := r.BankAllocations
	if len(bankAllocs) == 0 && len(r.BankAllocationsAlt) > 0 {
		bankAllocs = r.BankAllocationsAlt
	}
	return LedgerEntryRecord{
		LedgerName:      strings.TrimSpace(name),
		Amount:          amt,
		BankAllocations: bankAllocs,
	}
}

type rawVoucherXML struct {
	MasterID            string              `xml:"MASTERID"`
	MasterIDAttr        string              `xml:"MASTERID,attr"`
	VoucherTypeName     string              `xml:"VOUCHERTYPENAME"`
	VoucherTypeNameAttr string              `xml:"VOUCHERTYPENAME,attr"`
	VoucherNumber       string              `xml:"VOUCHERNUMBER"`
	VoucherNumberAttr   string              `xml:"VOUCHERNUMBER,attr"`
	Date                string              `xml:"DATE"`
	DateAttr            string              `xml:"DATE,attr"`
	EffectiveDate       string              `xml:"EFFECTIVEDATE"`
	EffectiveDateAttr   string              `xml:"EFFECTIVEDATE,attr"`
	Reference           string              `xml:"REFERENCE"`
	ReferenceAttr       string              `xml:"REFERENCE,attr"`
	PartyLedgerName     string              `xml:"PARTYLEDGERNAME"`
	PartyLedgerNameAttr string              `xml:"PARTYLEDGERNAME,attr"`
	Amount              float64             `xml:"AMOUNT"`
	AmountAttr          float64             `xml:"AMOUNT,attr"`
	Narration           string              `xml:"NARRATION"`
	NarrationAttr       string              `xml:"NARRATION,attr"`
	AllLedgerEntries     []rawLedgerEntryXML     `xml:"ALLLEDGERENTRIES.LIST"`
	LedgerEntries        []rawLedgerEntryXML     `xml:"LEDGERENTRIES.LIST"`
	AllInventoryEntries  []VoucherInventoryEntry `xml:"ALLINVENTORYENTRIES.LIST"`
	InventoryEntriesList []VoucherInventoryEntry `xml:"INVENTORYENTRIES.LIST"`
}

func (r rawVoucherXML) toVoucherItem() VoucherItem {
	item := VoucherItem{
		MasterID:        r.MasterID,
		VoucherTypeName: r.VoucherTypeName,
		VoucherNumber:   r.VoucherNumber,
		Date:            r.Date,
		EffectiveDate:   r.EffectiveDate,
		Reference:       r.Reference,
		PartyLedgerName: r.PartyLedgerName,
		Amount:          r.Amount,
		Narration:       r.Narration,
	}
	if item.MasterID == "" {
		item.MasterID = r.MasterIDAttr
	}
	if item.VoucherTypeName == "" {
		item.VoucherTypeName = r.VoucherTypeNameAttr
	}
	if item.VoucherNumber == "" {
		item.VoucherNumber = r.VoucherNumberAttr
	}
	if item.Date == "" {
		item.Date = r.DateAttr
	}
	if item.EffectiveDate == "" {
		item.EffectiveDate = r.EffectiveDateAttr
	}
	if item.Reference == "" {
		item.Reference = r.ReferenceAttr
	}
	if item.PartyLedgerName == "" {
		item.PartyLedgerName = r.PartyLedgerNameAttr
	}
	if item.Amount == 0 && r.AmountAttr != 0 {
		item.Amount = r.AmountAttr
	}
	item.Amount = round2(item.Amount)
	if item.Narration == "" {
		item.Narration = r.NarrationAttr
	}

	var entries []LedgerEntryRecord
	if len(r.AllLedgerEntries) > 0 {
		for _, e := range r.AllLedgerEntries {
			rec := e.toLedgerEntryRecord()
			rec.Amount = round2(rec.Amount)
			if rec.LedgerName != "" || rec.Amount != 0 {
				entries = append(entries, rec)
			}
		}
	} else if len(r.LedgerEntries) > 0 {
		for _, e := range r.LedgerEntries {
			rec := e.toLedgerEntryRecord()
			rec.Amount = round2(rec.Amount)
			if rec.LedgerName != "" || rec.Amount != 0 {
				entries = append(entries, rec)
			}
		}
	}
	item.LedgerEntries = entries
	if len(r.AllInventoryEntries) > 0 {
		item.InventoryEntries = r.AllInventoryEntries
	} else if len(r.InventoryEntriesList) > 0 {
		item.InventoryEntries = r.InventoryEntriesList
	}
	return item
}

func parseVouchersFromXML(raw []byte) ([]VoucherItem, error) {
	var env struct {
		XMLName xml.Name `xml:"ENVELOPE"`
		Body    struct {
			Data struct {
				TallyMessages []struct {
					Voucher *rawVoucherXML `xml:"VOUCHER"`
				} `xml:"TALLYMESSAGE"`
				Vouchers   []rawVoucherXML `xml:"VOUCHER"`
				Collection struct {
					Vouchers []rawVoucherXML `xml:"VOUCHER"`
				} `xml:"COLLECTION"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}

	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling vouchers XML: %w", err)
	}

	var result []VoucherItem
	for _, msg := range env.Body.Data.TallyMessages {
		if msg.Voucher != nil {
			v := msg.Voucher.toVoucherItem()
			if v.VoucherTypeName != "" || v.VoucherNumber != "" {
				result = append(result, v)
			}
		}
	}
	if len(result) == 0 && len(env.Body.Data.Vouchers) > 0 {
		for _, rawV := range env.Body.Data.Vouchers {
			result = append(result, rawV.toVoucherItem())
		}
	}
	if len(result) == 0 && len(env.Body.Data.Collection.Vouchers) > 0 {
		for _, rawV := range env.Body.Data.Collection.Vouchers {
			result = append(result, rawV.toVoucherItem())
		}
	}
	return result, nil
}

// FilterVouchers queries vouchers from Tally using TDL server-side pushdown filters,
// short-lived collection caching for fast pagination, and in-memory precision filtering.
func (c *Client) FilterVouchers(ctx context.Context, opts VoucherFilterOptions) ([]VoucherItem, error) {
	company := opts.Company
	if company == "" {
		company = c.defaultCompany
	}
	opts.VoucherType = NormalizeVoucherTypeAlias(opts.VoucherType)
	opts.FromDate = CleanDate(opts.FromDate)
	opts.ToDate = CleanDate(opts.ToDate)

	includeEntries := opts.IncludeEntries || strings.TrimSpace(opts.Ledger) != ""
	cacheKey := fmt.Sprintf("vouchers:%s:%s:%s:%s:%v",
		company,
		strings.ToLower(strings.TrimSpace(opts.VoucherType)),
		opts.FromDate,
		opts.ToDate,
		includeEntries,
	)

	var baseVouchers []VoucherItem
	if c.cache != nil {
		if cached, ok := c.cache.Get(cacheKey); ok {
			if items, ok := cached.([]VoucherItem); ok {
				baseVouchers = items
			}
		}
	}

	if baseVouchers == nil {
		val, err := c.flight.Do(ctx, cacheKey, func(fetchCtx context.Context) (any, error) {
			if c.cache != nil {
				if cached, ok := c.cache.Get(cacheKey); ok {
					if items, ok := cached.([]VoucherItem); ok {
						return items, nil
					}
				}
			}
			xmlQuery, err := BuildFilteredListVouchersXML(company, opts)
			if err != nil {
				return nil, err
			}
			raw, err := c.ExecuteRawXML(fetchCtx, xmlQuery)
			if err != nil {
				return nil, err
			}
			parsed, err := parseVouchersFromXML(raw)
			if err != nil {
				return nil, err
			}
			if c.cache != nil && len(parsed) > 0 {
				c.cache.SetWithTTL(cacheKey, parsed, 15*time.Second)
			}
			return parsed, nil
		})
		if err != nil {
			return nil, err
		}
		baseVouchers, _ = val.([]VoucherItem)
	}

	filtered := filterVoucherItems(baseVouchers, opts)

	// Sort results (default date_desc so recent transactions appear first; stable for ties)
	sortMode := strings.ToLower(strings.TrimSpace(opts.Sort))
	switch sortMode {
	case "", "date_desc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].Date > filtered[j].Date
		})
	case "date_asc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].Date < filtered[j].Date
		})
	case "amount_desc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return math.Abs(filtered[i].Amount) > math.Abs(filtered[j].Amount)
		})
	case "amount_asc":
		sort.SliceStable(filtered, func(i, j int) bool {
			return math.Abs(filtered[i].Amount) < math.Abs(filtered[j].Amount)
		})
	}

	// Strip LedgerEntries from list responses unless explicitly requested so TOON stays tabular
	if !opts.IncludeEntries {
		for i := range filtered {
			filtered[i].LedgerEntries = nil
		}
	}

	return filtered, nil
}

// GetLedgerStatementVouchers fetches vouchers touching a specific ledger account within a date range.
func (c *Client) GetLedgerStatementVouchers(ctx context.Context, ledgerName, fromDate, toDate string) ([]VoucherItem, error) {
	return c.GetLedgerStatementVouchersForCompany(ctx, c.defaultCompany, ledgerName, fromDate, toDate)
}

// GetLedgerStatementVouchersForCompany fetches vouchers touching a specific ledger in a specific company.
func (c *Client) GetLedgerStatementVouchersForCompany(ctx context.Context, company, ledgerName, fromDate, toDate string) ([]VoucherItem, error) {
	xmlQuery, err := BuildLedgerStatementXML(company, ledgerName, fromDate, toDate)
	if err != nil {
		return nil, err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return nil, err
	}

	return parseVouchersFromXML(raw)
}

// FilterVouchersPaginated queries vouchers with server-side pushdown and returns a paginated slice.
func (c *Client) FilterVouchersPaginated(ctx context.Context, opts VoucherFilterOptions) (*PaginatedVouchers, error) {
	vouchers, err := c.FilterVouchers(ctx, opts)
	if err != nil {
		return nil, err
	}

	total := len(vouchers)
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	offset := opts.Offset
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

// SummarizeVouchers aggregates matching vouchers server-side into count and total_amount, optionally grouped by voucher_type, party, or month.
func (c *Client) SummarizeVouchers(ctx context.Context, opts VoucherFilterOptions, groupBy string) (*VoucherSummary, error) {
	vouchers, err := c.FilterVouchers(ctx, opts)
	if err != nil {
		return nil, err
	}

	var totalAmt float64
	for _, v := range vouchers {
		totalAmt += math.Abs(v.Amount)
	}

	summary := &VoucherSummary{
		TotalCount:  len(vouchers),
		TotalAmount: round2(totalAmt),
		FromDate:    CleanDate(opts.FromDate),
		ToDate:      CleanDate(opts.ToDate),
	}

	gb := strings.ToLower(strings.TrimSpace(groupBy))
	if gb != "" {
		summary.GroupBy = gb
		buckets := make(map[string]*VoucherSummaryGroup)
		for _, v := range vouchers {
			var key string
			switch gb {
			case "voucher_type", "type":
				key = v.VoucherTypeName
			case "party":
				key = v.PartyLedgerName
			case "month":
				if len(v.Date) >= 6 {
					key = v.Date[:6]
				} else {
					key = v.Date
				}
			default:
				key = v.VoucherTypeName
			}
			if key == "" {
				key = "(none)"
			}
			b, ok := buckets[key]
			if !ok {
				b = &VoucherSummaryGroup{Key: key}
				buckets[key] = b
			}
			b.Count++
			b.TotalAmount += math.Abs(v.Amount)
		}

		groups := make([]VoucherSummaryGroup, 0, len(buckets))
		for _, b := range buckets {
			b.TotalAmount = round2(b.TotalAmount)
			groups = append(groups, *b)
		}
		sort.Slice(groups, func(i, j int) bool {
			if groups[i].TotalAmount != groups[j].TotalAmount {
				return groups[i].TotalAmount > groups[j].TotalAmount
			}
			return groups[i].Key < groups[j].Key
		})
		summary.Groups = groups
	}

	return summary, nil
}

func filterVoucherItems(items []VoucherItem, opts VoucherFilterOptions) []VoucherItem {
	var filtered []VoucherItem
	partyLower := strings.ToLower(strings.TrimSpace(opts.Party))
	ledgerLower := strings.ToLower(strings.TrimSpace(opts.Ledger))
	searchLower := strings.ToLower(strings.TrimSpace(opts.Search))

	for _, item := range items {
		// Party check
		if partyLower != "" {
			itemParty := strings.ToLower(item.PartyLedgerName)
			if !strings.Contains(itemParty, partyLower) {
				continue
			}
		}

		// Ledger check (matches party ledger or any ledger entry in the split)
		if ledgerLower != "" {
			matched := strings.Contains(strings.ToLower(item.PartyLedgerName), ledgerLower)
			if !matched {
				for _, entry := range item.LedgerEntries {
					if strings.Contains(strings.ToLower(entry.LedgerName), ledgerLower) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
		}

		// Amount check
		if opts.Amount != nil {
			target := math.Abs(*opts.Amount)
			tol := 1.0
			if opts.AmountTolerance != nil && *opts.AmountTolerance >= 0 {
				tol = *opts.AmountTolerance
			}
			itemAmt := math.Abs(item.Amount)
			if itemAmt < target-tol || itemAmt > target+tol {
				continue
			}
		} else {
			if opts.MinAmount != nil && math.Abs(item.Amount) < *opts.MinAmount {
				continue
			}
			if opts.MaxAmount != nil && math.Abs(item.Amount) > *opts.MaxAmount {
				continue
			}
		}

		// Search check
		if searchLower != "" {
			match := strings.Contains(strings.ToLower(item.PartyLedgerName), searchLower) ||
				strings.Contains(strings.ToLower(item.VoucherNumber), searchLower) ||
				strings.Contains(strings.ToLower(item.Reference), searchLower) ||
				strings.Contains(strings.ToLower(item.Narration), searchLower) ||
				strings.Contains(strings.ToLower(item.VoucherTypeName), searchLower)
			if !match {
				continue
			}
		}

		filtered = append(filtered, item)
	}

	return filtered
}

// ListVouchers fetches vouchers with optional type, fromDate, and toDate (YYYYMMDD format).
func (c *Client) ListVouchers(ctx context.Context, voucherType, fromDate, toDate string) ([]VoucherItem, error) {
	return c.FilterVouchers(ctx, VoucherFilterOptions{
		VoucherType: voucherType,
		FromDate:    fromDate,
		ToDate:      toDate,
	})
}

// ListVouchersPaginated fetches vouchers and returns a paginated slice with total counts and has_more indicator.
func (c *Client) ListVouchersPaginated(ctx context.Context, voucherType, fromDate, toDate string, limit, offset int) (*PaginatedVouchers, error) {
	return c.FilterVouchersPaginated(ctx, VoucherFilterOptions{
		VoucherType: voucherType,
		FromDate:    fromDate,
		ToDate:      toDate,
		Limit:       limit,
		Offset:      offset,
	})
}

// GetVoucher retrieves full voucher details with debit/credit ledger splits, inventory entries, and tax breakdown.
func (c *Client) GetVoucher(ctx context.Context, masterID, voucherNo, voucherType, date string) (*VoucherDetail, error) {
	return c.GetVoucherForCompany(ctx, c.defaultCompany, masterID, voucherNo, voucherType, date)
}

// GetVoucherForCompany retrieves full voucher details in a specific company.
func (c *Client) GetVoucherForCompany(ctx context.Context, company, masterID, voucherNo, voucherType, date string) (*VoucherDetail, error) {
	voucherType = NormalizeVoucherTypeAlias(voucherType)
	xmlQuery, err := BuildGetVoucherXML(company, masterID, voucherNo, voucherType, date)
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
	v.Amount = round2(v.Amount)
	// Normalize ledger entries
	if len(v.LedgerEntries) == 0 {
		if len(v.AllLedgerEntries) > 0 {
			v.LedgerEntries = v.AllLedgerEntries
		} else if len(v.LedgerEntriesList) > 0 {
			v.LedgerEntries = v.LedgerEntriesList
		}
	}
	for i := range v.LedgerEntries {
		v.LedgerEntries[i].Amount = round2(v.LedgerEntries[i].Amount)
		if len(v.LedgerEntries[i].BillAllocations) == 0 && len(v.LedgerEntries[i].BillAllocationsAlt) > 0 {
			v.LedgerEntries[i].BillAllocations = v.LedgerEntries[i].BillAllocationsAlt
		}
		for j := range v.LedgerEntries[i].BillAllocations {
			v.LedgerEntries[i].BillAllocations[j].Amount = round2(v.LedgerEntries[i].BillAllocations[j].Amount)
		}
		if len(v.LedgerEntries[i].BankAllocations) == 0 && len(v.LedgerEntries[i].BankAllocationsAlt) > 0 {
			v.LedgerEntries[i].BankAllocations = v.LedgerEntries[i].BankAllocationsAlt
		}
		for j := range v.LedgerEntries[i].BankAllocations {
			v.LedgerEntries[i].BankAllocations[j].Amount = round2(v.LedgerEntries[i].BankAllocations[j].Amount)
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
		v.InventoryEntries[i].Amount = round2(v.InventoryEntries[i].Amount)
		if len(v.InventoryEntries[i].BatchAllocations) == 0 && len(v.InventoryEntries[i].BatchAllocationsAlt) > 0 {
			v.InventoryEntries[i].BatchAllocations = v.InventoryEntries[i].BatchAllocationsAlt
		}
		for j := range v.InventoryEntries[i].BatchAllocations {
			v.InventoryEntries[i].BatchAllocations[j].Amount = round2(v.InventoryEntries[i].BatchAllocations[j].Amount)
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
	return c.GetTrialBalanceForCompany(ctx, c.defaultCompany, fromDate, toDate)
}

// GetTrialBalanceForCompany retrieves the Trial Balance report for a specific company.
func (c *Client) GetTrialBalanceForCompany(ctx context.Context, company, fromDate, toDate string) (map[string]any, error) {
	xmlQuery, err := BuildTrialBalanceXML(company, fromDate, toDate)
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
	return c.GetProfitAndLossForCompany(ctx, c.defaultCompany, fromDate, toDate)
}

// GetProfitAndLossForCompany retrieves the Profit & Loss statement for a specific company.
func (c *Client) GetProfitAndLossForCompany(ctx context.Context, company, fromDate, toDate string) (map[string]any, error) {
	return c.GetProfitAndLossWithOptions(ctx, company, fromDate, toDate, true)
}

// GetProfitAndLossWithOptions retrieves the Profit & Loss statement with configurable explosion.
func (c *Client) GetProfitAndLossWithOptions(ctx context.Context, company, fromDate, toDate string, explode bool) (map[string]any, error) {
	xmlQuery, err := BuildProfitAndLossXMLWithOptions(company, fromDate, toDate, explode)
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
	return c.GetBalanceSheetForCompany(ctx, c.defaultCompany, asOnDate, fromDate)
}

// GetBalanceSheetForCompany retrieves the Balance Sheet report for a specific company.
func (c *Client) GetBalanceSheetForCompany(ctx context.Context, company, asOnDate, fromDate string) (map[string]any, error) {
	return c.GetBalanceSheetWithOptions(ctx, company, asOnDate, fromDate, true)
}

// GetBalanceSheetWithOptions retrieves the Balance Sheet report with configurable explosion.
func (c *Client) GetBalanceSheetWithOptions(ctx context.Context, company, asOnDate, fromDate string, explode bool) (map[string]any, error) {
	xmlQuery, err := BuildBalanceSheetXMLWithOptions(company, asOnDate, fromDate, explode)
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
	return c.GetOutstandingReceivablesForCompany(ctx, c.defaultCompany, party, fromDate, toDate)
}

// GetOutstandingReceivablesForCompany retrieves the Bills Receivable report for a specific company.
func (c *Client) GetOutstandingReceivablesForCompany(ctx context.Context, company, party, fromDate, toDate string) (map[string]any, error) {
	return c.GetOutstandingReceivablesWithOptions(ctx, company, party, fromDate, toDate, true)
}

// GetOutstandingReceivablesWithOptions retrieves the Bills Receivable report with configurable explosion.
func (c *Client) GetOutstandingReceivablesWithOptions(ctx context.Context, company, party, fromDate, toDate string, explode bool) (map[string]any, error) {
	xmlQuery, err := BuildBillsReceivableXMLWithOptions(company, party, fromDate, toDate, explode)
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
	return c.GetOutstandingPayablesForCompany(ctx, c.defaultCompany, party, fromDate, toDate)
}

// GetOutstandingPayablesForCompany retrieves the Bills Payable report for a specific company.
func (c *Client) GetOutstandingPayablesForCompany(ctx context.Context, company, party, fromDate, toDate string) (map[string]any, error) {
	return c.GetOutstandingPayablesWithOptions(ctx, company, party, fromDate, toDate, true)
}

// GetOutstandingPayablesWithOptions retrieves the Bills Payable report with configurable explosion.
func (c *Client) GetOutstandingPayablesWithOptions(ctx context.Context, company, party, fromDate, toDate string, explode bool) (map[string]any, error) {
	xmlQuery, err := BuildBillsPayableXMLWithOptions(company, party, fromDate, toDate, explode)
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
	return c.GetDayBookForCompany(ctx, c.defaultCompany, date)
}

// GetDayBookForCompany retrieves the Day Book report for a specific company.
func (c *Client) GetDayBookForCompany(ctx context.Context, company, date string) (map[string]any, error) {
	return c.GetDayBookWithOptions(ctx, company, date, true)
}

// GetDayBookWithOptions retrieves the Day Book report with configurable explosion.
func (c *Client) GetDayBookWithOptions(ctx context.Context, company, date string, explode bool) (map[string]any, error) {
	xmlQuery, err := BuildDayBookXMLWithOptions(company, date, explode)
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
	return c.CreateLedgerForCompany(ctx, c.defaultCompany, name, parent, openingBalance)
}

// CreateLedgerForCompany creates a new ledger master in a specific company.
func (c *Client) CreateLedgerForCompany(ctx context.Context, company, name, parent string, openingBalance float64) (string, error) {
	parent = NormalizeGroupAlias(parent)
	xmlQuery, err := BuildCreateLedgerXML(company, name, parent, openingBalance)
	if err != nil {
		return "", err
	}

	raw, err := c.ExecuteRawXML(ctx, xmlQuery)
	if err != nil {
		return "", err
	}

	// Invalidate ledger cache so subsequent queries reflect newly created ledgers
	c.InvalidateCache("ledgers:")

	return string(raw), nil
}

// GetFinancialSummary compiles an executive financial KPI snapshot in < 350 tokens,
// returning cash/bank balances, receivables, payables, top debtors/creditors, and working capital.
func (c *Client) GetFinancialSummary(ctx context.Context, asOnDate, fromDate string) (*FinancialSummary, error) {
	return c.GetFinancialSummaryForCompany(ctx, c.defaultCompany, asOnDate, fromDate)
}

// GetFinancialSummaryForCompany compiles an executive financial KPI snapshot for a specific company.
func (c *Client) GetFinancialSummaryForCompany(ctx context.Context, company, asOnDate, fromDate string) (*FinancialSummary, error) {
	cleanAsOn := CleanDate(asOnDate)
	if cleanAsOn == "" {
		cleanAsOn = time.Now().Format("20060102")
	}
	cleanFrom := CleanDate(fromDate)

	ledgers, err := c.ListLedgersWithDates(ctx, company, "", cleanFrom, cleanAsOn)
	if err != nil {
		return nil, fmt.Errorf("fetching ledgers for financial summary: %w", err)
	}

	// Best-effort group hierarchy lookup so custom child groups (e.g. "North Zone Debtors") map cleanly
	groupParentMap := make(map[string]string)
	if groups, gErr := c.ListGroupsForCompany(ctx, company); gErr == nil {
		for _, g := range groups {
			if g.Name != "" && g.Parent != "" {
				groupParentMap[strings.ToLower(strings.TrimSpace(g.Name))] = strings.TrimSpace(g.Parent)
			}
		}
	}

	var cashAndBank, totalReceivable, totalPayable float64
	var revenueYTD, expensesYTD float64
	var debtors, creditors []PartyBalance

	for _, l := range ledgers {
		cat := classifyLedgerCategory(l.Parent, groupParentMap)
		switch cat {
		case "cash_bank":
			// Assets are negative (Dr) in Tally XML; positive (Cr) is bank overdraft
			cashAndBank += -l.ClosingBalance
		case "debtor":
			bal := -l.ClosingBalance
			if bal != 0 {
				totalReceivable += bal
				if bal > 0 {
					debtors = append(debtors, PartyBalance{Name: l.Name, Balance: bal})
				}
			}
		case "creditor":
			bal := l.ClosingBalance
			if bal != 0 {
				totalPayable += bal
				if bal > 0 {
					creditors = append(creditors, PartyBalance{Name: l.Name, Balance: bal})
				}
			}
		case "revenue":
			revenueYTD += math.Abs(l.ClosingBalance)
		case "expense":
			expensesYTD += math.Abs(l.ClosingBalance)
		}
	}

	// Sort debtors descending by balance and pick top 5
	sort.Slice(debtors, func(i, j int) bool {
		return debtors[i].Balance > debtors[j].Balance
	})
	if len(debtors) > 5 {
		debtors = debtors[:5]
	}

	for i := range debtors {
		debtors[i].Balance = round2(debtors[i].Balance)
	}
	// Sort creditors descending by balance and pick top 5
	sort.Slice(creditors, func(i, j int) bool {
		return creditors[i].Balance > creditors[j].Balance
	})
	if len(creditors) > 5 {
		creditors = creditors[:5]
	}
	for i := range creditors {
		creditors[i].Balance = round2(creditors[i].Balance)
	}

	cashAndBank = round2(cashAndBank)
	totalReceivable = round2(totalReceivable)
	totalPayable = round2(totalPayable)
	revenueYTD = round2(revenueYTD)
	expensesYTD = round2(expensesYTD)
	netProfitYTD := round2(revenueYTD - expensesYTD)
	workingCapital := round2((cashAndBank + totalReceivable) - totalPayable)

	companyName := company
	if companyName == "" {
		if comps, err := c.ListCompanies(ctx); err == nil && len(comps) > 0 {
			companyName = comps[0].Name
		}
	}
	if companyName == "" {
		companyName = "Active Company"
	}

	summaryText := fmt.Sprintf(
		"Company: %s (as on %s) | Cash & Bank: %.2f | Receivables: %.2f | Payables: %.2f | Working Capital: %.2f | Revenue: %.2f | Net Profit: %.2f",
		companyName, cleanAsOn, cashAndBank, totalReceivable, totalPayable, workingCapital, revenueYTD, netProfitYTD,
	)

	return &FinancialSummary{
		AsOnDate:        cleanAsOn,
		Company:         companyName,
		CashAndBank:     cashAndBank,
		TotalReceivable: totalReceivable,
		TopDebtors:      debtors,
		TotalPayable:    totalPayable,
		TopCreditors:    creditors,
		RevenueYTD:      revenueYTD,
		NetProfitYTD:    netProfitYTD,
		WorkingCapital:  workingCapital,
		Summary:         summaryText,
	}, nil
}

func classifyLedgerCategory(parent string, groupParentMap map[string]string) string {
	curr := strings.TrimSpace(parent)
	for depth := 0; depth < 16 && curr != ""; depth++ {
		lower := strings.ToLower(curr)
		switch {
		case strings.Contains(lower, "bank") || strings.Contains(lower, "cash"):
			return "cash_bank"
		case strings.Contains(lower, "debtor"):
			return "debtor"
		case strings.Contains(lower, "creditor"):
			return "creditor"
		case strings.Contains(lower, "sales") || strings.Contains(lower, "income"):
			return "revenue"
		case strings.Contains(lower, "purchase") || strings.Contains(lower, "expense"):
			return "expense"
		}
		next, ok := groupParentMap[lower]
		if !ok || strings.EqualFold(next, curr) {
			break
		}
		curr = next
	}
	return ""
}

// GetPartyOverviewForCompany returns a 360-degree view of a customer/vendor (profile + pending bills + recent vouchers) in 1 call.
func (c *Client) GetPartyOverviewForCompany(ctx context.Context, company, partyInput, fromDate, toDate string, recentLimit int) (*PartyOverview, error) {
	if recentLimit <= 0 {
		recentLimit = 10
	}
	if recentLimit > 50 {
		recentLimit = 50
	}

	resolvedName, correctedFrom, resolveErr := c.ResolveLedgerName(ctx, company, partyInput)
	if resolveErr != nil {
		return nil, resolveErr
	}

	// Build profile from detail call, falling back to cached ledger list if single-ledger XML is unavailable.
	var profile *LedgerDetail
	if d, err := c.GetLedgerDetailForCompany(ctx, company, resolvedName); err == nil && d != nil {
		profile = d
	} else if ledgers, lErr := c.ListLedgersForCompany(ctx, company, ""); lErr == nil {
		for _, l := range ledgers {
			if strings.EqualFold(l.Name, resolvedName) {
				profile = &LedgerDetail{
					Name:           l.Name,
					Parent:         l.Parent,
					ClosingBalance: round2(l.ClosingBalance),
					Phone:          l.Phone,
					Email:          l.Email,
					State:          l.State,
					GSTIN:          l.GSTIN,
				}
				break
			}
		}
	}
	if profile == nil {
		profile = &LedgerDetail{Name: resolvedName}
	}

	balType := "Cr"
	if profile.ClosingBalance < 0 {
		balType = "Dr"
	}

	overview := &PartyOverview{
		Name:           profile.Name,
		ResolvedFrom:   correctedFrom,
		Parent:         profile.Parent,
		ClosingBalance: round2(math.Abs(profile.ClosingBalance)),
		BalanceType:    balType,
		Phone:          profile.Phone,
		Email:          profile.Email,
		GSTIN:          profile.GSTIN,
		State:          profile.State,
	}

	// Fetch pending bills (Payables for Sundry Creditors, Receivables otherwise)
	isCreditor := strings.Contains(strings.ToLower(profile.Parent), "creditor") || strings.Contains(strings.ToLower(profile.Parent), "payable")
	var rawBills map[string]any
	var reportKind string
	if isCreditor {
		reportKind = "bills_payable"
		rawBills, _ = c.GetOutstandingPayablesWithOptions(ctx, company, resolvedName, fromDate, toDate, true)
	} else {
		reportKind = "bills_receivable"
		rawBills, _ = c.GetOutstandingReceivablesWithOptions(ctx, company, resolvedName, fromDate, toDate, true)
	}
	if len(rawBills) > 0 {
		if norm, ok := NormalizeReportWithOptions(reportKind, rawBills, ReportQueryOptions{
			Limit: 10,
			Sort:  "overdue_desc",
		}).(map[string]any); ok {
			if total, ok := norm["total"].(int); ok {
				overview.OutstandingsCount = total
			}
			if bills, ok := norm["bills"].([]OutstandingBillRow); ok {
				overview.OutstandingBills = bills
			}
		}
	}

	// Fetch recent vouchers touching this party/ledger
	vFrom, vTo := ResolveDateRange(fromDate, toDate, time.Now())
	if vFrom == "" {
		vFrom = CurrentFinancialYearStart(time.Now())
	}
	if pv, err := c.FilterVouchersPaginated(ctx, VoucherFilterOptions{
		Company:  company,
		Party:    resolvedName,
		FromDate: vFrom,
		ToDate:   vTo,
		Sort:     "date_desc",
		Limit:    recentLimit,
	}); err == nil && pv != nil {
		overview.RecentCount = pv.Total
		overview.RecentVouchers = pv.Vouchers
	}

	return overview, nil
}

// GeneratePDFOptions specifies the report or voucher parameters for PDF generation.
type GeneratePDFOptions struct {
	Company       string // Optional target company
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
	Base64PDF   string `json:"base64_pdf,omitempty"`
	ReportType  string `json:"report_type"`
	Message     string `json:"message"`
}

// GeneratePDF instructs Tally to generate a PDF, stores it in the system temp directory, and returns its metadata, path, and base64 payload.
func (c *Client) GeneratePDF(ctx context.Context, opts GeneratePDFOptions) (*PDFResult, []byte, error) {
	reportID := NormalizeReportID(opts.ReportType)
	company := opts.Company
	if company == "" {
		company = c.defaultCompany
	}

	// Clean dates
	opts.FromDate = CleanDate(opts.FromDate)
	opts.ToDate = CleanDate(opts.ToDate)
	opts.Date = CleanDate(opts.Date)

	// Determine and sanitize output file name to prevent path traversal
	fileName := strings.TrimSpace(opts.FileName)
	if fileName != "" {
		fileName = filepath.Base(strings.ReplaceAll(fileName, "\\", "/"))
		if fileName == "." || fileName == ".." || fileName == "/" || fileName == "" {
			return nil, nil, fmt.Errorf("invalid filename %q", opts.FileName)
		}
	}
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
	pruneOldPDFs(tempDir, 24*time.Hour)

	targetPath := filepath.Join(tempDir, fileName)
	_ = os.Remove(targetPath)

	// 1. Tell Tally to export the PDF via native XML
	xmlQuery, err := BuildExportPDFXMLWithMasterID(company, reportID, tempDir, fileName, opts.FromDate, opts.ToDate, opts.Date, opts.Party, opts.VoucherNumber, opts.VoucherType, opts.MasterID)
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
		companyName := company
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
			v, vErr := c.GetVoucherForCompany(ctx, company, opts.MasterID, opts.VoucherNumber, opts.VoucherType, opts.Date)
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

func pruneOldPDFs(dir string, maxAge time.Duration) {
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
