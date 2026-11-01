package tally_test

import (
	"bytes"
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

func TestBuildQueries(t *testing.T) {
	t.Run("BuildListCompaniesXML", func(t *testing.T) {
		xml := tally.BuildListCompaniesXML()
		if !strings.Contains(xml, "<ID>Company</ID>") {
			t.Errorf("expected '<ID>Company</ID>' in XML")
		}
	})

	t.Run("BuildListLedgersXML", func(t *testing.T) {
		xml, err := tally.BuildListLedgersXML("MyCompany", "Sundry Debtors")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, "<SVCURRENTCOMPANY>MyCompany</SVCURRENTCOMPANY>") {
			t.Errorf("missing current company")
		}
		if !strings.Contains(xml, "<CHILDOF>Sundry Debtors</CHILDOF>") {
			t.Errorf("missing group filter")
		}
	})

	t.Run("BuildCreateLedgerXML", func(t *testing.T) {
		xml, err := tally.BuildCreateLedgerXML("MyCompany", "Test Customer", "Sundry Debtors", 5000.50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, `ACTION="Create"`) {
			t.Errorf("expected Create action in XML")
		}
		if !strings.Contains(xml, "<NAME>Test Customer</NAME>") {
			t.Errorf("expected ledger name in XML")
		}
	})
}

func TestXMLToMap(t *testing.T) {
	xmlSample := `<ENVELOPE><HEADER><VERSION>1</VERSION></HEADER><BODY><DATA><NAME>Test Ledger</NAME><AMOUNT>1250.00</AMOUNT></DATA></BODY></ENVELOPE>`
	res, err := tally.XMLToMap([]byte(xmlSample))
	if err != nil {
		t.Fatalf("failed to convert XML to map: %v", err)
	}

	body, ok := res["ENVELOPE"].(map[string]any)["BODY"].(map[string]any)
	if !ok {
		t.Fatalf("expected BODY node")
	}
	data, ok := body["DATA"].(map[string]any)
	if !ok {
		t.Fatalf("expected DATA node")
	}
	if data["NAME"] != "Test Ledger" {
		t.Errorf("expected 'Test Ledger', got %v", data["NAME"])
	}
}

type roundTripFunc func(req *http.Request) *http.Response

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

