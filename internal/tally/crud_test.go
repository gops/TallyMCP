package tally_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tally-mcp/internal/config"
	"tally-mcp/internal/tally"
)

type fnRoundTripper func(req *http.Request) (*http.Response, error)

func (f fnRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestXMLEscapingAndGSTINValidation(t *testing.T) {
	// Valid GSTINs
	g, err := tally.ValidateGSTIN(" 27aabcu9603r1zm ")
	if err != nil || g != "27AABCU9603R1ZM" {
		t.Fatalf("expected valid normalized GSTIN, got g=%q err=%v", g, err)
	}
	// Invalid GSTIN
	if _, err := tally.ValidateGSTIN("INVALID-GSTIN"); err == nil {
		t.Errorf("expected error for invalid 15-char GSTIN")
	}

	// XML escaping for &, <, >, ", ' in ledger mutation XML
	state := "Maharashtra"
	pan := "AABCU9603R"
	billWise := true
	xmlStr, err := tally.BuildCreateLedgerXMLWithOptions(tally.LedgerWriteOptions{
		Company:            `A & B <Holdings> "India" '26`,
		Name:               `O'Brien & "Sons" <Pvt> Ltd.`,
		Parent:             "Sundry Debtors",
		State:              &state,
		GSTIN:              &g,
		PAN:                &pan,
		Address:            []string{`Plot 1 & 2, "Tech" Park`, `Near <Metro> 'East'`},
		HasAddress:         true,
		MaintainBillByBill: &billWise,
	})
	if err != nil {
		t.Fatalf("BuildCreateLedgerXMLWithOptions failed: %v", err)
	}
	if !strings.Contains(xmlStr, "&amp;") || !strings.Contains(xmlStr, "&lt;") || !strings.Contains(xmlStr, "&gt;") || !strings.Contains(xmlStr, "&quot;") || !strings.Contains(xmlStr, "&apos;") {
		t.Errorf("expected all 5 XML special characters escaped in generated XML:\n%s", xmlStr)
	}

	// Generated XML must parse cleanly with standard encoding/xml
	var parsed tally.TallyEnvelope
	if err := xml.Unmarshal([]byte(xmlStr), &parsed); err != nil {
		t.Fatalf("generated XML is not well-formed: %v\nXML:\n%s", err, xmlStr)
	}
}

func TestCreateVoucher_DebitCreditBalanceEnforcement(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<LEDGER NAME="Cash"><PARENT>Cash-in-Hand</PARENT><CLOSINGBALANCE>-5000.00</CLOSINGBALANCE></LEDGER>
		<LEDGER NAME="Acme Corp"><PARENT>Sundry Debtors</PARENT><CLOSINGBALANCE>-1000.00</CLOSINGBALANCE></LEDGER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000"})
	client.SetTransport(fnRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}, nil
	}))

	ctx := context.Background()

	// Imbalance > 0.01 must fail
	_, err := client.CreateVoucherForCompany(ctx, "", "Receipt", "20261126", "", "", "", "Test",
		[]tally.VoucherEntryInput{
			{LedgerName: "Cash", Amount: 1000.00, IsDebit: true},
			{LedgerName: "Acme Corp", Amount: 999.90, IsDebit: false},
		}, nil, true, false)
	if err == nil || !strings.Contains(err.Error(), "unbalanced") {
		t.Errorf("expected unbalanced error, got: %v", err)
	}

	// Within 0.01 rounding tolerance must succeed (and Contra allows optional party_ledger)
	res, err := client.CreateVoucherForCompany(ctx, "", "Contra", "20261126", "", "", "", "Cash deposit",
		[]tally.VoucherEntryInput{
			{LedgerName: "Cash", Amount: 1000.00, IsDebit: true},
			{LedgerName: "Acme Corp", Amount: 1000.01, IsDebit: false},
		}, nil, true, false)
	if err != nil {
		t.Fatalf("expected within 0.01 rounding to succeed, got: %v", err)
	}
	if res.PartyLedger != "" {
		t.Errorf("expected empty party_ledger for Contra when omitted, got %q", res.PartyLedger)
	}
}

