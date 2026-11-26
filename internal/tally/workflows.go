package tally

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// CreateVoucherForCompany validates, resolves ledgers, checks for duplicates, and either previews or imports a voucher into Tally.
func (c *Client) CreateVoucherForCompany(
	ctx context.Context,
	company, voucherType, date, voucherNumber, reference, partyLedger, narration string,
	rawEntries []VoucherEntryInput,
	rawInventory []InventoryEntryInput,
	preview, allowDuplicate bool,
) (*CreateVoucherResult, error) {
	vType := NormalizeVoucherTypeAlias(voucherType)
	if vType == "" {
		return nil, fmt.Errorf("voucher_type cannot be empty")
	}
	cleanDate := CleanDate(date)
	if cleanDate == "" {
		cleanDate = time.Now().Format("20060102")
	}
	if len(rawEntries) < 2 {
		return nil, fmt.Errorf("at least 2 ledger entries are required (debit and credit)")
	}

	resolvedEntries := make([]VoucherEntryInput, 0, len(rawEntries))
	var totalDebit, totalCredit float64

	for i, e := range rawEntries {
		name := strings.TrimSpace(e.LedgerName)
		if name == "" {
			return nil, fmt.Errorf("entry[%d] ledger_name cannot be empty", i)
		}
		if e.Amount == 0 {
			return nil, fmt.Errorf("entry[%d] (%s) amount cannot be 0", i, name)
		}

		resolvedName, correctedFrom, rErr := c.ResolveLedgerName(ctx, company, name)
		if rErr != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, rErr)
		}

		absAmt := math.Abs(e.Amount)
		isDebit := e.IsDebit
		if !isDebit && e.Amount < 0 {
			isDebit = true
		}

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
				return nil, fmt.Errorf("entry[%d].bill_allocations[%d] name cannot be empty", i, bi)
			}
			if b.Amount == 0 {
				return nil, fmt.Errorf("entry[%d].bill_allocations[%d] (%s) amount cannot be 0", i, bi, bName)
			}
			bType := strings.TrimSpace(b.BillType)
			if bType == "" {
				bType = "New Ref"
			}
			bSigned := math.Abs(b.Amount)
			if isDebit {
				bSigned = -bSigned
			}
			resolvedBills = append(resolvedBills, BillAllocationInput{
				Name:     bName,
				BillType: bType,
				Amount:   math.Round(bSigned*100) / 100,
			})
		}

		resolvedEntries = append(resolvedEntries, VoucherEntryInput{
			LedgerName:       resolvedName,
			ResolvedFrom:     correctedFrom,
			Amount:           math.Round(signedAmt*100) / 100,
			IsDebit:          isDebit,
			IsDeemedPositive: deemedPos,
			BillAllocations:  resolvedBills,
		})
	}

	totalDebit = math.Round(totalDebit*100) / 100
	totalCredit = math.Round(totalCredit*100) / 100
	if math.Abs(totalDebit-totalCredit) > 0.01 {
		return nil, fmt.Errorf("voucher is unbalanced: total_debit=%.2f != total_credit=%.2f (difference=%.2f)", totalDebit, totalCredit, math.Abs(totalDebit-totalCredit))
	}

	resolvedInv := make([]InventoryEntryInput, 0, len(rawInventory))
	for i, inv := range rawInventory {
		itemName := strings.TrimSpace(inv.StockItemName)
		if itemName == "" {
			return nil, fmt.Errorf("inventory_entries[%d] stock_item_name cannot be empty", i)
		}
		if inv.Amount == 0 {
			return nil, fmt.Errorf("inventory_entries[%d] (%s) amount cannot be 0", i, itemName)
		}
		isDebit := inv.IsDebit || inv.Amount < 0
		if !inv.IsDebit && inv.Amount > 0 && strings.EqualFold(vType, "Purchase") {
			isDebit = true
		}
		signedAmt := math.Abs(inv.Amount)
		deemedPos := "No"
		if isDebit {
			signedAmt = -signedAmt
			deemedPos = "Yes"
		}
		actualQty := strings.TrimSpace(inv.ActualQty)
		billedQty := strings.TrimSpace(inv.BilledQty)
		if actualQty == "" {
			actualQty = billedQty
		}
		resolvedInv = append(resolvedInv, InventoryEntryInput{
			StockItemName:    itemName,
			Rate:             strings.TrimSpace(inv.Rate),
			BilledQty:        billedQty,
			ActualQty:        actualQty,
			Amount:           math.Round(signedAmt*100) / 100,
			IsDebit:          isDebit,
			IsDeemedPositive: deemedPos,
			GodownName:       strings.TrimSpace(inv.GodownName),
			BatchName:        strings.TrimSpace(inv.BatchName),
		})
	}

	resolvedParty := strings.TrimSpace(partyLedger)
	if resolvedParty != "" {
		pName, _, pErr := c.ResolveLedgerName(ctx, company, resolvedParty)
		if pErr != nil {
			return nil, fmt.Errorf("party_ledger: %w", pErr)
		}
		resolvedParty = pName
	} else {
		resolvedParty = resolvedEntries[0].LedgerName
	}

	var duplicateMsg string
	vNum := strings.TrimSpace(voucherNumber)
	ref := strings.TrimSpace(reference)
	if !allowDuplicate && (vNum != "" || ref != "") {
		existing, fErr := c.FilterVouchers(ctx, VoucherFilterOptions{
			Company:     company,
			VoucherType: vType,
			FromDate:    cleanDate,
			ToDate:      cleanDate,
		})
		if fErr == nil {
			for _, ex := range existing {
				if vNum != "" && strings.EqualFold(strings.TrimSpace(ex.VoucherNumber), vNum) {
					duplicateMsg = fmt.Sprintf("duplicate %s voucher detected: voucher_number=%q already exists on %s (party=%q)", vType, ex.VoucherNumber, cleanDate, ex.PartyLedgerName)
					break
				}
				if ref != "" && strings.EqualFold(strings.TrimSpace(ex.Reference), ref) && strings.EqualFold(strings.TrimSpace(ex.PartyLedgerName), resolvedParty) {
					duplicateMsg = fmt.Sprintf("duplicate %s voucher detected: reference=%q for party=%q already exists on %s", vType, ex.Reference, resolvedParty, cleanDate)
					break
				}
			}
		}
	}

	if duplicateMsg != "" && !preview {
		return nil, fmt.Errorf("%s (pass allow_duplicate=true to override)", duplicateMsg)
	}

	xmlPayload, err := BuildCreateVoucherXML(company, vType, cleanDate, vNum, ref, resolvedParty, narration, resolvedEntries, resolvedInv)
	if err != nil {
		return nil, err
	}

	result := &CreateVoucherResult{
		Status:           "preview",
		Valid:            true,
		Company:          company,
		VoucherType:      vType,
		Date:             cleanDate,
		VoucherNumber:    vNum,
		Reference:        ref,
		PartyLedger:      resolvedParty,
		Narration:        narration,
		TotalDebit:       totalDebit,
		TotalCredit:      totalCredit,
		Entries:          resolvedEntries,
		InventoryEntries: resolvedInv,
		DuplicateWarning: duplicateMsg,
	}

	if preview {
		result.Hint = "Dry-run preview succeeded. Re-call tally_create_voucher with preview=false to post this voucher to Tally."
		return result, nil
	}

	rawResp, err := c.ExecuteRawXML(ctx, xmlPayload)
	if err != nil {
		return nil, fmt.Errorf("executing Tally voucher import: %w", err)
	}
	if imp := ParseImportResult(rawResp); imp != nil {
		if imp.Errors > 0 || imp.Exceptions > 0 || imp.LineError != "" {
			return nil, fmt.Errorf("Tally reported import error: errors=%d exceptions=%d line_error=%q", imp.Errors, imp.Exceptions, imp.LineError)
		}
	}

	c.InvalidateCache("vouchers:")
	c.InvalidateCache("ledgers:")
	result.Status = "created"
	return result, nil
}