func TestTallyClientWithMockServer(t *testing.T) {
	mockTallyResponse := `<ENVELOPE>
  <HEADER><STATUS>1</STATUS></HEADER>
  <BODY>
    <DATA>
      <COMPANY NAME="Acme Corporation Ltd">
        <FORMALNAME>Acme Corp</FORMALNAME>
        <STATENAME>Maharashtra</STATENAME>
        <COUNTRYNAME>India</COUNTRYNAME>
      </COMPANY>
    </DATA>
  </BODY>
</ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL:            "http://mock-tally:9000",
		TimeoutSeconds: 5,
	})

	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockTallyResponse)),
		}
	}))

	ctx := context.Background()

	// Test Ping
	status, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if !status.Connected {
		t.Errorf("expected status.Connected to be true")
	}
	if status.CompaniesCount != 1 {
		t.Errorf("expected 1 company, got %d", status.CompaniesCount)
	}
	if status.Companies[0].Name != "Acme Corporation Ltd" {
		t.Errorf("expected 'Acme Corporation Ltd', got %s", status.Companies[0].Name)
	}

	// Test ListCompanies
	companies, err := client.ListCompanies(ctx)
	if err != nil {
		t.Fatalf("list companies failed: %v", err)
	}
	if len(companies) != 1 || companies[0].Name != "Acme Corporation Ltd" {
		t.Errorf("list companies failed: %v", companies)
	}
}

func TestListVouchersPaginated(t *testing.T) {
	// Generate mock XML with 120 vouchers
	var b strings.Builder
	b.WriteString("<ENVELOPE><HEADER><STATUS>1</STATUS></HEADER><BODY><DATA>")
	for i := 1; i <= 120; i++ {
		b.WriteString("<TALLYMESSAGE><VOUCHER>")
		b.WriteString("<VOUCHERTYPENAME>Sales</VOUCHERTYPENAME>")
		b.WriteString(strings.ReplaceAll("<VOUCHERNUMBER>INV-{{N}}</VOUCHERNUMBER>", "{{N}}", strings.TrimSpace(strings.Repeat(" ", 0)+string(rune('0'+i%10)))))
		b.WriteString("<DATE>20240401</DATE>")
		b.WriteString("</VOUCHER></TALLYMESSAGE>")
	}
	b.WriteString("</DATA></BODY></ENVELOPE>")
	mockXML := b.String()

	client := tally.NewClient(config.TallyConfig{
		URL: "http://mock-tally:9000",
	})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockXML)),
		}
	}))

	ctx := context.Background()

	// 1. Default page (limit 0 -> 50, offset 0)
	page1, err := client.ListVouchersPaginated(ctx, "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("page 1 failed: %v", err)
	}
	if page1.Total != 120 {
		t.Errorf("expected total=120, got %d", page1.Total)
	}
	if page1.Limit != 50 {
		t.Errorf("expected default limit=50, got %d", page1.Limit)
	}
	if page1.Offset != 0 {
		t.Errorf("expected offset=0, got %d", page1.Offset)
	}
	if page1.Count != 50 {
		t.Errorf("expected count=50, got %d", page1.Count)
	}
	if !page1.HasMore {
		t.Errorf("expected has_more=true")
	}
	if page1.NextOffset == nil || *page1.NextOffset != 50 {
		t.Errorf("expected next_offset=50, got %v", page1.NextOffset)
	}

	// 2. Second page (limit 50, offset 50)
	page2, err := client.ListVouchersPaginated(ctx, "", "", "", 50, 50)
	if err != nil {
		t.Fatalf("page 2 failed: %v", err)
	}
	if page2.Count != 50 {
		t.Errorf("expected count=50, got %d", page2.Count)
	}
	if !page2.HasMore {
		t.Errorf("expected has_more=true")
	}
	if page2.NextOffset == nil || *page2.NextOffset != 100 {
		t.Errorf("expected next_offset=100, got %v", page2.NextOffset)
	}

	// 3. Third page (limit 50, offset 100) -> 20 remaining
	page3, err := client.ListVouchersPaginated(ctx, "", "", "", 50, 100)
	if err != nil {
		t.Fatalf("page 3 failed: %v", err)
	}
	if page3.Count != 20 {
		t.Errorf("expected count=20, got %d", page3.Count)
	}
	if page3.HasMore {
		t.Errorf("expected has_more=false on final page")
	}
	if page3.NextOffset != nil {
		t.Errorf("expected next_offset=nil on final page")
	}

	// 4. Out of bounds page (offset 150)
	page4, err := client.ListVouchersPaginated(ctx, "", "", "", 50, 150)
	if err != nil {
		t.Fatalf("page 4 failed: %v", err)
	}
	if page4.Count != 0 {
		t.Errorf("expected count=0, got %d", page4.Count)
	}
	if page4.HasMore {
		t.Errorf("expected has_more=false")
	}

	// 5. Max limit clamping (limit 1000 clamped to 250)
	page5, err := client.ListVouchersPaginated(ctx, "", "", "", 1000, 0)
	if err != nil {
		t.Fatalf("page 5 failed: %v", err)
	}
	if page5.Limit != 250 {
		t.Errorf("expected limit capped at 250, got %d", page5.Limit)
	}
	if page5.Count != 120 {
		t.Errorf("expected count=120, got %d", page5.Count)
	}
}

func TestBuildGetVoucherXML(t *testing.T) {
	xml1, err := tally.BuildGetVoucherXML("MyCompany", "1042", "", "", "")
	if err != nil {
		t.Fatalf("BuildGetVoucherXML failed: %v", err)
	}
	if !strings.Contains(xml1, "$MasterId = 1042") {
		t.Errorf("expected $MasterId = 1042 in XML, got: %s", xml1)
	}

	xml2, err := tally.BuildGetVoucherXML("MyCompany", "", "INV-102", "Sales", "20240401")
	if err != nil {
		t.Fatalf("BuildGetVoucherXML failed: %v", err)
	}
	if !strings.Contains(xml2, `$VoucherNumber = "INV-102"`) {
		t.Errorf("expected VoucherNumber filter in XML, got: %s", xml2)
	}
	if !strings.Contains(xml2, `$VoucherTypeName = "Sales"`) {
		t.Errorf("expected VoucherTypeName filter in XML, got: %s", xml2)
	}
}

func TestGetVoucher(t *testing.T) {
	mockVoucherXML := `<ENVELOPE>
  <HEADER><STATUS>1</STATUS></HEADER>
  <BODY>
    <DATA>
      <COLLECTION>
        <VOUCHER>
          <DATE>20240401</DATE>
          <VOUCHERTYPENAME>Sales</VOUCHERTYPENAME>
          <VOUCHERNUMBER>INV-102</VOUCHERNUMBER>
          <REFERENCE>PO-991</REFERENCE>
          <PARTYLEDGERNAME>Acme Customer Ltd</PARTYLEDGERNAME>
          <AMOUNT>-11800.00</AMOUNT>
          <NARRATION>Being sales invoice #102 for 10 pcs Widget Pro</NARRATION>
          <MASTERID>4512</MASTERID>
          <ALLLEDGERENTRIES.LIST>
            <LEDGERNAME>Acme Customer Ltd</LEDGERNAME>
            <ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE>
            <AMOUNT>-11800.00</AMOUNT>
            <BILLALLOCATIONS.LIST>
              <NAME>INV-102</NAME>
              <BILLTYPE>New Ref</BILLTYPE>
              <AMOUNT>-11800.00</AMOUNT>
            </BILLALLOCATIONS.LIST>
          </ALLLEDGERENTRIES.LIST>
          <ALLLEDGERENTRIES.LIST>
            <LEDGERNAME>Sales Account</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>10000.00</AMOUNT>
          </ALLLEDGERENTRIES.LIST>
          <ALLLEDGERENTRIES.LIST>
            <LEDGERNAME>CGST</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>900.00</AMOUNT>
          </ALLLEDGERENTRIES.LIST>
          <ALLLEDGERENTRIES.LIST>
            <LEDGERNAME>SGST</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>900.00</AMOUNT>
          </ALLLEDGERENTRIES.LIST>
          <ALLINVENTORYENTRIES.LIST>
            <STOCKITEMNAME>Widget Pro 100</STOCKITEMNAME>
            <RATE>1000.00/pcs</RATE>
            <ACTUALQTY>10 pcs</ACTUALQTY>
            <BILLEDQTY>10 pcs</BILLEDQTY>
            <AMOUNT>10000.00</AMOUNT>
            <BATCHALLOCATIONS.LIST>
              <GODOWNNAME>Main Warehouse</GODOWNNAME>
              <BATCHNAME>Batch-A</BATCHNAME>
              <AMOUNT>10000.00</AMOUNT>
              <ACTUALQTY>10 pcs</ACTUALQTY>
            </BATCHALLOCATIONS.LIST>
          </ALLINVENTORYENTRIES.LIST>
        </VOUCHER>
      </COLLECTION>
    </DATA>
  </BODY>
</ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL: "http://mock-tally:9000",
	})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockVoucherXML)),
		}
	}))

	ctx := context.Background()
	detail, err := client.GetVoucher(ctx, "4512", "", "", "")
	if err != nil {
		t.Fatalf("GetVoucher failed: %v", err)
	}

	if detail.VoucherNumber != "INV-102" {
		t.Errorf("expected VoucherNumber INV-102, got %s", detail.VoucherNumber)
	}
	if detail.MasterID != "4512" {
		t.Errorf("expected MasterID 4512, got %s", detail.MasterID)
	}
	if detail.PartyLedgerName != "Acme Customer Ltd" {
		t.Errorf("expected party ledger 'Acme Customer Ltd', got %s", detail.PartyLedgerName)
	}
	if detail.Narration != "Being sales invoice #102 for 10 pcs Widget Pro" {
		t.Errorf("expected narration, got %s", detail.Narration)
	}

	// Verify ledger entries
	if len(detail.LedgerEntries) != 4 {
		t.Fatalf("expected 4 ledger entries, got %d", len(detail.LedgerEntries))
	}
	if len(detail.LedgerEntries[0].BillAllocations) != 1 {
		t.Errorf("expected 1 bill allocation, got %d", len(detail.LedgerEntries[0].BillAllocations))
	} else if detail.LedgerEntries[0].BillAllocations[0].Name != "INV-102" {
		t.Errorf("expected bill allocation name 'INV-102', got %s", detail.LedgerEntries[0].BillAllocations[0].Name)
	}

	// Verify inventory entries
	if len(detail.InventoryEntries) != 1 {
		t.Fatalf("expected 1 inventory entry, got %d", len(detail.InventoryEntries))
	}
	inv := detail.InventoryEntries[0]
	if inv.StockItemName != "Widget Pro 100" || inv.BilledQty != "10 pcs" || inv.Amount != 10000 {
		t.Errorf("unexpected inventory item: %+v", inv)
	}
	if len(inv.BatchAllocations) != 1 || inv.BatchAllocations[0].GodownName != "Main Warehouse" {
		t.Errorf("unexpected batch allocation: %+v", inv.BatchAllocations)
	}

	// Verify automatic tax breakdown
	if len(detail.Taxes) != 2 {
		t.Fatalf("expected 2 taxes (CGST, SGST), got %d", len(detail.Taxes))
	}
	if detail.Taxes[0].LedgerName != "CGST" || detail.Taxes[0].Amount != 900 {
		t.Errorf("unexpected CGST tax: %+v", detail.Taxes[0])
	}
	if detail.Taxes[1].LedgerName != "SGST" || detail.Taxes[1].Amount != 900 {
		t.Errorf("unexpected SGST tax: %+v", detail.Taxes[1])
	}
}

