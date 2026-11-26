package tally_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tally-mcp/internal/config"
	"tally-mcp/internal/tally"
)

func TestCleanDateFormats(t *testing.T) {
	cases := map[string]string{
		"20260401":             "20260401",
		"2026-04-01":           "20260401",
		"2026/04/01":           "20260401",
		"01-04-2026":           "20260401",
		"01/04/2026":           "20260401",
		"2026-10-01T14:30:00Z": "20261001",
		"01 Apr 2026":          "20260401",
		"1 Apr 2026":           "20260401",
	}
	for input, want := range cases {
		if got := tally.CleanDate(input); got != want {
			t.Errorf("CleanDate(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAliasNormalization(t *testing.T) {
	if got := tally.NormalizeGroupAlias("customers"); got != "Sundry Debtors" {
		t.Errorf("NormalizeGroupAlias(customers) = %q", got)
	}
	if got := tally.NormalizeGroupAlias("vendors"); got != "Sundry Creditors" {
		t.Errorf("NormalizeGroupAlias(vendors) = %q", got)
	}
	if got := tally.NormalizeVoucherTypeAlias("receipts"); got != "Receipt" {
		t.Errorf("NormalizeVoucherTypeAlias(receipts) = %q", got)
	}
	if got := tally.NormalizeVoucherTypeAlias("sales invoice"); got != "Sales" {
		t.Errorf("NormalizeVoucherTypeAlias(sales invoice) = %q", got)
	}
}

func TestResolveLedgerNameAndCompany(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<COMPANY NAME="Acme Enterprises Pvt Ltd"></COMPANY>
		<COMPANY NAME="Beta Logistics LLP"></COMPANY>
		<LEDGER NAME="G.R. Metalloys Pvt Ltd"><PARENT>Sundry Debtors</PARENT></LEDGER>
		<LEDGER NAME="HDFC BANK A/C-322"><PARENT>Bank Accounts</PARENT></LEDGER>
		<LEDGER NAME="HDFC Credit Card"><PARENT>Current Liabilities</PARENT></LEDGER>
		<LEDGER NAME="Tata Consultancy Services"><PARENT>Sundry Debtors</PARENT></LEDGER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 60})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))
	ctx := context.Background()

	// Exact case-insensitive match
	resolved, from, err := client.ResolveLedgerName(ctx, "", "tata consultancy services")
	if err != nil || resolved != "Tata Consultancy Services" || from == "" {
		t.Fatalf("expected case-insensitive correction, got resolved=%q from=%q err=%v", resolved, from, err)
	}

	// Punctuation-normalized match ("GR Metalloys Pvt Ltd" -> "G.R. Metalloys Pvt Ltd")
	resolved, from, err = client.ResolveLedgerName(ctx, "", "GR Metalloys Pvt Ltd")
	if err != nil || resolved != "G.R. Metalloys Pvt Ltd" || from == "" {
		t.Fatalf("expected punctuation-normalized correction, got resolved=%q from=%q err=%v", resolved, from, err)
	}

	// Unambiguous substring match
	resolved, _, err = client.ResolveLedgerName(ctx, "", "Consultancy")
	if err != nil || resolved != "Tata Consultancy Services" {
		t.Fatalf("expected unambiguous substring match, got resolved=%q err=%v", resolved, err)
	}

	// Ambiguous substring returns candidates
	_, _, err = client.ResolveLedgerName(ctx, "", "HDFC")
	if err == nil || !strings.Contains(err.Error(), "HDFC BANK A/C-322") || !strings.Contains(err.Error(), "HDFC Credit Card") {
		t.Fatalf("expected did-you-mean candidates for ambiguous 'HDFC', got: %v", err)
	}

	// Typo fuzzy candidates
	_, _, err = client.ResolveLedgerName(ctx, "", "Ttaa Consultancy")
	if err == nil || !strings.Contains(err.Error(), "Tata Consultancy Services") {
		t.Fatalf("expected fuzzy candidate for typo, got: %v", err)
	}

	// Company resolution
	comp, _, err := client.ResolveCompany(ctx, "Acme")
	if err != nil || comp != "Acme Enterprises Pvt Ltd" {
		t.Fatalf("expected company resolution to 'Acme Enterprises Pvt Ltd', got %q err=%v", comp, err)
	}
	if _, _, err := client.ResolveCompany(ctx, "Unknown Co"); err == nil || !strings.Contains(err.Error(), "Acme Enterprises Pvt Ltd") {
		t.Fatalf("expected available companies in CompanyNotFoundError, got: %v", err)
	}
}

