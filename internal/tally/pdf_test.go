package tally_test

import (
	"bytes"
	"strings"
	"testing"

	"tally-mcp/internal/tally"
)

func TestGenerateReportPDF(t *testing.T) {
	data := tally.PDFReportData{
		Title:       "Balance Sheet",
		Company:     "Acme International Corp",
		DateRange:   "01-Apr-2024 to 31-Mar-2025",
		Party:       "Sundry Debtors & Creditors",
		VoucherNo:   "BS-2024",
		VoucherType: "Report",
		Headers:     []string{"Particulars", "Schedule", "Amount"},
		Rows: [][]string{
			{"Capital Account", "1", "1,000,000.00"},
			{"Loans (Liability)", "2", "250,000.00"},
			{"Current Liabilities", "3", "180,000.00"},
			{"Fixed Assets", "4", "850,000.00"},
			{"Current Assets", "5", "580,000.00"},
		},
		Summary: map[string]string{
			"Total Liabilities": "1,430,000.00",
			"Total Assets":      "1,430,000.00",
		},
		Notes: "Generated automatically from Tally ERP data.",
	}

	pdfBytes, err := tally.GenerateReportPDF(data)
	if err != nil {
		t.Fatalf("GenerateReportPDF failed: %v", err)
	}

	if len(pdfBytes) == 0 {
		t.Fatalf("expected non-empty PDF bytes")
	}

	// Verify standard PDF-1.4 header
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-1.4\n")) {
		t.Errorf("expected PDF-1.4 header, got %q", string(pdfBytes[:15]))
	}

	// Verify PDF trailer and EOF
	str := string(pdfBytes)
	if !strings.Contains(str, "%%EOF") {
		t.Errorf("missing %%EOF trailer in PDF")
	}
	if !strings.Contains(str, "/Type /Catalog") {
		t.Errorf("missing Catalog object in PDF")
	}
	if !strings.Contains(str, "/Type /Pages") {
		t.Errorf("missing Pages object in PDF")
	}
	if !strings.Contains(str, "xref\n0 7") {
		t.Errorf("missing xref table in PDF")
	}
	if !strings.Contains(str, "Acme International Corp") {
		t.Errorf("expected company name inside PDF stream")
	}
	if !strings.Contains(str, "Balance Sheet") {
		t.Errorf("expected report title inside PDF stream")
	}
}