func TestReportXMLBuilders(t *testing.T) {
	t.Run("BuildProfitAndLossXML", func(t *testing.T) {
		xml, err := tally.BuildProfitAndLossXML("MyCompany", "2024-04-01", "2025-03-31")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, "<ID>Profit and Loss</ID>") {
			t.Errorf("expected Profit and Loss report ID")
		}
		if !strings.Contains(xml, "<SVCURRENTCOMPANY>MyCompany</SVCURRENTCOMPANY>") {
			t.Errorf("expected current company")
		}
		if !strings.Contains(xml, "<SVFROMDATE>20240401</SVFROMDATE>") || !strings.Contains(xml, "<SVTODATE>20250331</SVTODATE>") {
			t.Errorf("expected cleaned date format 20240401 and 20250331, got: %s", xml)
		}
		if !strings.Contains(xml, "<EXPLODEFLAG>Yes</EXPLODEFLAG>") {
			t.Errorf("expected EXPLODEFLAG")
		}
	})

	t.Run("BuildBalanceSheetXML", func(t *testing.T) {
		xml, err := tally.BuildBalanceSheetXML("MyCompany", "2025-03-31", "2024-04-01")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, "<ID>Balance Sheet</ID>") {
			t.Errorf("expected Balance Sheet report ID")
		}
		if !strings.Contains(xml, "<SVTODATE>20250331</SVTODATE>") {
			t.Errorf("expected as on date in SVTODATE")
		}
		if !strings.Contains(xml, "<SVFROMDATE>20240401</SVFROMDATE>") {
			t.Errorf("expected from date in SVFROMDATE")
		}
	})

	t.Run("BuildBillsReceivableXML", func(t *testing.T) {
		xml, err := tally.BuildBillsReceivableXML("MyCompany", "Acme Customer Ltd", "2024-04-01", "2025-03-31")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, "<ID>Bills Receivable</ID>") {
			t.Errorf("expected Bills Receivable report ID")
		}
		if !strings.Contains(xml, "<LEDGERNAME>Acme Customer Ltd</LEDGERNAME>") {
			t.Errorf("expected party filter in LEDGERNAME")
		}
		if !strings.Contains(xml, "<SVFROMDATE>20240401</SVFROMDATE>") {
			t.Errorf("expected cleaned date")
		}
	})

	t.Run("BuildBillsPayableXML", func(t *testing.T) {
		xml, err := tally.BuildBillsPayableXML("MyCompany", "Acme Supplier Ltd", "2024-04-01", "2025-03-31")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, "<ID>Bills Payable</ID>") {
			t.Errorf("expected Bills Payable report ID")
		}
		if !strings.Contains(xml, "<LEDGERNAME>Acme Supplier Ltd</LEDGERNAME>") {
			t.Errorf("expected party filter in LEDGERNAME")
		}
	})

	t.Run("BuildDayBookXML", func(t *testing.T) {
		xml, err := tally.BuildDayBookXML("MyCompany", "2024-05-15")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(xml, "<ID>Day Book</ID>") {
			t.Errorf("expected Day Book report ID")
		}
		if !strings.Contains(xml, "<SVFROMDATE>20240515</SVFROMDATE><SVTODATE>20240515</SVTODATE>") {
			t.Errorf("expected single day in SVFROMDATE and SVTODATE")
		}
	})
}