func TestMergeLedgers_GSTINMatchingAndDeleteRefusal(t *testing.T) {
	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<COMPANY NAME="Demo Co"><BOOKSFROM>20250401</BOOKSFROM><ENDINGAT>20260331</ENDINGAT></COMPANY>
		<LEDGER NAME="Dup Party A"><PARENT>Sundry Debtors</PARENT><OPENINGBALANCE>0</OPENINGBALANCE><CLOSINGBALANCE>-500.00</CLOSINGBALANCE>
			<LEDGSTREGDETAILS.LIST><GSTIN>27AABCU9603R1ZM</GSTIN><GSTREGISTRATIONTYPE>Regular</GSTREGISTRATIONTYPE></LEDGSTREGDETAILS.LIST>
		</LEDGER>
		<LEDGER NAME="Target Party B"><PARENT>Sundry Debtors</PARENT><OPENINGBALANCE>0</OPENINGBALANCE><CLOSINGBALANCE>-1000.00</CLOSINGBALANCE>
			<PARTYGSTIN>29AABCU9603R1ZX</PARTYGSTIN>
		</LEDGER>
		<LEDGER NAME="NoGSTIN Party"><PARENT>Sundry Debtors</PARENT><OPENINGBALANCE>0</OPENINGBALANCE><CLOSINGBALANCE>0</CLOSINGBALANCE></LEDGER>
		<VOUCHER MASTERID="55" VOUCHERNUMBER="S-55" DATE="20251010" VOUCHERTYPENAME="Sales" PARTYLEDGERNAME="Dup Party A" AMOUNT="-500.00">
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>Dup Party A</LEDGERNAME><AMOUNT>-500.00</AMOUNT></ALLLEDGERENTRIES.LIST>
			<ALLLEDGERENTRIES.LIST><LEDGERNAME>Sales</LEDGERNAME><AMOUNT>500.00</AMOUNT></ALLLEDGERENTRIES.LIST>
		</VOUCHER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000"})
	client.SetTransport(fnRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}, nil
	}))

	ctx := context.Background()

	// 1. Different GSTINs with require_same_gstin=true must fail
	_, err := client.MergeLedgersForCompany(ctx, "Demo Co", "Dup Party A", "Target Party B", true, true)
	if err == nil || !strings.Contains(err.Error(), "GSTIN verification failed") {
		t.Errorf("expected GSTIN mismatch error, got: %v", err)
	}

	// 2. Empty GSTIN with require_same_gstin=true must fail
	_, err = client.MergeLedgersForCompany(ctx, "Demo Co", "NoGSTIN Party", "Target Party B", true, true)
	if err == nil || !strings.Contains(err.Error(), "GSTIN verification failed") {
		t.Errorf("expected missing GSTIN error, got: %v", err)
	}

	// 3. Delete ledger with non-zero balance/vouchers must refuse and suggest tally_merge_ledgers
	_, err = client.DeleteLedgerForCompany(ctx, "Demo Co", "Dup Party A", false)
	if err == nil || !strings.Contains(err.Error(), "tally_merge_ledgers") || !strings.Contains(err.Error(), "voucher_count=1") {
		t.Errorf("expected safe refusal referencing tally_merge_ledgers and voucher_count=1, got: %v", err)
	}
}

func TestConcurrencyQueue_Serializes10ParallelRequests(t *testing.T) {
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var totalCalls atomic.Int32

	mockXML := `<ENVELOPE><BODY><DATA><COLLECTION>
		<LEDGER NAME="Cash"><PARENT>Cash-in-Hand</PARENT><CLOSINGBALANCE>-100.00</CLOSINGBALANCE></LEDGER>
	</COLLECTION></DATA></BODY></ENVELOPE>`

	client := tally.NewClient(config.TallyConfig{
		URL:             "http://mock-tally:9000",
		DisableCache:    true,
		DisableThrottle: true,
		MaxQueueSize:    50,
	})
	client.SetTransport(fnRoundTripper(func(req *http.Request) (*http.Response, error) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		totalCalls.Add(1)
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockXML)),
			Header:     make(http.Header),
		}, nil
	}))

	ctx := context.Background()
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		idx := i
		go func() {
			_, err := client.ListLedgersForCompany(ctx, "", fmt.Sprintf("Group-%d", idx))
			errCh <- err
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("parallel request %d failed: %v", i, err)
		}
	}

	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("expected strictly serialized maxInFlight=1, got %d", got)
	}
	if got := totalCalls.Load(); got != 10 {
		t.Errorf("expected all 10 requests executed, got %d", got)
	}
}
