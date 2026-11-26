package tally

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// LedgerNotFoundError is returned when a ledger lookup fails, carrying close candidates
// so AI agents can self-correct without extra search round-trips.
type LedgerNotFoundError struct {
	Query      string
	Candidates []string
}

func (e *LedgerNotFoundError) Error() string {
	if len(e.Candidates) > 0 {
		return fmt.Sprintf("ledger %q not found; did you mean one of: %s", e.Query, strings.Join(e.Candidates, ", "))
	}
	return fmt.Sprintf("ledger %q not found; call tally_list_ledgers with search to discover exact ledger names", e.Query)
}

// CompanyNotFoundError is returned when a requested company name does not match loaded companies.
type CompanyNotFoundError struct {
	Query     string
	Available []string
}

func (e *CompanyNotFoundError) Error() string {
	if len(e.Available) > 0 {
		return fmt.Sprintf("company %q is not loaded in Tally; loaded companies: %s", e.Query, strings.Join(e.Available, ", "))
	}
	return fmt.Sprintf("company %q is not loaded in Tally; call tally_list_companies to inspect loaded companies", e.Query)
}

// NormalizeGroupAlias maps common natural-language ledger group terms used by LLMs
// to Tally's standard accounting group names.
func NormalizeGroupAlias(group string) string {
	g := strings.TrimSpace(group)
	if g == "" {
		return ""
	}
	switch strings.ToLower(g) {
	case "customer", "customers", "debtor", "debtors", "ar", "accounts receivable", "receivable", "receivables":
		return "Sundry Debtors"
	case "vendor", "vendors", "supplier", "suppliers", "creditor", "creditors", "ap", "accounts payable", "payable", "payables":
		return "Sundry Creditors"
	case "bank", "banks", "bank account":
		return "Bank Accounts"
	case "cash", "cash in hand", "petty cash":
		return "Cash-in-Hand"
	case "expense", "expenses", "indirect expense":
		return "Indirect Expenses"
	case "direct expense":
		return "Direct Expenses"
	case "sales", "revenue":
		return "Sales Accounts"
	case "purchase", "purchases":
		return "Purchase Accounts"
	case "income", "indirect income":
		return "Indirect Incomes"
	case "direct income":
		return "Direct Incomes"
	case "fixed asset", "fixed assets":
		return "Fixed Assets"
	case "current asset", "current assets":
		return "Current Assets"
	case "current liability", "current liabilities":
		return "Current Liabilities"
	case "duties", "taxes", "gst", "duties and taxes":
		return "Duties & Taxes"
	default:
		return g
	}
}

// NormalizeVoucherTypeAlias maps common plural/informal voucher type names to Tally's standard names.
func NormalizeVoucherTypeAlias(vtype string) string {
	v := strings.TrimSpace(vtype)
	if v == "" {
		return ""
	}
	switch strings.ToLower(v) {
	case "receipts", "rcpt":
		return "Receipt"
	case "payments", "pmt", "pay":
		return "Payment"
	case "sale", "sales invoice", "sales_invoice", "invoice", "invoices":
		return "Sales"
	case "purchases", "purchase bill", "purchase_bill", "bill":
		return "Purchase"
	case "journals", "jv":
		return "Journal"
	case "contras":
		return "Contra"
	case "credit note", "credit_note", "creditnote", "cn":
		return "Credit Note"
	case "debit note", "debit_note", "debitnote", "dn":
		return "Debit Note"
	default:
		return v
	}
}

func normalizeAlphaNum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ResolveCompany resolves an optional company parameter against loaded companies.
// When company is empty, it returns the configured default company.
func (c *Client) ResolveCompany(ctx context.Context, company string) (resolved string, correctedFrom string, err error) {
	trimmed := strings.TrimSpace(company)
	if trimmed == "" {
		return c.defaultCompany, "", nil
	}

	companies, listErr := c.ListCompanies(ctx)
	if listErr != nil || len(companies) == 0 {
		return trimmed, "", nil
	}

	var available []string
	var ciMatches []string
	var subMatches []string
	lowerTarget := strings.ToLower(trimmed)

	for _, comp := range companies {
		name := strings.TrimSpace(comp.Name)
		if name == "" {
			continue
		}
		available = append(available, name)
		if name == trimmed {
			return name, "", nil
		}
		if strings.EqualFold(name, trimmed) {
			ciMatches = append(ciMatches, name)
		} else if strings.Contains(strings.ToLower(name), lowerTarget) {
			subMatches = append(subMatches, name)
		}
	}

	if len(ciMatches) == 1 {
		return ciMatches[0], trimmed, nil
	}
	if len(subMatches) == 1 {
		return subMatches[0], trimmed, nil
	}

	return "", "", &CompanyNotFoundError{
		Query:     trimmed,
		Available: available,
	}
}