func TestFinancialReportsClient(t *testing.T) {
	mockReportResponse := `<ENVELOPE>
  <HEADER><STATUS>1</STATUS></HEADER>
  <BODY>
    <DATA>
      <REPORTITEM>
        <NAME>Sales Accounts</NAME>
        <AMOUNT>-250000.00</AMOUNT>
      </REPORTITEM>
    </DATA>
  </BODY>
</ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL:            "http://mock-tally:9000",
		TimeoutSeconds: 5,
	})

	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockReportResponse)),
		}
	}))

	ctx := context.Background()

	t.Run("GetProfitAndLoss", func(t *testing.T) {
		res, err := client.GetProfitAndLoss(ctx, "2024-04-01", "2025-03-31")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || res["ENVELOPE"] == nil {
			t.Errorf("expected parsed envelope in result")
		}
	})

	t.Run("GetBalanceSheet", func(t *testing.T) {
		res, err := client.GetBalanceSheet(ctx, "2025-03-31", "2024-04-01")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || res["ENVELOPE"] == nil {
			t.Errorf("expected parsed envelope in result")
		}
	})

	t.Run("GetOutstandingReceivables", func(t *testing.T) {
		res, err := client.GetOutstandingReceivables(ctx, "Acme Customer", "2024-04-01", "2025-03-31")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || res["ENVELOPE"] == nil {
			t.Errorf("expected parsed envelope in result")
		}
	})

	t.Run("GetOutstandingPayables", func(t *testing.T) {
		res, err := client.GetOutstandingPayables(ctx, "Acme Supplier", "2024-04-01", "2025-03-31")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || res["ENVELOPE"] == nil {
			t.Errorf("expected parsed envelope in result")
		}
	})

	t.Run("GetDayBook", func(t *testing.T) {
		res, err := client.GetDayBook(ctx, "2024-05-15")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || res["ENVELOPE"] == nil {
			t.Errorf("expected parsed envelope in result")
		}
	})
}

func TestExportPDFQueries(t *testing.T) {
	t.Run("NormalizeReportID", func(t *testing.T) {
		if tally.NormalizeReportID("bs") != "Balance Sheet" {
			t.Errorf("expected Balance Sheet for bs")
		}
		if tally.NormalizeReportID("profit & loss") != "Profit and Loss" {
			t.Errorf("expected Profit and Loss for profit & loss")
		}
		if tally.NormalizeReportID("daybook") != "Day Book" {
			t.Errorf("expected Day Book for daybook")
		}
		if tally.NormalizeReportID("invoice") != "Voucher" {
			t.Errorf("expected Voucher for invoice")
		}
	})

	t.Run("BuildExportPDFXML", func(t *testing.T) {
		xml, err := tally.BuildExportPDFXML(
			"Acme Corp",
			"Balance Sheet",
			"C:\\Temp",
			"bs.pdf",
			"2024-04-01",
			"2025-03-31",
			"",
			"",
			"",
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(xml, "<ID>Balance Sheet</ID>") {
			t.Errorf("expected Balance Sheet report ID in XML")
		}
		if !strings.Contains(xml, "<SVEXPORTFORMAT>$$SysName:PDF</SVEXPORTFORMAT>") {
			t.Errorf("expected PDF export format")
		}
		if !strings.Contains(xml, "<SVEXPORTLOCATION>C:\\Temp</SVEXPORTLOCATION>") {
			t.Errorf("expected export location")
		}
		if !strings.Contains(xml, "<SVPRINTFILENAME>bs.pdf</SVPRINTFILENAME>") {
			t.Errorf("expected print file name")
		}
		if !strings.Contains(xml, "<SVFROMDATE>20240401</SVFROMDATE>") || !strings.Contains(xml, "<SVTODATE>20250331</SVTODATE>") {
			t.Errorf("expected cleaned date format")
		}
	})
}

func TestClientGeneratePDF(t *testing.T) {
	mockResponse := `<ENVELOPE><HEADER><STATUS>1</STATUS></HEADER><BODY><DATA></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL:            "http://mock-tally:9000",
		TimeoutSeconds: 5,
	})

	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockResponse)),
		}
	}))

	ctx := context.Background()

	t.Run("Generate Balance Sheet PDF", func(t *testing.T) {
		opts := tally.GeneratePDFOptions{
			ReportType: "Balance Sheet",
			FromDate:   "20240401",
			ToDate:     "20250331",
			FileName:   "test_balance_sheet.pdf",
		}

		res, pdfBytes, err := client.GeneratePDF(ctx, opts)
		if err != nil {
			t.Fatalf("GeneratePDF failed: %v", err)
		}

		if res.FileName != "test_balance_sheet.pdf" {
			t.Errorf("expected file_name test_balance_sheet.pdf, got %s", res.FileName)
		}
		if res.ReportType != "Balance Sheet" {
			t.Errorf("expected report_type Balance Sheet, got %s", res.ReportType)
		}
		if res.SizeBytes <= 0 {
			t.Errorf("expected positive size_bytes, got %d", res.SizeBytes)
		}
		if len(res.Base64PDF) == 0 {
			t.Errorf("expected non-empty Base64PDF")
		}
		if !bytes.HasPrefix(pdfBytes, []byte("%PDF-1.4")) {
			t.Errorf("expected PDF header in pdfBytes")
		}
	})

	t.Run("Generate Voucher PDF with Direct PDF Stream", func(t *testing.T) {
		streamedPDF := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\nxref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\nstartxref\n50\n%%EOF\n")

		directClient := tally.NewClient(config.TallyConfig{
			URL:            "http://mock-tally:9000",
			TimeoutSeconds: 5,
		})
		directClient.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/pdf"}},
				Body:       io.NopCloser(bytes.NewReader(streamedPDF)),
			}
		}))

		opts := tally.GeneratePDFOptions{
			ReportType:    "Voucher",
			VoucherNumber: "102",
			VoucherType:   "Sales",
			FileName:      "invoice_102.pdf",
		}

		res, pdfBytes, err := directClient.GeneratePDF(ctx, opts)
		if err != nil {
			t.Fatalf("direct GeneratePDF failed: %v", err)
		}

		if res.FileName != "invoice_102.pdf" {
			t.Errorf("expected invoice_102.pdf, got %s", res.FileName)
		}
		if len(pdfBytes) != len(streamedPDF) {
			t.Errorf("expected %d bytes, got %d", len(streamedPDF), len(pdfBytes))
		}
	})
}

