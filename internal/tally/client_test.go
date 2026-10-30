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
		if !strings.Contains(xml, "List of Companies") {
			t.Errorf("expected 'List of Companies' in XML")
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