func TestSingleflightCoalescesConcurrentCacheMisses(t *testing.T) {
	var httpCalls atomic.Int32
	mockXML := `<ENVELOPE><BODY><DATA><TALLYMESSAGE><LEDGER NAME="Cash"><PARENT>Cash-in-Hand</PARENT></LEDGER></TALLYMESSAGE></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL:             "http://mock-tally:9000",
		CacheTTLSeconds: 30,
		ThrottleMs:      1,
	})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		httpCalls.Add(1)
		time.Sleep(30 * time.Millisecond)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ledgers, err := client.ListLedgers(context.Background(), "")
			if err != nil || len(ledgers) != 1 {
				t.Errorf("unexpected error or count: %v len=%d", err, len(ledgers))
			}
		}()
	}
	wg.Wait()

	if got := httpCalls.Load(); got != 1 {
		t.Errorf("expected singleflight to coalesce 6 concurrent misses into 1 HTTP call, got %d", got)
	}
}

func TestVoucherListCachingSortingAndHeaderOnly(t *testing.T) {
	var httpCalls atomic.Int32
	mockXML := `<ENVELOPE><BODY><DATA>
		<TALLYMESSAGE><VOUCHER><VOUCHERTYPENAME>Sales</VOUCHERTYPENAME><VOUCHERNUMBER>INV-1</VOUCHERNUMBER><DATE>20260401</DATE><AMOUNT>-100</AMOUNT><ALLLEDGERENTRIES.LIST><LEDGERNAME>Cash</LEDGERNAME><AMOUNT>-100</AMOUNT></ALLLEDGERENTRIES.LIST></VOUCHER></TALLYMESSAGE>
		<TALLYMESSAGE><VOUCHER><VOUCHERTYPENAME>Sales</VOUCHERTYPENAME><VOUCHERNUMBER>INV-2</VOUCHERNUMBER><DATE>20260405</DATE><AMOUNT>-500</AMOUNT><ALLLEDGERENTRIES.LIST><LEDGERNAME>Cash</LEDGERNAME><AMOUNT>-500</AMOUNT></ALLLEDGERENTRIES.LIST></VOUCHER></TALLYMESSAGE>
	</DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 30})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		httpCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	// Verify header-only FETCH by default
	qXML, err := tally.BuildFilteredListVouchersXML("", tally.VoucherFilterOptions{FromDate: "20260401"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(qXML, "AllLedgerEntries") {
		t.Errorf("expected default voucher list XML to omit AllLedgerEntries.*, got: %s", qXML)
	}

	// Page 1 (default sort date_desc -> INV-2 first)
	ctx := context.Background()
	p1, err := client.FilterVouchersPaginated(ctx, tally.VoucherFilterOptions{FromDate: "20260401", Limit: 1, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Vouchers) != 1 || p1.Vouchers[0].VoucherNumber != "INV-2" {
		t.Fatalf("expected INV-2 first under date_desc, got %+v", p1.Vouchers)
	}
	if len(p1.Vouchers[0].LedgerEntries) != 0 {
		t.Errorf("expected LedgerEntries stripped by default for tabular TOON, got %+v", p1.Vouchers[0].LedgerEntries)
	}

	// Page 2 should hit short-lived voucher cache (still 1 HTTP call)
	p2, err := client.FilterVouchersPaginated(ctx, tally.VoucherFilterOptions{FromDate: "20260401", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Vouchers) != 1 || p2.Vouchers[0].VoucherNumber != "INV-1" {
		t.Fatalf("expected INV-1 on page 2, got %+v", p2.Vouchers)
	}
	if httpCalls.Load() != 1 {
		t.Errorf("expected pagination page 2 to hit cache (1 HTTP call total), got %d", httpCalls.Load())
	}
}

func TestFinancialSummarySubGroupsAndOverdraft(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<GROUP NAME="North Zone Debtors"><PARENT>Sundry Debtors</PARENT></GROUP>
		<LEDGER NAME="Cash in Hand" PARENT="Cash-in-Hand"><CLOSINGBALANCE>-10000</CLOSINGBALANCE></LEDGER>
		<LEDGER NAME="Bank OD" PARENT="Bank Accounts"><CLOSINGBALANCE>4000</CLOSINGBALANCE></LEDGER>
		<LEDGER NAME="Northern Client" PARENT="North Zone Debtors"><CLOSINGBALANCE>-50000</CLOSINGBALANCE></LEDGER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", DefaultCompany: "TestCo"})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))

	sum, err := client.GetFinancialSummary(context.Background(), "20260410", "20260401")
	if err != nil {
		t.Fatalf("GetFinancialSummary failed: %v", err)
	}
	// Cash 10000 - Bank OD 4000 = 6000
	if sum.CashAndBank != 6000 {
		t.Errorf("expected CashAndBank=6000 (10000 cash - 4000 OD), got %.2f", sum.CashAndBank)
	}
	if sum.TotalReceivable != 50000 {
		t.Errorf("expected TotalReceivable=50000 via North Zone Debtors subgroup, got %.2f", sum.TotalReceivable)
	}
}

func TestXMLGuardAndErrorParsing(t *testing.T) {
	// Undefined filter even with 0 formulas defined
	badFilter := `<ENVELOPE><BODY><DESC><TDL><TDLMESSAGE><COLLECTION NAME="C"><FILTER>MissingFormula</FILTER></COLLECTION></TDLMESSAGE></TDL></DESC></BODY></ENVELOPE>`
	if _, err := tally.ValidateAndSanitizeTallyXML(badFilter); err == nil {
		t.Errorf("expected error for undefined <FILTER>MissingFormula</FILTER>")
	}

	// Multiple LINEERROR + hint
	errXML := []byte(`<RESPONSE><LINEERROR>Company not open</LINEERROR><LINEERROR>Invalid date</LINEERROR></RESPONSE>`)
	err := tally.CheckTallyError(errXML)
	if err == nil || !strings.Contains(err.Error(), "Company not open") || !strings.Contains(err.Error(), "Invalid date") || !strings.Contains(err.Error(), "tally_list_companies") {
		t.Errorf("expected multi-error + recovery hint, got: %v", err)
	}

	// GeneratePDF path traversal guard
	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000"})
	if _, _, err := client.GeneratePDF(context.Background(), tally.GeneratePDFOptions{
		ReportType: "Balance Sheet",
		FileName:   "../../",
	}); err == nil {
		t.Errorf("expected error for invalid traversal filename")
	}

	// Report flattening
	rawMap, err := tally.XMLToMap([]byte(`<ENVELOPE><BODY><DATA>
		<BSNAME><DSPACCNAME><DSPDISPNAME>Capital Account</DSPDISPNAME></DSPACCNAME></BSNAME>
		<BSAMT><BSMAINAMT>500000.00</BSMAINAMT></BSAMT>
	</DATA></BODY></ENVELOPE>`))
	if err != nil {
		t.Fatal(err)
	}
	norm := tally.NormalizeReportForAgent("balance_sheet", rawMap)
	m, ok := norm.(map[string]any)
	if !ok || m["count"] != 1 {
		t.Errorf("expected normalized balance sheet report with count=1, got %#v", norm)
	}
}

func TestGetLedgerDetailReportPagingAndVoucherSummary(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<LEDGER NAME="A &amp; B Traders">
			<PARENT>Sundry Debtors</PARENT>
			<OPENINGBALANCE>-1000.129</OPENINGBALANCE>
			<CLOSINGBALANCE>-5432.104</CLOSINGBALANCE>
			<ADDRESS.LIST><ADDRESS>Line 1</ADDRESS><ADDRESS>Line 2</ADDRESS></ADDRESS.LIST>
		</LEDGER>
		<VOUCHER VOUCHERNUMBER="S-1" DATE="20260410" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="A &amp; B Traders" AMOUNT="-1000.00"></VOUCHER>
		<VOUCHER VOUCHERNUMBER="S-2" DATE="20260415" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="Other Co" AMOUNT="-2500.00"></VOUCHER>
		<VOUCHER VOUCHERNUMBER="R-1" DATE="20260501" VOUCHERTYPENAME="Receipt" PARTYLEDGERNAME="A &amp; B Traders" AMOUNT="1000.00"></VOUCHER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 60})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}
	}))
	ctx := context.Background()

	// 1. Clean LedgerDetail with rounded floats and flattened address
	ld, err := client.GetLedgerDetailForCompany(ctx, "", "A & B Traders")
	if err != nil {
		t.Fatalf("GetLedgerDetailForCompany failed: %v", err)
	}
	if ld.OpeningBalance != -1000.13 || ld.ClosingBalance != -5432.10 || len(ld.Address) != 2 {
		t.Errorf("unexpected LedgerDetail: %+v", ld)
	}

	// 2. FinancialSummary omits Summary in serialized JSON
	sum, err := client.GetFinancialSummary(ctx, "20260531", "20260401")
	if err != nil {
		t.Fatalf("GetFinancialSummary failed: %v", err)
	}
	jsonOut, err := tally.CompactJSON(sum)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jsonOut, `"summary"`) {
		t.Errorf("expected FinancialSummary serialized JSON to omit redundant summary string, got: %s", jsonOut)
	}

	// 3. SummarizeVouchers groups by voucher_type and month
	vSum, err := client.SummarizeVouchers(ctx, tally.VoucherFilterOptions{FromDate: "20260401"}, "voucher_type")
	if err != nil {
		t.Fatalf("SummarizeVouchers failed: %v", err)
	}
	if vSum.TotalCount != 3 || vSum.TotalAmount != 4500 || len(vSum.Groups) != 2 {
		t.Errorf("unexpected VoucherSummary: %+v", vSum)
	}

	// 4. Report normalization: zero-row stripping, overdue_only, numeric parsing, pagination
	billsMap, err := tally.XMLToMap([]byte(`<ENVELOPE><HEADER><VERSION>1</VERSION></HEADER><BODY><DATA>
		<BILLFIXED><BILLDATE>20260401</BILLDATE><BILLREF>INV-1</BILLREF><BILLPARTY>Alpha</BILLPARTY></BILLFIXED>
		<BILLFIXED><BILLDATE>20260402</BILLDATE><BILLREF>INV-2</BILLREF><BILLPARTY>Beta</BILLPARTY></BILLFIXED>
		<BILLFIXED><BILLDATE>20260403</BILLDATE><BILLREF>INV-3</BILLREF><BILLPARTY>Zero</BILLPARTY></BILLFIXED>
		<BILLCL>1,200.00 Dr</BILLCL><BILLCL>5,000.00 Dr</BILLCL><BILLCL>0.00</BILLCL>
		<BILLDUE>20260415</BILLDUE><BILLDUE>20260416</BILLDUE><BILLDUE>20260417</BILLDUE>
		<BILLOVERDUE>0</BILLOVERDUE><BILLOVERDUE>45 days</BILLOVERDUE><BILLOVERDUE>10</BILLOVERDUE>
	</DATA></BODY></ENVELOPE>`))
	if err != nil {
		t.Fatal(err)
	}
	pagedBills := tally.NormalizeReportWithOptions("bills_receivable", billsMap, tally.ReportQueryOptions{
		OverdueOnly: true,
		MinAmount:   1000,
		Limit:       1,
	}).(map[string]any)
	if pagedBills["total"] != 1 || pagedBills["count"] != 1 {
		t.Errorf("expected 1 overdue bill >= 1000 (zero row stripped), got: %+v", pagedBills)
	}
	bills := pagedBills["bills"].([]tally.OutstandingBillRow)
	if bills[0].PendingAmount != 5000 || bills[0].OverdueDays != 45 || bills[0].BalanceType != "Dr" {
		t.Errorf("expected parsed numeric bill row, got: %+v", bills[0])
	}

	// 5. Relative date range resolution
	now := time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)
	if f, tDate := tally.ResolveDateRange("this_month", "", now); f != "20261101" || tDate != "20261125" {
		t.Errorf("this_month expected 20261101..20261125, got %s..%s", f, tDate)
	}
	if f, tDate := tally.ResolveDateRange("last_month", "", now); f != "20261001" || tDate != "20261031" {
		t.Errorf("last_month expected 20261001..20261031, got %s..%s", f, tDate)
	}
	if f, tDate := tally.ResolveDateRange("last_fy", "", now); f != "20250401" || tDate != "20260331" {
		t.Errorf("last_fy expected 20250401..20260331, got %s..%s", f, tDate)
	}

	// 6. Sorted ledgers (balance_desc)
	sortedLedgers, err := client.FilterLedgersSortedForCompany(ctx, "", "", "", "balance_desc", 10, 0, true)
	if err != nil {
		t.Fatalf("FilterLedgersSortedForCompany failed: %v", err)
	}
	compact := sortedLedgers.Ledgers.([]tally.CompactLedgerItem)
	if len(compact) == 0 || compact[0].Name != "A & B Traders" {
		t.Errorf("expected A & B Traders first in balance_desc, got %+v", compact)
	}

	// 7. Party overview 1-call aggregation
	overview, err := client.GetPartyOverviewForCompany(ctx, "", "A B Traders", "20260401", "20261130", 5)
	if err != nil {
		t.Fatalf("GetPartyOverviewForCompany failed: %v", err)
	}
	if overview.Name != "A & B Traders" || overview.BalanceType != "Dr" || overview.RecentCount != 2 {
		t.Errorf("unexpected PartyOverview: %+v", overview)
	}
}