func TestQuerySerializationNoParallelExecution(t *testing.T) {
	var activeQueries atomic.Int32
	var maxConcurrent atomic.Int32

	cfg := config.TallyConfig{
		URL:            "http://mock-tally:9000",
		TimeoutSeconds: 5,
		MaxQueueSize:   50,
		ThrottleMs:     5,
	}

	client := tally.NewClient(cfg)
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		cur := activeQueries.Add(1)
		defer activeQueries.Add(-1)

		// Record highest concurrency seen inside roundTripper
		for {
			oldMax := maxConcurrent.Load()
			if cur <= oldMax || maxConcurrent.CompareAndSwap(oldMax, cur) {
				break
			}
		}

		time.Sleep(15 * time.Millisecond)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(`<ENVELOPE><HEADER><STATUS>1</STATUS></HEADER></ENVELOPE>`)),
		}
	}))

	const totalWorkers = 10
	var wg sync.WaitGroup
	ctx := context.Background()

	// Launch 10 parallel queries simultaneously
	for i := 0; i < totalWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.ExecuteRawXML(ctx, `<ENVELOPE></ENVELOPE>`)
			if err != nil {
				t.Errorf("unexpected query error: %v", err)
			}
		}()
	}

	wg.Wait()

	if maxConcurrent.Load() > 1 {
		t.Fatalf("expected max 1 concurrent query to Tally, but saw %d parallel queries!", maxConcurrent.Load())
	}
}