// ResolveLedgerName resolves a ledger name against the cached master ledger list.
// It auto-corrects casing, punctuation differences, and unambiguous substring matches,
// and returns a LedgerNotFoundError with top candidates when ambiguous or missing.
func (c *Client) ResolveLedgerName(ctx context.Context, company, input string) (resolved string, correctedFrom string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", fmt.Errorf("ledger name cannot be empty")
	}

	ledgers, listErr := c.ListLedgersForCompany(ctx, company, "")
	if listErr != nil || len(ledgers) == 0 {
		// Minimal test mocks or empty master responses fall back cleanly
		return trimmed, "", nil
	}

	// 1. Exact match
	for _, l := range ledgers {
		if l.Name == trimmed {
			return l.Name, "", nil
		}
	}

	// 2. Case-insensitive exact match
	for _, l := range ledgers {
		if strings.EqualFold(strings.TrimSpace(l.Name), trimmed) {
			return l.Name, trimmed, nil
		}
	}

	// 3. Alphanumeric normalized match (e.g. "GR Metalloys" -> "G.R. Metalloys")
	normTarget := normalizeAlphaNum(trimmed)
	var normMatches []string
	if normTarget != "" {
		for _, l := range ledgers {
			if normalizeAlphaNum(l.Name) == normTarget {
				normMatches = append(normMatches, l.Name)
			}
		}
		if len(normMatches) == 1 {
			return normMatches[0], trimmed, nil
		}
	}

	// 4. Case-insensitive substring match
	lowerTarget := strings.ToLower(trimmed)
	var subMatchedLedgers []LedgerItem
	for _, l := range ledgers {
		if strings.Contains(strings.ToLower(l.Name), lowerTarget) {
			subMatchedLedgers = append(subMatchedLedgers, l)
		}
	}
	if len(subMatchedLedgers) == 1 {
		return subMatchedLedgers[0].Name, trimmed, nil
	}
	if len(subMatchedLedgers) > 1 {
		if len(subMatchedLedgers) > 5 {
			subMatchedLedgers = subMatchedLedgers[:5]
		}
		candidates := make([]string, len(subMatchedLedgers))
		for i, l := range subMatchedLedgers {
			candidates[i] = formatLedgerCandidate(l)
		}
		return "", "", &LedgerNotFoundError{Query: trimmed, Candidates: candidates}
	}

	// 5. Fuzzy ranking by token overlap + edit distance for "did you mean"
	type scoredLedger struct {
		ledger LedgerItem
		score  int
	}
	targetTokens := strings.Fields(strings.ToLower(trimmed))
	var scored []scoredLedger
	for _, l := range ledgers {
		dist := levenshteinDistance(lowerTarget, strings.ToLower(l.Name))
		bonus := 0
		lLower := strings.ToLower(l.Name)
		for _, tok := range targetTokens {
			if len(tok) >= 2 && strings.Contains(lLower, tok) {
				bonus += 5
			}
		}
		// Only suggest reasonably close candidates
		maxLen := len(lowerTarget)
		if len(lLower) > maxLen {
			maxLen = len(lLower)
		}
		if bonus > 0 || dist <= max(3, maxLen/2) {
			scored = append(scored, scoredLedger{ledger: l, score: dist - bonus})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score < scored[j].score
		}
		return scored[i].ledger.Name < scored[j].ledger.Name
	})

	var candidates []string
	for i := 0; i < len(scored) && i < 5; i++ {
		candidates = append(candidates, formatLedgerCandidate(scored[i].ledger))
	}

	return "", "", &LedgerNotFoundError{
		Query:      trimmed,
		Candidates: candidates,
	}
}

func formatLedgerCandidate(l LedgerItem) string {
	if l.Parent != "" {
		return fmt.Sprintf("%s [%s: %.2f]", l.Name, l.Parent, round2(l.ClosingBalance))
	}
	return l.Name
}

func levenshteinDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}

	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := 0; j <= len(rb); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
