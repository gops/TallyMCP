package tally

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func collectEntryLedgerNames(entries []VoucherEntryInput) []string {
	seen := make(map[string]bool)
	var out []string
	for _, e := range entries {
		name := strings.TrimSpace(e.LedgerName)
		if name != "" && !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			out = append(out, name)
		}
	}
	return out
}

func checkTallyImportResponse(raw []byte, op string) error {
	if imp := ParseImportResult(raw); imp != nil {
		if imp.Errors > 0 || imp.Exceptions > 0 || imp.LineError != "" {
			return fmt.Errorf("Tally reported %s error: errors=%d exceptions=%d line_error=%q", op, imp.Errors, imp.Exceptions, imp.LineError)
		}
		return nil
	}
	return CheckTallyError(raw)
}

func voucherDetailToSnapshot(v *VoucherDetail) *VoucherSnapshot {
	if v == nil {
		return nil
	}
	entries := make([]VoucherEntryInput, 0, len(v.LedgerEntries))
	var totalDebit, totalCredit float64
	for _, leg := range v.LedgerEntries {
		isDebit := leg.Amount < 0
		absAmt := math.Abs(leg.Amount)
		if isDebit {
			totalDebit += absAmt
		} else {
			totalCredit += absAmt
		}
		var bills []BillAllocationInput
		for _, b := range leg.BillAllocations {
			bills = append(bills, BillAllocationInput{
				Name:     b.Name,
				BillType: b.BillType,
				Amount:   round2(b.Amount),
			})
		}
		entries = append(entries, VoucherEntryInput{
			LedgerName:      leg.LedgerName,
			Amount:          round2(leg.Amount),
			IsDebit:         isDebit,
			BillAllocations: bills,
			BankAllocations: leg.BankAllocations,
		})
	}
	return &VoucherSnapshot{
		MasterID:      v.MasterID,
		VoucherType:   v.VoucherTypeName,
		VoucherNumber: v.VoucherNumber,
		Date:          CleanDate(v.Date),
		Reference:     v.Reference,
		PartyLedger:   v.PartyLedgerName,
		Narration:     v.Narration,
		TotalDebit:    round2(totalDebit),
		TotalCredit:   round2(totalCredit),
		Entries:       entries,
	}
}