func TestQueryBufferOverflow(t *testing.T) {
	cfg := config.TallyConfig{
		URL:            "http://mock-tally:9000",
		TimeoutSeconds: 5,
		MaxQueueSize:   2, // small queue of 2 items
		ThrottleMs:     0,
	}

	client := tally.NewClient(cfg)

	// Block the first query so subsequent queries queue up
	blocker := make(chan struct{})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		<-blocker
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(`<ENVELOPE><HEADER><STATUS>1</STATUS></HEADER></ENVELOPE>`)),
		}
	}))

	ctx := context.Background()

	// Query 1: will acquire querySem and block
	go func() {
		_, _ = client.ExecuteRawXML(ctx, `<ENVELOPE>1</ENVELOPE>`)
	}()
	time.Sleep(20 * time.Millisecond)

	// Query 2: enters queueSem (slot 1 of 2)
	go func() {
		_, _ = client.ExecuteRawXML(ctx, `<ENVELOPE>2</ENVELOPE>`)
	}()
	time.Sleep(20 * time.Millisecond)

	// Query 3: enters queueSem (slot 2 of 2)
	go func() {
		_, _ = client.ExecuteRawXML(ctx, `<ENVELOPE>3</ENVELOPE>`)
	}()
	time.Sleep(20 * time.Millisecond)

	// Query 4: buffer is full! Should immediately fail
	_, err := client.ExecuteRawXML(ctx, `<ENVELOPE>4</ENVELOPE>`)
	if err == nil {
		t.Fatalf("expected error due to full query buffer, but got nil")
	}
	if !strings.Contains(err.Error(), "tally query buffer is full") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Release blocked queries
	close(blocker)
}

