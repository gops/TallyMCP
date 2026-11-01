package tally_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

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