// BuildLedgerStatementResult fetches and computes a complete running-balance statement for any ledger
// (including Expense, Tax, Bank, and Suspense ledgers where the ledger appears in any entry line).
func (c *Client) BuildLedgerStatementResult(
	ctx context.Context,
	company, ledgerInput, rawFrom, rawTo, sortOrder string,
	limit, offset int,
	summaryOnly bool,
	maxNarrLen int,
) (any, error) {
	resolvedName, correctedFrom, resolveErr := c.ResolveLedgerName(ctx, company, ledgerInput)
	if resolveErr != nil {
		return nil, resolveErr
	}

	fromDate, toDate := ResolveDateRange(rawFrom, rawTo, time.Now())
	if fromDate == "" {
		fromDate = CurrentFinancialYearStart(time.Now())
	}
	if toDate == "" {
		toDate = CurrentFinancialYearEnd(time.Now())
	}

	var parent string
	var openingBalance float64
	var foundInCache bool

	if ledgers, lErr := c.ListLedgersForCompany(ctx, company, ""); lErr == nil {
		for _, l := range ledgers {
			if strings.EqualFold(strings.TrimSpace(l.Name), resolvedName) {
				parent = l.Parent
				openingBalance = l.OpeningBalance
				foundInCache = true
				break
			}
		}
	}
	if !foundInCache {
		if ld, ldErr := c.GetLedgerDetailForCompany(ctx, company, resolvedName); ldErr == nil && ld != nil {
			parent = ld.Parent
			openingBalance = ld.OpeningBalance
		}
	}

	vouchers, err := c.GetLedgerStatementVouchersForCompany(ctx, company, resolvedName, fromDate, toDate)
	if err != nil {
		return nil, fmt.Errorf("fetching vouchers for ledger %q: %w", resolvedName, err)
	}

	entries := make([]LedgerStatementEntry, 0)
	var totalDebit, totalCredit float64

	isTarget := func(name string) bool {
		return strings.EqualFold(strings.TrimSpace(name), resolvedName)
	}

	for _, v := range vouchers {
		cleanVDate := CleanDate(v.Date)
		if fromDate != "" && cleanVDate != "" && cleanVDate < fromDate {
			continue
		}
		if toDate != "" && cleanVDate != "" && cleanVDate > toDate {
			continue
		}

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

		if !matched && isTarget(v.PartyLedgerName) {
			matchedAmount = v.Amount
			matched = true
		}
		if !matched {
			continue
		}

		if len(contraLedgers) == 0 && !isTarget(v.PartyLedgerName) && strings.TrimSpace(v.PartyLedgerName) != "" {
			contraLedgers = append(contraLedgers, strings.TrimSpace(v.PartyLedgerName))
		}
		seenContra := make(map[string]bool)
		var uniqueContras []string
		for _, cl := range contraLedgers {
			if !seenContra[cl] {
				seenContra[cl] = true
				uniqueContras = append(uniqueContras, cl)
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

		dateStr := cleanVDate
		if len(dateStr) == 8 {
			dateStr = fmt.Sprintf("%s-%s-%s", dateStr[0:4], dateStr[4:6], dateStr[6:8])
		}

		entries = append(entries, LedgerStatementEntry{
			MasterID:      v.MasterID,
			Date:          dateStr,
			VoucherType:   v.VoucherTypeName,
			VoucherNumber: v.VoucherNumber,
			Reference:     v.Reference,
			ContraLedger:  contraStr,
			Debit:         round2(debit),
			Credit:        round2(credit),
			Narration:     v.Narration,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date < entries[j].Date
		}
		return entries[i].VoucherNumber < entries[j].VoucherNumber
	})

	running := openingBalance
	for i := range entries {
		running = running + entries[i].Credit - entries[i].Debit
		entries[i].RunningBalance = round2(running)
		entries[i].Balance = round2(math.Abs(running))
		if running < 0 {
			entries[i].BalanceType = "Dr"
		} else {
			entries[i].BalanceType = "Cr"
		}
	}

	totalDebit = round2(totalDebit)
	totalCredit = round2(totalCredit)
	net := openingBalance + totalCredit - totalDebit
	closingBalance := round2(math.Abs(net))
	closingBalanceType := "Cr"
	if net < 0 {
		closingBalanceType = "Dr"
	}
	openingBalanceType := "Cr"
	if openingBalance < 0 {
		openingBalanceType = "Dr"
	}

	if strings.EqualFold(strings.TrimSpace(sortOrder), "desc") {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}

	totalEntries := len(entries)
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	var pagedEntries []LedgerStatementEntry
	var hasMore bool
	var nextOffset *int
	if offset < totalEntries {
		end := offset + limit
		if end > totalEntries {
			end = totalEntries
		}
		pagedEntries = entries[offset:end]
		if end < totalEntries {
			hasMore = true
			nextVal := end
			nextOffset = &nextVal
		}
	} else {
		pagedEntries = []LedgerStatementEntry{}
	}

	var statementNote string
	if totalEntries == 0 {
		statementNote = fmt.Sprintf("0 entries in %s..%s; try broadening from_date/to_date (e.g. 'last_fy' or explicit YYYYMMDD)", fromDate, toDate)
	}

	if summaryOnly {
		compactEntries := make([]CompactStatementEntry, len(pagedEntries))
		for i, e := range pagedEntries {
			signedAmt := e.Credit
			if e.Debit > 0 && e.Credit == 0 {
				signedAmt = -e.Debit
			} else if e.Debit > 0 {
				signedAmt = round2(e.Credit - e.Debit)
			}
			compactEntries[i] = CompactStatementEntry{
				MasterID:      e.MasterID,
				Date:          strings.ReplaceAll(e.Date, "-", ""),
				VoucherType:   e.VoucherType,
				VoucherNumber: e.VoucherNumber,
				Reference:     e.Reference,
				ContraLedger:  e.ContraLedger,
				Amount:        signedAmt,
				Balance:       e.Balance,
				BalanceType:   e.BalanceType,
				Narration:     truncateText(e.Narration, maxNarrLen),
			}
		}
		return CompactLedgerStatementResult{
			LedgerName:         resolvedName,
			ResolvedFrom:       correctedFrom,
			Parent:             parent,
			FromDate:           fromDate,
			ToDate:             toDate,
			OpeningBalance:     round2(math.Abs(openingBalance)),
			OpeningBalanceType: openingBalanceType,
			TotalDebit:         totalDebit,
			TotalCredit:        totalCredit,
			ClosingBalance:     closingBalance,
			ClosingBalanceType: closingBalanceType,
			EntriesCount:       len(compactEntries),
			TotalEntries:       totalEntries,
			Limit:              limit,
			Offset:             offset,
			HasMore:            hasMore,
			NextOffset:         nextOffset,
			Note:               statementNote,
			Entries:            compactEntries,
		}, nil
	}

	return LedgerStatementResult{
		LedgerName:         resolvedName,
		ResolvedFrom:       correctedFrom,
		Parent:             parent,
		FromDate:           fromDate,
		ToDate:             toDate,
		OpeningBalance:     round2(math.Abs(openingBalance)),
		OpeningBalanceType: openingBalanceType,
		TotalDebit:         totalDebit,
		TotalCredit:        totalCredit,
		ClosingBalance:     closingBalance,
		ClosingBalanceType: closingBalanceType,
		EntriesCount:       len(pagedEntries),
		TotalEntries:       totalEntries,
		Limit:              limit,
		Offset:             offset,
		HasMore:            hasMore,
		NextOffset:         nextOffset,
		Note:               statementNote,
		Entries:            pagedEntries,
	}, nil
}

func truncateText(s string, maxLen int) string {
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

// CreateLedgerWithOptions validates, previews (if dryRun), or creates a new ledger with GST/PAN/address/bill-wise attributes.
func (c *Client) CreateLedgerWithOptions(ctx context.Context, opts LedgerWriteOptions, dryRun bool) (*LedgerMutationResult, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.Parent = NormalizeGroupAlias(opts.Parent)
	if opts.Name == "" {
		return nil, fmt.Errorf("ledger 'name' is required")
	}
	if opts.Parent == "" {
		return nil, fmt.Errorf("ledger 'parent' group is required")
	}

	if existing, err := c.ListLedgersForCompany(ctx, opts.Company, ""); err == nil {
		for _, l := range existing {
			if strings.EqualFold(strings.TrimSpace(l.Name), opts.Name) {
				errMsg := fmt.Sprintf("ledger %q already exists under %q", l.Name, l.Parent)
				c.emitAudit(AuditEntry{
					Company:         opts.Company,
					Operation:       "create_ledger",
					DryRun:          dryRun,
					Status:          "rejected",
					AffectedLedgers: []string{opts.Name},
					Error:           errMsg,
				})
				return nil, fmt.Errorf("%s", errMsg)
			}
		}
	}

	xmlPayload, err := BuildCreateLedgerXMLWithOptions(opts)
	if err != nil {
		return nil, err
	}

	after := buildProjectedLedgerDetail(&LedgerDetail{Name: opts.Name}, opts)
	res := &LedgerMutationResult{
		Status:  "preview",
		DryRun:  dryRun,
		Company: opts.Company,
		Ledger:  opts.Name,
		After:   after,
	}

	if dryRun {
		res.Hint = "Dry-run preview succeeded. Re-call tally_create_ledger with dry_run=false to create this ledger in Tally."
		c.emitAudit(AuditEntry{
			Company:         opts.Company,
			Operation:       "create_ledger",
			DryRun:          true,
			Status:          "preview",
			AffectedLedgers: []string{opts.Name},
			After:           after,
		})
		return res, nil
	}

	raw, err := c.ExecuteRawXML(ctx, xmlPayload)
	if err != nil {
		return nil, err
	}
	if err := checkTallyImportResponse(raw, "create ledger"); err != nil {
		return nil, err
	}

	c.InvalidateCache("ledgers:")
	res.Status = "created"
	c.emitAudit(AuditEntry{
		Company:         opts.Company,
		Operation:       "create_ledger",
		DryRun:          false,
		Status:          "applied",
		AffectedLedgers: []string{opts.Name},
		After:           after,
	})
	return res, nil
}

// fetchLedgerDetailFull retrieves a LedgerDetail, falling back to ListLedgersForCompany when SingleLedgerColl isn't populated.
func (c *Client) fetchLedgerDetailFull(ctx context.Context, company, ledgerInput string) (*LedgerDetail, error) {
	resolvedName, _, rErr := c.ResolveLedgerName(ctx, company, ledgerInput)
	if rErr != nil {
		return nil, rErr
	}

	if d, err := c.GetLedgerDetailForCompany(ctx, company, resolvedName); err == nil && d != nil {
		return d, nil
	}

	ledgers, lErr := c.ListLedgersForCompany(ctx, company, "")
	if lErr != nil {
		return nil, lErr
	}
	for _, l := range ledgers {
		if strings.EqualFold(strings.TrimSpace(l.Name), resolvedName) {
			return &LedgerDetail{
				Name:                l.Name,
				Parent:              l.Parent,
				OpeningBalance:      l.OpeningBalance,
				ClosingBalance:      l.ClosingBalance,
				Phone:               l.Phone,
				Email:               l.Email,
				Contact:             l.Contact,
				State:               l.State,
				Country:             l.Country,
				GSTIN:               l.GSTIN,
				GSTRegistrationType: l.GSTRegistrationType,
				PAN:                 l.PAN,
				MaintainBillByBill:  l.MaintainBillByBill,
				CreditPeriod:        l.CreditPeriod,
				CreditLimit:         l.CreditLimit,
			}, nil
		}
	}
	return nil, &LedgerNotFoundError{Query: ledgerInput}
}

func buildProjectedLedgerDetail(base *LedgerDetail, opts LedgerWriteOptions) *LedgerDetail {
	cp := *base
	if len(base.Address) > 0 {
		cp.Address = append([]string(nil), base.Address...)
	}
	if len(base.OpeningBillAllocations) > 0 {
		cp.OpeningBillAllocations = append([]BillAllocation(nil), base.OpeningBillAllocations...)
	}
	if strings.TrimSpace(opts.NewName) != "" {
		cp.Name = strings.TrimSpace(opts.NewName)
	}
	if strings.TrimSpace(opts.Parent) != "" {
		cp.Parent = NormalizeGroupAlias(opts.Parent)
	}
	if opts.OpeningBalance != nil {
		delta := round2(*opts.OpeningBalance - base.OpeningBalance)
		cp.OpeningBalance = round2(*opts.OpeningBalance)
		cp.ClosingBalance = round2(base.ClosingBalance + delta)
	}
	if opts.State != nil {
		cp.State = strings.TrimSpace(*opts.State)
	}
	if opts.Country != nil {
		cp.Country = strings.TrimSpace(*opts.Country)
	}
	if opts.GSTIN != nil {
		cp.GSTIN = strings.ToUpper(strings.TrimSpace(*opts.GSTIN))
		if cp.GSTIN != "" && cp.GSTRegistrationType == "" {
			cp.GSTRegistrationType = "Regular"
		}
	}
	if opts.GSTRegistrationType != nil {
		cp.GSTRegistrationType = strings.TrimSpace(*opts.GSTRegistrationType)
	}
	if opts.PAN != nil {
		cp.PAN = strings.ToUpper(strings.TrimSpace(*opts.PAN))
	}
	if opts.HasAddress {
		cp.Address = append([]string(nil), opts.Address...)
	}
	if opts.MaintainBillByBill != nil {
		cp.MaintainBillByBill = *opts.MaintainBillByBill
	}
	if opts.CreditPeriod != nil {
		cp.CreditPeriod = strings.TrimSpace(*opts.CreditPeriod)
	}
	if opts.ReplaceOpeningBills {
		cp.OpeningBillAllocations = append([]BillAllocation(nil), opts.OpeningBills...)
	}
	return &cp
}

// UpdateLedgerForCompany alters an existing ledger (including renaming, group reclassification, GSTIN/PAN/state/opening balance).
func (c *Client) UpdateLedgerForCompany(ctx context.Context, opts LedgerWriteOptions, dryRun bool) (*LedgerMutationResult, error) {
	before, err := c.fetchLedgerDetailFull(ctx, opts.Company, opts.Name)
	if err != nil {
		return nil, err
	}

	opts.Name = before.Name
	if strings.TrimSpace(opts.Parent) != "" {
		opts.Parent = NormalizeGroupAlias(opts.Parent)
	}

	newName := strings.TrimSpace(opts.NewName)
	if newName != "" && !strings.EqualFold(newName, before.Name) {
		if existing, lErr := c.ListLedgersForCompany(ctx, opts.Company, ""); lErr == nil {
			for _, l := range existing {
				if strings.EqualFold(strings.TrimSpace(l.Name), newName) {
					errMsg := fmt.Sprintf("cannot rename ledger %q to %q: target ledger name already exists", before.Name, l.Name)
					c.emitAudit(AuditEntry{
						Company:         opts.Company,
						Operation:       "update_ledger",
						DryRun:          dryRun,
						Status:          "rejected",
						AffectedLedgers: []string{before.Name, newName},
						Before:          before,
						Error:           errMsg,
					})
					return nil, fmt.Errorf("%s", errMsg)
				}
			}
		}
	}

	xmlPayload, err := BuildUpdateLedgerXML(opts)
	if err != nil {
		return nil, err
	}

	after := buildProjectedLedgerDetail(before, opts)
	var changes []string
	if after.Name != before.Name {
		changes = append(changes, fmt.Sprintf("name: %q -> %q", before.Name, after.Name))
	}
	if after.Parent != before.Parent {
		changes = append(changes, fmt.Sprintf("parent: %q -> %q", before.Parent, after.Parent))
	}
	if after.State != before.State {
		changes = append(changes, fmt.Sprintf("state: %q -> %q", before.State, after.State))
	}
	if after.GSTIN != before.GSTIN {
		changes = append(changes, fmt.Sprintf("gstin: %q -> %q", before.GSTIN, after.GSTIN))
	}
	if after.GSTRegistrationType != before.GSTRegistrationType {
		changes = append(changes, fmt.Sprintf("gst_registration_type: %q -> %q", before.GSTRegistrationType, after.GSTRegistrationType))
	}
	if after.PAN != before.PAN {
		changes = append(changes, fmt.Sprintf("pan: %q -> %q", before.PAN, after.PAN))
	}
	if after.OpeningBalance != before.OpeningBalance {
		changes = append(changes, fmt.Sprintf("opening_balance: %.2f -> %.2f", before.OpeningBalance, after.OpeningBalance))
	}
	if after.MaintainBillByBill != before.MaintainBillByBill {
		changes = append(changes, fmt.Sprintf("maintain_bill_by_bill: %v -> %v", before.MaintainBillByBill, after.MaintainBillByBill))
	}

	affectedLedgers := []string{before.Name}
	if after.Name != before.Name {
		affectedLedgers = append(affectedLedgers, after.Name)
	}

	res := &LedgerMutationResult{
		Status:  "preview",
		DryRun:  dryRun,
		Company: opts.Company,
		Ledger:  after.Name,
		Before:  before,
		After:   after,
		Changes: changes,
	}

	if dryRun {
		res.Hint = "Dry-run preview succeeded. Re-call tally_update_ledger with dry_run=false to apply changes in Tally."
		c.emitAudit(AuditEntry{
			Company:         opts.Company,
			Operation:       "update_ledger",
			DryRun:          true,
			Status:          "preview",
			AffectedLedgers: affectedLedgers,
			Before:          before,
			After:           after,
		})
		return res, nil
	}

	raw, err := c.ExecuteRawXML(ctx, xmlPayload)
	if err != nil {
		return nil, fmt.Errorf("updating ledger %q: %w", before.Name, err)
	}
	if err := checkTallyImportResponse(raw, "update ledger"); err != nil {
		return nil, err
	}

	c.InvalidateCache("ledgers:")
	c.InvalidateCache("vouchers:")
	res.Status = "updated"
	c.emitAudit(AuditEntry{
		Company:         opts.Company,
		Operation:       "update_ledger",
		DryRun:          false,
		Status:          "applied",
		AffectedLedgers: affectedLedgers,
		Before:          before,
		After:           after,
	})
	return res, nil
}

func (c *Client) getCompanyBookSpan(ctx context.Context, company string) (string, string) {
	booksFrom := "19000101"
	booksTo := "20991231"
	comps, err := c.ListCompanies(ctx)
	if err != nil || len(comps) == 0 {
		return booksFrom, booksTo
	}
	var chosen *CompanyItem
	if strings.TrimSpace(company) != "" {
		for i := range comps {
			if strings.EqualFold(strings.TrimSpace(comps[i].Name), strings.TrimSpace(company)) {
				chosen = &comps[i]
				break
			}
		}
	} else {
		for i := range comps {
			if strings.EqualFold(strings.TrimSpace(comps[i].IsActiveCompany), "Yes") {
				chosen = &comps[i]
				break
			}
		}
		if chosen == nil {
			chosen = &comps[0]
		}
	}
	if chosen != nil {
		if bf := CleanDate(chosen.BooksFrom); bf != "" {
			booksFrom = bf
		} else if sf := CleanDate(chosen.StartingFrom); sf != "" {
			booksFrom = sf
		}
		if ea := CleanDate(chosen.EndingAt); ea != "" {
			booksTo = ea
		}
	}
	return booksFrom, booksTo
}

func (c *Client) findVouchersTouchingLedger(ctx context.Context, company, ledgerName, booksFrom, booksTo string) ([]VoucherItem, error) {
	vouchers, err := c.GetLedgerStatementVouchersForCompany(ctx, company, ledgerName, booksFrom, booksTo)
	if err != nil {
		return nil, err
	}
	var matching []VoucherItem
	for _, v := range vouchers {
		cleanDate := CleanDate(v.Date)
		if booksFrom != "" && cleanDate != "" && cleanDate < booksFrom {
			continue
		}
		if booksTo != "" && cleanDate != "" && cleanDate > booksTo {
			continue
		}
		touches := strings.EqualFold(strings.TrimSpace(v.PartyLedgerName), ledgerName)
		if !touches {
			for _, e := range v.LedgerEntries {
				if strings.EqualFold(strings.TrimSpace(e.LedgerName), ledgerName) {
					touches = true
					break
				}
			}
		}
		if touches {
			matching = append(matching, v)
		}
	}
	return matching, nil
}

// DeleteLedgerForCompany safely deletes a ledger ONLY after verifying zero opening/closing balance and zero vouchers.
func (c *Client) DeleteLedgerForCompany(ctx context.Context, company, name string, dryRun bool) (*DeleteLedgerResult, error) {
	before, err := c.fetchLedgerDetailFull(ctx, company, name)
	if err != nil {
		return nil, err
	}

	booksFrom, booksTo := c.getCompanyBookSpan(ctx, company)
	vouchers, err := c.findVouchersTouchingLedger(ctx, company, before.Name, booksFrom, booksTo)
	if err != nil {
		return nil, fmt.Errorf("checking vouchers for ledger %q: %w", before.Name, err)
	}

	voucherCount := len(vouchers)
	if before.OpeningBalance != 0 || before.ClosingBalance != 0 || voucherCount > 0 {
		errMsg := fmt.Sprintf(
			"cannot delete ledger %q: ledger still has opening_balance=%.2f, closing_balance=%.2f, voucher_count=%d (in book period %s..%s); use tally_merge_ledgers first to migrate transactions and balances",
			before.Name, before.OpeningBalance, before.ClosingBalance, voucherCount, booksFrom, booksTo,
		)
		c.emitAudit(AuditEntry{
			Company:         company,
			Operation:       "delete_ledger",
			DryRun:          dryRun,
			Status:          "rejected",
			AffectedLedgers: []string{before.Name},
			Before:          before,
			Error:           errMsg,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	xmlPayload, err := BuildDeleteLedgerXML(company, before.Name)
	if err != nil {
		return nil, err
	}

	res := &DeleteLedgerResult{
		Status:         "preview",
		DryRun:         dryRun,
		Company:        company,
		Ledger:         before.Name,
		CanDelete:      true,
		VoucherCount:   0,
		OpeningBalance: 0,
		ClosingBalance: 0,
		BookSpan:       booksFrom + ".." + booksTo,
		Before:         before,
	}

	if dryRun {
		res.Hint = "Dry-run verification passed (0 vouchers, 0 balances). Re-call tally_delete_ledger with dry_run=false to delete this ledger."
		c.emitAudit(AuditEntry{
			Company:         company,
			Operation:       "delete_ledger",
			DryRun:          true,
			Status:          "preview",
			AffectedLedgers: []string{before.Name},
			Before:          before,
		})
		return res, nil
	}

	raw, err := c.ExecuteRawXML(ctx, xmlPayload)
	if err != nil {
		return nil, fmt.Errorf("deleting ledger %q: %w", before.Name, err)
	}
	if err := checkTallyImportResponse(raw, "delete ledger"); err != nil {
		return nil, err
	}

	c.InvalidateCache("ledgers:")
	res.Status = "deleted"
	c.emitAudit(AuditEntry{
		Company:         company,
		Operation:       "delete_ledger",
		DryRun:          false,
		Status:          "applied",
		AffectedLedgers: []string{before.Name},
		Before:          before,
	})
	return res, nil
}

// MergeLedgersForCompany safely merges source_ledger into target_ledger, migrating all vouchers and opening balances,
// verifying balances, and deleting source_ledger only after post-merge verification succeeds.
func (c *Client) MergeLedgersForCompany(ctx context.Context, company, sourceInput, targetInput string, requireSameGSTIN, dryRun bool) (*MergeLedgersResult, error) {
	src, err := c.fetchLedgerDetailFull(ctx, company, sourceInput)
	if err != nil {
		return nil, fmt.Errorf("source_ledger: %w", err)
	}
	tgt, err := c.fetchLedgerDetailFull(ctx, company, targetInput)
	if err != nil {
		return nil, fmt.Errorf("target_ledger: %w", err)
	}
	if strings.EqualFold(src.Name, tgt.Name) {
		return nil, fmt.Errorf("source_ledger and target_ledger cannot be the same (%q)", src.Name)
	}

	if requireSameGSTIN {
		srcG := strings.ToUpper(strings.TrimSpace(src.GSTIN))
		tgtG := strings.ToUpper(strings.TrimSpace(tgt.GSTIN))
		if srcG == "" || tgtG == "" || !strings.EqualFold(srcG, tgtG) {
			errMsg := fmt.Sprintf(
				"GSTIN verification failed (require_same_gstin=true): source_ledger %q has gstin=%q, target_ledger %q has gstin=%q (pass require_same_gstin=false only for verified missing-GSTIN typos)",
				src.Name, srcG, tgt.Name, tgtG,
			)
			c.emitAudit(AuditEntry{
				Company:         company,
				Operation:       "merge_ledgers",
				DryRun:          dryRun,
				Status:          "rejected",
				AffectedLedgers: []string{src.Name, tgt.Name},
				Error:           errMsg,
			})
			return nil, fmt.Errorf("%s", errMsg)
		}
	}

	booksFrom, booksTo := c.getCompanyBookSpan(ctx, company)
	vouchers, err := c.findVouchersTouchingLedger(ctx, company, src.Name, booksFrom, booksTo)
	if err != nil {
		return nil, fmt.Errorf("inspecting vouchers for source_ledger %q: %w", src.Name, err)
	}

	var masterIDs []string
	var minDate, maxDate string
	for _, v := range vouchers {
		if v.MasterID != "" {
			masterIDs = append(masterIDs, v.MasterID)
		}
		cd := CleanDate(v.Date)
		if cd != "" {
			if minDate == "" || cd < minDate {
				minDate = cd
			}
			if maxDate == "" || cd > maxDate {
				maxDate = cd
			}
		}
	}
	dateSpan := ""
	if minDate != "" {
		dateSpan = minDate + ".." + maxDate
	}

	expectedTargetClosing := round2(src.ClosingBalance + tgt.ClosingBalance)
	res := &MergeLedgersResult{
		Status:                       "preview",
		DryRun:                       dryRun,
		Company:                      company,
		SourceLedger:                 src.Name,
		TargetLedger:                 tgt.Name,
		RequireSameGSTIN:             requireSameGSTIN,
		SourceGSTIN:                  src.GSTIN,
		TargetGSTIN:                  tgt.GSTIN,
		VoucherCount:                 len(vouchers),
		AffectedMasterIDs:            masterIDs,
		DateSpan:                     dateSpan,
		OpeningBalanceDelta:          round2(src.OpeningBalance),
		OpeningBillsMoved:            len(src.OpeningBillAllocations),
		SourceClosingBalance:         round2(src.ClosingBalance),
		TargetClosingBalanceBefore:   round2(tgt.ClosingBalance),
		ExpectedTargetClosingBalance: expectedTargetClosing,
	}

	beforeState := map[string]any{
		"source": src,
		"target": tgt,
	}

	if dryRun {
		res.Hint = "Dry-run merge plan generated without modifying Tally. Re-call tally_merge_ledgers with dry_run=false to execute."
		c.emitAudit(AuditEntry{
			Company:           company,
			Operation:         "merge_ledgers",
			DryRun:            true,
			Status:            "preview",
			AffectedMasterIDs: masterIDs,
			AffectedLedgers:   []string{src.Name, tgt.Name},
			Before:            beforeState,
			After:             res,
		})
		return res, nil
	}

	// Step 5a: Alter each affected voucher from source_ledger -> target_ledger
	alteredCount := 0
	for _, v := range vouchers {
		fullV, gErr := c.GetVoucherForCompany(ctx, company, v.MasterID, v.VoucherNumber, v.VoucherTypeName, v.Date)
		if gErr != nil {
			return nil, fmt.Errorf("fetching full voucher (master_id=%q, number=%q) for merge: %w", v.MasterID, v.VoucherNumber, gErr)
		}

		newParty := fullV.PartyLedgerName
		if strings.EqualFold(strings.TrimSpace(newParty), src.Name) {
			newParty = tgt.Name
		}

		var entries []VoucherEntryInput
		for _, leg := range fullV.LedgerEntries {
			lName := leg.LedgerName
			if strings.EqualFold(strings.TrimSpace(lName), src.Name) {
				lName = tgt.Name
			}
			var bills []BillAllocationInput
			for _, b := range leg.BillAllocations {
				bills = append(bills, BillAllocationInput{
					Name:     b.Name,
					BillType: b.BillType,
					Amount:   round2(b.Amount),
				})
			}
			isDebit := leg.Amount < 0
			deemedPos := "No"
			if isDebit {
				deemedPos = "Yes"
			}
			entries = append(entries, VoucherEntryInput{
				LedgerName:       lName,
				Amount:           round2(leg.Amount),
				IsDebit:          isDebit,
				IsDeemedPositive: deemedPos,
				BillAllocations:  bills,
				BankAllocations:  leg.BankAllocations,
			})
		}

		var invEntries []InventoryEntryInput
		for _, inv := range fullV.InventoryEntries {
			isDebit := inv.Amount < 0
			deemedPos := "No"
			if isDebit {
				deemedPos = "Yes"
			}
			var godown, batch string
			if len(inv.BatchAllocations) > 0 {
				godown = inv.BatchAllocations[0].GodownName
				batch = inv.BatchAllocations[0].BatchName
			}
			invEntries = append(invEntries, InventoryEntryInput{
				StockItemName:    inv.StockItemName,
				Rate:             inv.Rate,
				BilledQty:        inv.BilledQty,
				ActualQty:        inv.ActualQty,
				Amount:           round2(inv.Amount),
				IsDebit:          isDebit,
				IsDeemedPositive: deemedPos,
				GodownName:       godown,
				BatchName:        batch,
			})
		}

		alterXML, err := BuildAlterVoucherXML(
			company, "Alter", fullV.MasterID, fullV.VoucherTypeName,
			CleanDate(fullV.Date), fullV.VoucherNumber, fullV.Reference,
			newParty, fullV.Narration, false, entries, invEntries,
		)
		if err != nil {
			return nil, fmt.Errorf("building alter XML for voucher %q: %w", fullV.MasterID, err)
		}
		rawResp, err := c.ExecuteRawXML(ctx, alterXML)
		if err != nil {
			return nil, fmt.Errorf("altering voucher %q during merge: %w", fullV.MasterID, err)
		}
		if err := checkTallyImportResponse(rawResp, "merge voucher alter"); err != nil {
			return nil, err
		}
		alteredCount++
	}
	res.AlteredVouchers = alteredCount

	// Step 5b: Transfer non-zero opening balance & opening bill allocations
	if src.OpeningBalance != 0 || len(src.OpeningBillAllocations) > 0 {
		combinedOpening := round2(tgt.OpeningBalance + src.OpeningBalance)
		combinedBills := make([]BillAllocation, 0, len(tgt.OpeningBillAllocations)+len(src.OpeningBillAllocations))
		combinedBills = append(combinedBills, tgt.OpeningBillAllocations...)
		combinedBills = append(combinedBills, src.OpeningBillAllocations...)

		tgtXML, err := BuildUpdateLedgerXML(LedgerWriteOptions{
			Company:             company,
			Name:                tgt.Name,
			OpeningBalance:      &combinedOpening,
			OpeningBills:        combinedBills,
			ReplaceOpeningBills: len(combinedBills) > 0,
		})
		if err != nil {
			return nil, fmt.Errorf("building opening balance transfer XML for %q: %w", tgt.Name, err)
		}
		rawTgt, err := c.ExecuteRawXML(ctx, tgtXML)
		if err != nil {
			return nil, fmt.Errorf("updating target opening balance on %q: %w", tgt.Name, err)
		}
		if err := checkTallyImportResponse(rawTgt, "transfer opening balance"); err != nil {
			return nil, err
		}

		zero := 0.0
		srcClearXML, err := BuildUpdateLedgerXML(LedgerWriteOptions{
			Company:             company,
			Name:                src.Name,
			OpeningBalance:      &zero,
			OpeningBills:        nil,
			ReplaceOpeningBills: len(src.OpeningBillAllocations) > 0,
		})
		if err != nil {
			return nil, fmt.Errorf("building opening balance clear XML for %q: %w", src.Name, err)
		}
		rawSrc, err := c.ExecuteRawXML(ctx, srcClearXML)
		if err != nil {
			return nil, fmt.Errorf("clearing source opening balance on %q: %w", src.Name, err)
		}
		if err := checkTallyImportResponse(rawSrc, "clear source opening balance"); err != nil {
			return nil, err
		}
	}

	// Step 5c: Re-query and verify before deleting source_ledger
	c.InvalidateCache("vouchers:")
	c.InvalidateCache("ledgers:")

	postSrc, err := c.fetchLedgerDetailFull(ctx, company, src.Name)
	if err != nil {
		return nil, fmt.Errorf("post-merge verification failed querying source %q: %w", src.Name, err)
	}
	postSrcVouchers, err := c.findVouchersTouchingLedger(ctx, company, src.Name, booksFrom, booksTo)
	if err != nil {
		return nil, fmt.Errorf("post-merge verification failed querying source vouchers for %q: %w", src.Name, err)
	}
	if len(postSrcVouchers) != 0 || postSrc.OpeningBalance != 0 || postSrc.ClosingBalance != 0 {
		return nil, fmt.Errorf(
			"post-merge verification failed: source_ledger %q still has vouchers=%d, opening=%.2f, closing=%.2f; source_ledger was NOT deleted",
			src.Name, len(postSrcVouchers), postSrc.OpeningBalance, postSrc.ClosingBalance,
		)
	}

	postTgt, err := c.fetchLedgerDetailFull(ctx, company, tgt.Name)
	if err != nil {
		return nil, fmt.Errorf("post-merge verification failed querying target %q: %w", tgt.Name, err)
	}
	if math.Abs(postTgt.ClosingBalance-expectedTargetClosing) > 0.01 {
		return nil, fmt.Errorf(
			"post-merge balance verification failed: target %q closing_balance=%.2f != expected %.2f (source_old=%.2f + target_old=%.2f); source_ledger was NOT deleted",
			tgt.Name, postTgt.ClosingBalance, expectedTargetClosing, src.ClosingBalance, tgt.ClosingBalance,
		)
	}
	verifiedClosing := round2(postTgt.ClosingBalance)
	res.VerifiedTargetClosingBalance = &verifiedClosing

	// Step 5d: Delete verified empty source_ledger
	delXML, err := BuildDeleteLedgerXML(company, src.Name)
	if err != nil {
		return nil, err
	}
	rawDel, err := c.ExecuteRawXML(ctx, delXML)
	if err != nil {
		return nil, fmt.Errorf("deleting verified empty source ledger %q: %w", src.Name, err)
	}
	if err := checkTallyImportResponse(rawDel, "delete merged source ledger"); err != nil {
		return nil, err
	}

	c.InvalidateCache("vouchers:")
	c.InvalidateCache("ledgers:")

	res.Status = "merged"
	res.SourceDeleted = true
	c.emitAudit(AuditEntry{
		Company:           company,
		Operation:         "merge_ledgers",
		DryRun:            false,
		Status:            "applied",
		AffectedMasterIDs: masterIDs,
		AffectedLedgers:   []string{src.Name, tgt.Name},
		Before:            beforeState,
		After:             res,
	})
	return res, nil
}

// UpdateVoucherForCompany applies targeted edits (date, voucher_number, reference, narration, entries, or reclassify_ledger)
// to an existing voucher identified by master_id, validates debit==credit balance, and supports dry_run.
func (c *Client) UpdateVoucherForCompany(ctx context.Context, opts UpdateVoucherOptions) (*UpdateVoucherResult, error) {
	existing, err := c.GetVoucherForCompany(ctx, opts.Company, opts.MasterID, opts.VoucherNumber, opts.VoucherType, opts.Date)
	if err != nil {
		return nil, fmt.Errorf("locating voucher to update: %w", err)
	}

	before := voucherDetailToSnapshot(existing)

	targetDate := CleanDate(opts.NewDate)
	if targetDate == "" {
		targetDate = CleanDate(existing.Date)
	}
	targetNumber := strings.TrimSpace(opts.NewVoucherNumber)
	if targetNumber == "" {
		targetNumber = existing.VoucherNumber
	}
	targetRef := existing.Reference
	if opts.HasReference || strings.TrimSpace(opts.Reference) != "" {
		targetRef = strings.TrimSpace(opts.Reference)
	}
	targetNarr := existing.Narration
	if opts.HasNarration || strings.TrimSpace(opts.Narration) != "" {
		targetNarr = strings.TrimSpace(opts.Narration)
	}
	targetParty := strings.TrimSpace(opts.PartyLedger)

	var entriesInput []VoucherEntryInput
	if len(opts.Entries) > 0 {
		entriesInput = opts.Entries
	} else {
		for _, leg := range existing.LedgerEntries {
			var bills []BillAllocationInput
			for _, b := range leg.BillAllocations {
				bills = append(bills, BillAllocationInput{
					Name:     b.Name,
					BillType: b.BillType,
					Amount:   b.Amount,
				})
			}
			entriesInput = append(entriesInput, VoucherEntryInput{
				LedgerName:      leg.LedgerName,
				Amount:          leg.Amount,
				IsDebit:         leg.Amount < 0,
				BillAllocations: bills,
				BankAllocations: leg.BankAllocations,
			})
		}
	}

	var affectedLedgers []string
	if opts.Reclassify != nil && strings.TrimSpace(opts.Reclassify.FromLedger) != "" {
		fromName := strings.TrimSpace(opts.Reclassify.FromLedger)
		if resolvedFrom, _, rErr := c.ResolveLedgerName(ctx, opts.Company, fromName); rErr == nil {
			fromName = resolvedFrom
		}
		toRaw := strings.TrimSpace(opts.Reclassify.ToLedger)
		if toRaw == "" {
			return nil, fmt.Errorf("reclassify_ledger.to_ledger cannot be empty")
		}
		toName, _, tErr := c.ResolveLedgerName(ctx, opts.Company, toRaw)
		if tErr != nil {
			return nil, fmt.Errorf("reclassify_ledger target: %w", tErr)
		}

		replaced := false
		for i := range entriesInput {
			if strings.EqualFold(strings.TrimSpace(entriesInput[i].LedgerName), fromName) ||
				strings.EqualFold(strings.TrimSpace(entriesInput[i].LedgerName), strings.TrimSpace(opts.Reclassify.FromLedger)) {
				entriesInput[i].LedgerName = toName
				if len(opts.Reclassify.BillAllocations) > 0 {
					entriesInput[i].BillAllocations = opts.Reclassify.BillAllocations
				}
				replaced = true
			}
		}
		if !replaced {
			return nil, fmt.Errorf("reclassify_ledger: ledger %q not found in voucher master_id=%q entries", opts.Reclassify.FromLedger, existing.MasterID)
		}
		if targetParty == "" && (strings.EqualFold(strings.TrimSpace(existing.PartyLedgerName), fromName) ||
			strings.EqualFold(strings.TrimSpace(existing.PartyLedgerName), strings.TrimSpace(opts.Reclassify.FromLedger))) {
			targetParty = toName
		}
		affectedLedgers = append(affectedLedgers, fromName, toName)
	}

	if len(entriesInput) < 2 {
		return nil, fmt.Errorf("voucher must have at least 2 ledger entries")
	}

	resolvedEntries := make([]VoucherEntryInput, 0, len(entriesInput))
	var totalDebit, totalCredit float64
	for i, e := range entriesInput {
		resolvedName, correctedFrom, rErr := c.ResolveLedgerName(ctx, opts.Company, e.LedgerName)
		if rErr != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, rErr)
		}
		if e.Amount == 0 {
			return nil, fmt.Errorf("entry[%d] (%s) amount cannot be 0", i, resolvedName)
		}
		absAmt := math.Abs(e.Amount)
		isDebit := e.IsDebit || e.Amount < 0
		signedAmt := absAmt
		deemedPos := "No"
		if isDebit {
			signedAmt = -absAmt
			deemedPos = "Yes"
			totalDebit += absAmt
		} else {
			totalCredit += absAmt
		}

		var resolvedBills []BillAllocationInput
		for bi, b := range e.BillAllocations {
			bName := strings.TrimSpace(b.Name)
			if bName == "" {
				bName = strings.TrimSpace(b.BillName)
			}
			if bName == "" {
				return nil, fmt.Errorf("entry[%d].bill_allocations[%d] name/bill_name cannot be empty", i, bi)
			}
			bType := strings.TrimSpace(b.BillType)
			if bType == "" {
				bType = "Agst Ref"
			}
			bAmt := b.Amount
			if bAmt == 0 {
				bAmt = absAmt
			}
			bSigned := math.Abs(bAmt)
			if isDebit {
				bSigned = -bSigned
			}
			resolvedBills = append(resolvedBills, BillAllocationInput{
				Name:     bName,
				BillType: bType,
				Amount:   round2(bSigned),
			})
		}

		resolvedEntries = append(resolvedEntries, VoucherEntryInput{
			LedgerName:       resolvedName,
			ResolvedFrom:     correctedFrom,
			Amount:           round2(signedAmt),
			IsDebit:          isDebit,
			IsDeemedPositive: deemedPos,
			BillAllocations:  resolvedBills,
			BankAllocations:  e.BankAllocations,
		})
	}

	totalDebit = round2(totalDebit)
	totalCredit = round2(totalCredit)
	if math.Abs(totalDebit-totalCredit) > 0.01 {
		return nil, fmt.Errorf("updated voucher is unbalanced: total_debit=%.2f != total_credit=%.2f", totalDebit, totalCredit)
	}

	if targetParty != "" {
		pName, _, pErr := c.ResolveLedgerName(ctx, opts.Company, targetParty)
		if pErr != nil {
			return nil, fmt.Errorf("party_ledger: %w", pErr)
		}
		targetParty = pName
	} else {
		targetParty = existing.PartyLedgerName
		if targetParty == "" && !strings.EqualFold(existing.VoucherTypeName, "Contra") && !strings.EqualFold(existing.VoucherTypeName, "Journal") {
			targetParty = resolvedEntries[0].LedgerName
		}
	}

	var invEntries []InventoryEntryInput
	for _, inv := range existing.InventoryEntries {
		isDebit := inv.Amount < 0
		deemedPos := "No"
		if isDebit {
			deemedPos = "Yes"
		}
		var godown, batch string
		if len(inv.BatchAllocations) > 0 {
			godown = inv.BatchAllocations[0].GodownName
			batch = inv.BatchAllocations[0].BatchName
		}
		invEntries = append(invEntries, InventoryEntryInput{
			StockItemName:    inv.StockItemName,
			Rate:             inv.Rate,
			BilledQty:        inv.BilledQty,
			ActualQty:        inv.ActualQty,
			Amount:           round2(inv.Amount),
			IsDebit:          isDebit,
			IsDeemedPositive: deemedPos,
			GodownName:       godown,
			BatchName:        batch,
		})
	}

	xmlPayload, err := BuildAlterVoucherXML(
		opts.Company, "Alter", existing.MasterID, existing.VoucherTypeName,
		targetDate, targetNumber, targetRef, targetParty, targetNarr, false, resolvedEntries, invEntries,
	)
	if err != nil {
		return nil, err
	}

	after := &VoucherSnapshot{
		MasterID:      existing.MasterID,
		VoucherType:   existing.VoucherTypeName,
		VoucherNumber: targetNumber,
		Date:          targetDate,
		Reference:     targetRef,
		PartyLedger:   targetParty,
		Narration:     targetNarr,
		TotalDebit:    totalDebit,
		TotalCredit:   totalCredit,
		Entries:       resolvedEntries,
	}

	if len(affectedLedgers) == 0 {
		affectedLedgers = collectEntryLedgerNames(resolvedEntries)
	}

	res := &UpdateVoucherResult{
		Status:   "preview",
		DryRun:   opts.DryRun,
		Company:  opts.Company,
		MasterID: existing.MasterID,
		Before:   before,
		After:    after,
	}

	if opts.DryRun {
		res.Hint = "Dry-run preview succeeded. Re-call tally_update_voucher with dry_run=false to apply changes in Tally."
		c.emitAudit(AuditEntry{
			Company:           opts.Company,
			Operation:         "update_voucher",
			DryRun:            true,
			Status:            "preview",
			AffectedMasterIDs: []string{existing.MasterID},
			AffectedLedgers:   affectedLedgers,
			Before:            before,
			After:             after,
		})
		return res, nil
	}

	rawResp, err := c.ExecuteRawXML(ctx, xmlPayload)
	if err != nil {
		return nil, fmt.Errorf("executing Tally voucher update: %w", err)
	}
	if err := checkTallyImportResponse(rawResp, "update voucher"); err != nil {
		return nil, err
	}

	c.InvalidateCache("vouchers:")
	c.InvalidateCache("ledgers:")
	res.Status = "updated"
	c.emitAudit(AuditEntry{
		Company:           opts.Company,
		Operation:         "update_voucher",
		DryRun:            false,
		Status:            "applied",
		AffectedMasterIDs: []string{existing.MasterID},
		AffectedLedgers:   affectedLedgers,
		Before:            before,
		After:             after,
	})
	return res, nil
}

// DeleteVoucherForCompany deletes a single voucher by master_id, supporting dry_run=true to inspect full voucher details first.
func (c *Client) DeleteVoucherForCompany(ctx context.Context, company, masterID string, dryRun bool) (*DeleteVoucherResult, error) {
	cleanID := strings.TrimSpace(masterID)
	if cleanID == "" {
		return nil, fmt.Errorf("master_id is required to delete a voucher")
	}

	existing, err := c.GetVoucherForCompany(ctx, company, cleanID, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("locating voucher to delete (master_id=%q): %w", cleanID, err)
	}

	snapshot := voucherDetailToSnapshot(existing)
	cleanDate := CleanDate(existing.Date)
	xmlPayload, err := BuildAlterVoucherXML(
		company, "Delete", existing.MasterID, existing.VoucherTypeName,
		cleanDate, existing.VoucherNumber, existing.Reference, existing.PartyLedgerName, existing.Narration, false, nil, nil,
	)
	if err != nil {
		return nil, err
	}

	res := &DeleteVoucherResult{
		Status:   "preview",
		DryRun:   dryRun,
		Company:  company,
		MasterID: existing.MasterID,
		Voucher:  snapshot,
	}

	if dryRun {
		res.Hint = "Dry-run preview succeeded. Re-call tally_delete_voucher with dry_run=false to delete this voucher from Tally."
		c.emitAudit(AuditEntry{
			Company:           company,
			Operation:         "delete_voucher",
			DryRun:            true,
			Status:            "preview",
			AffectedMasterIDs: []string{existing.MasterID},
			AffectedLedgers:   collectEntryLedgerNames(snapshot.Entries),
			Before:            snapshot,
		})
		return res, nil
	}

	rawResp, err := c.ExecuteRawXML(ctx, xmlPayload)
	if err != nil {
		return nil, fmt.Errorf("executing Tally voucher delete: %w", err)
	}
	if err := checkTallyImportResponse(rawResp, "delete voucher"); err != nil {
		return nil, err
	}

	c.InvalidateCache("vouchers:")
	c.InvalidateCache("ledgers:")
	res.Status = "deleted"
	c.emitAudit(AuditEntry{
		Company:           company,
		Operation:         "delete_voucher",
		DryRun:            false,
		Status:            "applied",
		AffectedMasterIDs: []string{existing.MasterID},
		AffectedLedgers:   collectEntryLedgerNames(snapshot.Entries),
		Before:            snapshot,
	})
	return res, nil
}