func TestQueryContextCancellation(t *testing.T) {
	cfg := config.TallyConfig{
		URL:            "http://mock-tally:9000",
		TimeoutSeconds: 5,
		MaxQueueSize:   10,
	}

	client := tally.NewClient(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel context

	_, err := client.ExecuteRawXML(ctx, `<ENVELOPE></ENVELOPE>`)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestMemoryCache(t *testing.T) {
	cache := tally.NewMemoryCache(50 * time.Millisecond)

	cache.Set("ledgers:company1:all", "val1")
	cache.Set("ledgers:company1:debtors", "val2")
	cache.Set("stock:company1:all", "val3")

	if v, ok := cache.Get("ledgers:company1:all"); !ok || v != "val1" {
		t.Fatalf("expected val1, got %v", v)
	}

	// Invalidate prefix
	cache.Invalidate("ledgers:")
	if _, ok := cache.Get("ledgers:company1:all"); ok {
		t.Errorf("expected ledgers:company1:all to be invalidated")
	}
	if v, ok := cache.Get("stock:company1:all"); !ok || v != "val3" {
		t.Errorf("expected stock:company1:all to persist")
	}

	// Expiration
	time.Sleep(60 * time.Millisecond)
	if _, ok := cache.Get("stock:company1:all"); ok {
		t.Errorf("expected stock:company1:all to expire after TTL")
	}
}

func TestFilterLedgersAndFinancialSummary(t *testing.T) {
	mockLedgerXML := `<ENVELOPE>
  <HEADER><STATUS>1</STATUS></HEADER>
  <BODY>
    <DATA>
      <TALLYMESSAGE><LEDGER NAME="HDFC Bank" PARENT="Bank Accounts"><CLOSINGBALANCE>-150000.00</CLOSINGBALANCE></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Cash in Hand" PARENT="Cash-in-Hand"><CLOSINGBALANCE>-25000.00</CLOSINGBALANCE></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Tata Consultancy Services" PARENT="Sundry Debtors"><CLOSINGBALANCE>-85000.00</CLOSINGBALANCE><LEDGERSTATENAME>Maharashtra</LEDGERSTATENAME></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Infosys Ltd" PARENT="Sundry Debtors"><CLOSINGBALANCE>-62000.00</CLOSINGBALANCE><LEDGERSTATENAME>Karnataka</LEDGERSTATENAME></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Wipro Technologies" PARENT="Sundry Debtors"><CLOSINGBALANCE>-12000.00</CLOSINGBALANCE><LEDGERSTATENAME>Karnataka</LEDGERSTATENAME></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Dell Suppliers" PARENT="Sundry Creditors"><CLOSINGBALANCE>45000.00</CLOSINGBALANCE><LEDGERSTATENAME>Delhi</LEDGERSTATENAME></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="HP Distributors" PARENT="Sundry Creditors"><CLOSINGBALANCE>30000.00</CLOSINGBALANCE><LEDGERSTATENAME>Maharashtra</LEDGERSTATENAME></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Domestic Sales" PARENT="Sales Accounts"><CLOSINGBALANCE>-500000.00</CLOSINGBALANCE></LEDGER></TALLYMESSAGE>
      <TALLYMESSAGE><LEDGER NAME="Raw Material Purchases" PARENT="Purchase Accounts"><CLOSINGBALANCE>320000.00</CLOSINGBALANCE></LEDGER></TALLYMESSAGE>
    </DATA>
  </BODY>
</ENVELOPE>`

	var httpCallCount int32
	client := tally.NewClient(config.TallyConfig{
		URL:              "http://mock-tally:9000",
		CacheTTLSeconds: 60,
		DefaultCompany:   "Acme Corp",
	})

	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		atomic.AddInt32(&httpCallCount, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockLedgerXML)),
		}
	}))

	ctx := context.Background()

	// 1. First call fetches from HTTP
	paged, err := client.FilterLedgers(ctx, "", "tata", 10, 0, false)
	if err != nil {
		t.Fatalf("filter ledgers failed: %v", err)
	}
	if paged.Total != 1 {
		t.Errorf("expected 1 result matching 'tata', got %d", paged.Total)
	}
	if atomic.LoadInt32(&httpCallCount) != 1 {
		t.Errorf("expected 1 http call, got %d", httpCallCount)
	}

	// 2. Second call should hit memory cache (0 new HTTP calls)
	pagedCompact, err := client.FilterLedgers(ctx, "", "", 2, 0, true)
	if err != nil {
		t.Fatalf("filter ledgers compact failed: %v", err)
	}
	if pagedCompact.Total != 9 {
		t.Errorf("expected 9 total ledgers, got %d", pagedCompact.Total)
	}
	if pagedCompact.Count != 2 {
		t.Errorf("expected count 2, got %d", pagedCompact.Count)
	}
	if !pagedCompact.HasMore {
		t.Errorf("expected HasMore to be true")
	}
	if atomic.LoadInt32(&httpCallCount) != 1 {
		t.Errorf("expected cache hit with 1 total HTTP call, got %d", httpCallCount)
	}

	// 3. Test Financial Summary
	summary, err := client.GetFinancialSummary(ctx, "20250331", "20240401")
	if err != nil {
		t.Fatalf("get financial summary failed: %v", err)
	}

	// Cash & Bank = 150000 + 25000 = 175000
	if summary.CashAndBank != 175000 {
		t.Errorf("expected CashAndBank 175000, got %.2f", summary.CashAndBank)
	}
	// TotalReceivable = 85000 + 62000 + 12000 = 159000
	if summary.TotalReceivable != 159000 {
		t.Errorf("expected TotalReceivable 159000, got %.2f", summary.TotalReceivable)
	}
	// TotalPayable = 45000 + 30000 = 75000
	if summary.TotalPayable != 75000 {
		t.Errorf("expected TotalPayable 75000, got %.2f", summary.TotalPayable)
	}
	// Top Debtors count = 3
	if len(summary.TopDebtors) != 3 || summary.TopDebtors[0].Name != "Tata Consultancy Services" {
		t.Errorf("unexpected top debtors: %+v", summary.TopDebtors)
	}
	// Working Capital = (175000 + 159000) - 75000 = 259000
	if summary.WorkingCapital != 259000 {
		t.Errorf("expected WorkingCapital 259000, got %.2f", summary.WorkingCapital)
	}
	// Revenue = 500000, Expenses = 320000, Net Profit = 180000
	if summary.RevenueYTD != 500000 || summary.NetProfitYTD != 180000 {
		t.Errorf("expected Revenue 500000 and NetProfit 180000, got Rev: %.2f, NP: %.2f", summary.RevenueYTD, summary.NetProfitYTD)
	}
	if summary.Summary == "" {
		t.Errorf("expected summary text, got empty")
	}

	// 4. Test CreateLedger Cache Invalidation
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		atomic.AddInt32(&httpCallCount, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(`<RESPONSE><STATUS>1</STATUS></RESPONSE>`)),
		}
	}))

	_, err = client.CreateLedger(ctx, "New Debtor", "Sundry Debtors", 1000)
	if err != nil {
		t.Fatalf("create ledger failed: %v", err)
	}

	// After CreateLedger, cache for ledgers should be invalidated
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		atomic.AddInt32(&httpCallCount, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockLedgerXML)),
		}
	}))

	currentCalls := atomic.LoadInt32(&httpCallCount)
	_, _ = client.ListLedgers(ctx, "")
	if atomic.LoadInt32(&httpCallCount) <= currentCalls {
		t.Errorf("expected cache to be invalidated after CreateLedger, triggering new HTTP call")
	}
}