func classifyGSTLedger(ledgerName string) string {
	lower := strings.ToLower(strings.TrimSpace(ledgerName))
	switch {
	case strings.Contains(lower, "cgst"):
		return "cgst"
	case strings.Contains(lower, "sgst") || strings.Contains(lower, "utgst"):
		return "sgst"
	case strings.Contains(lower, "igst"):
		return "igst"
	case strings.Contains(lower, "cess"):
		return "cess"
	default:
		return ""
	}
}

func addGSTComponent(t *GSTComponentTotals, kind string, amt float64) {
	switch kind {
	case "cgst":
		t.CGST += amt
	case "sgst":
		t.SGST += amt
	case "igst":
		t.IGST += amt
	case "cess":
		t.Cess += amt
	}
}

func finalizeGSTTotals(t *GSTComponentTotals) {
	t.CGST = math.Round(t.CGST*100) / 100
	t.SGST = math.Round(t.SGST*100) / 100
	t.IGST = math.Round(t.IGST*100) / 100
	t.Cess = math.Round(t.Cess*100) / 100
	t.Total = math.Round((t.CGST+t.SGST+t.IGST+t.Cess)*100) / 100
}

// GetGSTSummaryForCompany aggregates Output GST, Input Tax Credit, Net Payable, and missing GSTIN flags.
func (c *Client) GetGSTSummaryForCompany(ctx context.Context, company, fromDate, toDate string) (*GSTSummary, error) {
	cleanFrom, cleanTo := ResolveDateRange(fromDate, toDate, time.Now())
	if cleanFrom == "" {
		cleanFrom = CurrentFinancialYearStart(time.Now())
	}
	if cleanTo == "" {
		cleanTo = time.Now().Format("20060102")
	}

	vouchers, err := c.FilterVouchers(ctx, VoucherFilterOptions{
		Company:        company,
		FromDate:       cleanFrom,
		ToDate:         cleanTo,
		IncludeEntries: true,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching vouchers for GST summary: %w", err)
	}

	partyGSTIN := make(map[string]string)
	if ledgers, lErr := c.ListLedgersForCompany(ctx, company, ""); lErr == nil {
		for _, l := range ledgers {
			partyGSTIN[strings.ToLower(strings.TrimSpace(l.Name))] = strings.TrimSpace(l.GSTIN)
		}
	}

	summary := &GSTSummary{
		Company:  company,
		FromDate: cleanFrom,
		ToDate:   cleanTo,
	}
	missingPartiesSet := make(map[string]bool)

	for _, v := range vouchers {
		vt := strings.ToLower(strings.TrimSpace(v.VoucherTypeName))
		isSales := strings.Contains(vt, "sales") || strings.Contains(vt, "debit note")
		isPurchase := strings.Contains(vt, "purchase") || strings.Contains(vt, "credit note")
		if !isSales && !isPurchase {
			continue
		}

		amt := math.Abs(v.Amount)
		if isSales {
			summary.SalesVouchers++
			summary.TotalSalesValue += amt
		} else {
			summary.PurchaseVouchers++
			summary.TotalPurchaseValue += amt
		}

		party := strings.TrimSpace(v.PartyLedgerName)
		if party != "" && !strings.EqualFold(party, "Cash") {
			if gstin, known := partyGSTIN[strings.ToLower(party)]; known && gstin == "" {
				summary.MissingGSTINVouchers++
				missingPartiesSet[party] = true
			}
		}

		for _, leg := range v.LedgerEntries {
			kind := classifyGSTLedger(leg.LedgerName)
			if kind == "" {
				continue
			}
			taxAmt := math.Abs(leg.Amount)
			if isSales {
				addGSTComponent(&summary.OutputTax, kind, taxAmt)
			} else {
				addGSTComponent(&summary.InputTaxCredit, kind, taxAmt)
			}
		}
	}

	summary.TotalSalesValue = math.Round(summary.TotalSalesValue*100) / 100
	summary.TotalPurchaseValue = math.Round(summary.TotalPurchaseValue*100) / 100
	finalizeGSTTotals(&summary.OutputTax)
	finalizeGSTTotals(&summary.InputTaxCredit)

	summary.NetGSTPayable = GSTComponentTotals{
		CGST: math.Round((summary.OutputTax.CGST-summary.InputTaxCredit.CGST)*100) / 100,
		SGST: math.Round((summary.OutputTax.SGST-summary.InputTaxCredit.SGST)*100) / 100,
		IGST: math.Round((summary.OutputTax.IGST-summary.InputTaxCredit.IGST)*100) / 100,
		Cess: math.Round((summary.OutputTax.Cess-summary.InputTaxCredit.Cess)*100) / 100,
	}
	summary.NetGSTPayable.Total = math.Round((summary.OutputTax.Total-summary.InputTaxCredit.Total)*100) / 100

	for p := range missingPartiesSet {
		summary.MissingGSTINParties = append(summary.MissingGSTINParties, p)
	}
	sort.Strings(summary.MissingGSTINParties)
	if len(summary.MissingGSTINParties) > 25 {
		summary.MissingGSTINParties = summary.MissingGSTINParties[:25]
	}

	return summary, nil
}

// GetBankReconForCompany inspects bank vouchers and BANKALLOCATIONS.LIST to report reconciled vs unreconciled entries.
func (c *Client) GetBankReconForCompany(ctx context.Context, company, bankLedger, fromDate, toDate string, unreconciledOnly bool, limit int) (*BankReconResult, error) {
	ledgers, err := c.ListLedgersForCompany(ctx, company, "")
	if err != nil {
		return nil, fmt.Errorf("listing ledgers for bank recon: %w", err)
	}

	var bankLedgers []LedgerItem
	var availableBanks []string
	for _, l := range ledgers {
		p := strings.ToLower(strings.TrimSpace(l.Parent))
		if strings.Contains(p, "bank") {
			bankLedgers = append(bankLedgers, l)
			availableBanks = append(availableBanks, l.Name)
		}
	}

	targetBank := strings.TrimSpace(bankLedger)
	var bookBal float64
	if targetBank == "" {
		if len(bankLedgers) == 0 {
			return nil, fmt.Errorf("no bank ledgers found in company")
		}
		targetBank = bankLedgers[0].Name
		bookBal = bankLedgers[0].ClosingBalance
	} else {
		resolved, _, rErr := c.ResolveLedgerName(ctx, company, targetBank)
		if rErr != nil {
			return nil, rErr
		}
		targetBank = resolved
		for _, l := range ledgers {
			if strings.EqualFold(l.Name, targetBank) {
				bookBal = l.ClosingBalance
				break
			}
		}
	}

	cleanFrom, cleanTo := ResolveDateRange(fromDate, toDate, time.Now())
	if cleanFrom == "" {
		cleanFrom = CurrentFinancialYearStart(time.Now())
	}
	if cleanTo == "" {
		cleanTo = CurrentFinancialYearEnd(time.Now())
	}

	vouchers, err := c.GetLedgerStatementVouchersForCompany(ctx, company, targetBank, cleanFrom, cleanTo)
	if err != nil {
		return nil, fmt.Errorf("fetching bank vouchers for %q: %w", targetBank, err)
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}

	balType := "Cr"
	if bookBal < 0 {
		balType = "Dr"
	}

	res := &BankReconResult{
		Company:            company,
		BankLedger:         targetBank,
		AvailableBanks:     availableBanks,
		FromDate:           cleanFrom,
		ToDate:             cleanTo,
		BookClosingBalance: math.Round(math.Abs(bookBal)*100) / 100,
		BookBalanceType:    balType,
		Entries:            make([]BankReconEntry, 0),
	}

	for _, v := range vouchers {
		var bankAmt float64
		var matched bool
		var contra []string
		var alloc BankAllocation
		var hasAlloc bool

		for _, leg := range v.LedgerEntries {
			if strings.EqualFold(strings.TrimSpace(leg.LedgerName), targetBank) {
				bankAmt += leg.Amount
				matched = true
				if len(leg.BankAllocations) > 0 && !hasAlloc {
					alloc = leg.BankAllocations[0]
					hasAlloc = true
				}
			} else if strings.TrimSpace(leg.LedgerName) != "" {
				contra = append(contra, leg.LedgerName)
			}
		}
		if !matched {
			continue
		}

		res.TotalEntries++
		reconciled := strings.TrimSpace(alloc.BankersDate) != ""
		var dr, cr float64
		if bankAmt < 0 {
			dr = math.Round(math.Abs(bankAmt)*100) / 100
		} else if bankAmt > 0 {
			cr = math.Round(bankAmt*100) / 100
		}

		if reconciled {
			res.ReconciledCount++
		} else {
			res.UnreconciledCount++
			res.UnreconciledDebit += dr
			res.UnreconciledCredit += cr
		}

		if unreconciledOnly && reconciled {
			continue
		}
		if len(res.Entries) < limit {
			res.Entries = append(res.Entries, BankReconEntry{
				Date:             v.Date,
				VoucherType:      v.VoucherTypeName,
				VoucherNumber:    v.VoucherNumber,
				Particulars:      strings.Join(contra, ", "),
				TransactionType:  alloc.TransactionType,
				InstrumentNumber: alloc.InstrumentNumber,
				InstrumentDate:   alloc.InstrumentDate,
				BankersDate:      alloc.BankersDate,
				Reconciled:       reconciled,
				Debit:            dr,
				Credit:           cr,
			})
		}
	}

	res.UnreconciledDebit = math.Round(res.UnreconciledDebit*100) / 100
	res.UnreconciledCredit = math.Round(res.UnreconciledCredit*100) / 100
	return res, nil
}

// GetSalesAnalyticsForCompany ranks top parties and top stock items for Sales or Purchase vouchers.
func (c *Client) GetSalesAnalyticsForCompany(ctx context.Context, company, voucherType, fromDate, toDate string, topN int) (*SalesAnalyticsResult, error) {
	vType := NormalizeVoucherTypeAlias(voucherType)
	if vType == "" {
		vType = "Sales"
	}
	cleanFrom, cleanTo := ResolveDateRange(fromDate, toDate, time.Now())
	if cleanFrom == "" {
		cleanFrom = CurrentFinancialYearStart(time.Now())
	}
	if cleanTo == "" {
		cleanTo = CurrentFinancialYearEnd(time.Now())
	}
	if topN <= 0 {
		topN = 10
	}
	if topN > 50 {
		topN = 50
	}

	vouchers, err := c.FilterVouchers(ctx, VoucherFilterOptions{
		Company:        company,
		VoucherType:    vType,
		FromDate:       cleanFrom,
		ToDate:         cleanTo,
		IncludeEntries: true,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching %s vouchers for analytics: %w", vType, err)
	}

	partyAgg := make(map[string]*AnalyticsPartyRank)
	itemAgg := make(map[string]*AnalyticsItemRank)
	var totalAmt float64

	for _, v := range vouchers {
		amt := math.Abs(v.Amount)
		totalAmt += amt

		party := strings.TrimSpace(v.PartyLedgerName)
		if party == "" {
			party = "(Unspecified)"
		}
		p, ok := partyAgg[party]
		if !ok {
			p = &AnalyticsPartyRank{Party: party}
			partyAgg[party] = p
		}
		p.Count++
		p.TotalAmount += amt

		for _, inv := range v.InventoryEntries {
			itemName := strings.TrimSpace(inv.StockItemName)
			if itemName == "" {
				continue
			}
			it, ok := itemAgg[itemName]
			if !ok {
				it = &AnalyticsItemRank{Item: itemName, SampleQty: strings.TrimSpace(inv.BilledQty)}
				itemAgg[itemName] = it
			}
			it.Count++
			it.TotalAmount += math.Abs(inv.Amount)
		}
	}

	parties := make([]AnalyticsPartyRank, 0, len(partyAgg))
	for _, p := range partyAgg {
		p.TotalAmount = math.Round(p.TotalAmount*100) / 100
		parties = append(parties, *p)
	}
	sort.Slice(parties, func(i, j int) bool {
		return parties[i].TotalAmount > parties[j].TotalAmount
	})
	if len(parties) > topN {
		parties = parties[:topN]
	}

	items := make([]AnalyticsItemRank, 0, len(itemAgg))
	for _, it := range itemAgg {
		it.TotalAmount = math.Round(it.TotalAmount*100) / 100
		items = append(items, *it)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].TotalAmount > items[j].TotalAmount
	})
	if len(items) > topN {
		items = items[:topN]
	}

	return &SalesAnalyticsResult{
		Company:       company,
		VoucherType:   vType,
		FromDate:      cleanFrom,
		ToDate:        cleanTo,
		TotalVouchers: len(vouchers),
		TotalAmount:   math.Round(totalAmt*100) / 100,
		TopParties:    parties,
		TopItems:      items,
	}, nil
}