func TestFilterStockItems(t *testing.T) {
	mockStockXML := `<ENVELOPE>
  <HEADER><STATUS>1</STATUS></HEADER>
  <BODY>
    <DATA>
      <TALLYMESSAGE><STOCKITEM NAME="Dell Latitude 5420" PARENT="Laptops"><BASEUNITS>Nos</BASEUNITS><CLOSINGBALANCE>15 Nos</CLOSINGBALANCE><CLOSINGVALUE>-750000.00</CLOSINGVALUE></STOCKITEM></TALLYMESSAGE>
      <TALLYMESSAGE><STOCKITEM NAME="Apple MacBook Pro 14" PARENT="Laptops"><BASEUNITS>Nos</BASEUNITS><CLOSINGBALANCE>8 Nos</CLOSINGBALANCE><CLOSINGVALUE>-1200000.00</CLOSINGVALUE></STOCKITEM></TALLYMESSAGE>
      <TALLYMESSAGE><STOCKITEM NAME="Logitech MX Master 3S" PARENT="Accessories"><BASEUNITS>Nos</BASEUNITS><CLOSINGBALANCE>50 Nos</CLOSINGBALANCE><CLOSINGVALUE>-350000.00</CLOSINGVALUE></STOCKITEM></TALLYMESSAGE>
    </DATA>
  </BODY>
</ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL: "http://mock-tally:9000",
	})
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/xml;charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(mockStockXML)),
		}
	}))

	ctx := context.Background()

	// Search filter
	res, err := client.FilterStockItems(ctx, "", "apple", 10, 0, false)
	if err != nil {
		t.Fatalf("FilterStockItems failed: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("expected 1 result matching 'apple', got %d", res.Total)
	}

	// Compact projection
	compactRes, err := client.FilterStockItems(ctx, "", "", 1, 0, true)
	if err != nil {
		t.Fatalf("FilterStockItems compact failed: %v", err)
	}
	if compactRes.Total != 3 || compactRes.Count != 1 || !compactRes.HasMore {
		t.Errorf("unexpected compact pagination result: %+v", compactRes)
	}
}
