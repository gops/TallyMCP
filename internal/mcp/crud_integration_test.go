package mcp_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"tally-mcp/internal/config"
	mcppkg "tally-mcp/internal/mcp"
	"tally-mcp/internal/tally"
)

// statefulTallyMock simulates multi-company Tally XML HTTP responses across read, create, update, merge, and delete flows.
type statefulTallyMock struct {
	mu        sync.Mutex
	companies []tally.CompanyItem
	groups    map[string][]tally.GroupItem
	ledgers   map[string]map[string]*tally.LedgerDetail // company -> lower(name) -> detail
	vouchers  map[string]map[string]*tally.VoucherDetail // company -> masterID -> detail
	nextID    int
}

func newStatefulTallyMock() *statefulTallyMock {
	m := &statefulTallyMock{
		companies: []tally.CompanyItem{
			{Name: "Alpha & Co", BooksFrom: "20250401", EndingAt: "20260331", IsActiveCompany: "Yes"},
			{Name: "Beta Pvt. Ltd.", BooksFrom: "20250401", EndingAt: "20260331"},
		},
		groups: map[string][]tally.GroupItem{
			"Alpha & Co": {
				{Name: "Sundry Debtors", Parent: "Current Assets", OpeningBalance: 0, ClosingBalance: -26800},
				{Name: "Suspense A/c", Parent: "Current Liabilities", OpeningBalance: 0, ClosingBalance: -5000},
				{Name: "Indirect Expenses", Parent: "Primary", OpeningBalance: 0, ClosingBalance: -3000},
			},
			"Beta Pvt. Ltd.": {
				{Name: "Sundry Debtors", Parent: "Current Assets", OpeningBalance: 0, ClosingBalance: -1000},
			},
		},
		ledgers:  make(map[string]map[string]*tally.LedgerDetail),
		vouchers: make(map[string]map[string]*tally.VoucherDetail),
		nextID:   500,
	}

	m.ledgers["Alpha & Co"] = map[string]*tally.LedgerDetail{
		"dup acme": {
			Name: "Dup Acme", Parent: "Sundry Debtors", State: "Maharashtra",
			GSTIN: "27AABCU9603R1ZM", GSTRegistrationType: "Regular", PAN: "AABCU9603R",
			OpeningBalance: -1000, ClosingBalance: -12800, MaintainBillByBill: true,
			OpeningBillAllocations: []tally.BillAllocation{{Name: "OP-1", BillType: "New Ref", Amount: -1000}},
		},
		"acme & co": {
			Name: "Acme & Co", Parent: "Sundry Debtors", State: "Maharashtra",
			GSTIN: "27AABCU9603R1ZM", GSTRegistrationType: "Regular", PAN: "AABCU9603R",
			OpeningBalance: -2000, ClosingBalance: -14000, MaintainBillByBill: true,
			OpeningBillAllocations: []tally.BillAllocation{{Name: "OP-2", BillType: "New Ref", Amount: -2000}},
		},
		"suspance a/c.": {
			Name: "SUSPANCE A/C.", Parent: "Suspense A/c", OpeningBalance: 0, ClosingBalance: -5000,
		},
		"office rent": {
			Name: "Office Rent", Parent: "Indirect Expenses", OpeningBalance: 0, ClosingBalance: -3000,
		},
		"hdfc bank": {
			Name: "HDFC Bank", Parent: "Bank Accounts", OpeningBalance: 0, ClosingBalance: 8000,
		},
		"sales account": {
			Name: "Sales Account", Parent: "Sales Accounts", OpeningBalance: 0, ClosingBalance: 23800,
		},
		"unused ledger": {
			Name: "Unused Ledger", Parent: "Sundry Creditors", OpeningBalance: 0, ClosingBalance: 0,
		},
	}

	m.ledgers["Beta Pvt. Ltd."] = map[string]*tally.LedgerDetail{
		"beta buyer": {
			Name: "Beta Buyer", Parent: "Sundry Debtors", State: "Karnataka",
			GSTIN: "29AABCB1234F1Z5", GSTRegistrationType: "Regular", PAN: "AABCB1234F",
			OpeningBalance: 0, ClosingBalance: -1000, MaintainBillByBill: true,
		},
	}

	m.vouchers["Alpha & Co"] = map[string]*tally.VoucherDetail{
		"100": {
			MasterID: "100", VoucherTypeName: "Sales", VoucherNumber: "S-100", Date: "20251005",
			PartyLedgerName: "Acme & Co", Amount: -12000, Narration: "Existing invoice to Acme & Co",
			LedgerEntries: []tally.VoucherLedgerEntry{
				{LedgerName: "Acme & Co", Amount: -12000, BillAllocations: []tally.BillAllocation{{Name: "S-100", BillType: "New Ref", Amount: -12000}}},
				{LedgerName: "Sales Account", Amount: 12000},
			},
		},
		"101": {
			MasterID: "101", VoucherTypeName: "Sales", VoucherNumber: "S-101", Date: "20251010",
			PartyLedgerName: "Dup Acme", Amount: -11800, Narration: "Invoice to Dup Acme",
			LedgerEntries: []tally.VoucherLedgerEntry{
				{LedgerName: "Dup Acme", Amount: -11800, BillAllocations: []tally.BillAllocation{{Name: "S-101", BillType: "New Ref", Amount: -11800}}},
				{LedgerName: "Sales Account", Amount: 11800},
			},
		},
		"102": {
			MasterID: "102", VoucherTypeName: "Payment", VoucherNumber: "P-102", Date: "20251015",
			PartyLedgerName: "HDFC Bank", Amount: -5000, Narration: "Unresolved entry",
			LedgerEntries: []tally.VoucherLedgerEntry{
				{LedgerName: "SUSPANCE A/C.", Amount: -5000},
				{LedgerName: "HDFC Bank", Amount: 5000},
			},
		},
		"103": {
			MasterID: "103", VoucherTypeName: "Payment", VoucherNumber: "P-103", Date: "20251020",
			PartyLedgerName: "HDFC Bank", Amount: -3000, Narration: "Oct rent",
			LedgerEntries: []tally.VoucherLedgerEntry{
				{LedgerName: "Office Rent", Amount: -3000},
				{LedgerName: "HDFC Bank", Amount: 3000},
			},
		},
	}
	m.vouchers["Beta Pvt. Ltd."] = map[string]*tally.VoucherDetail{}
	return m
}

var (
	reTagCompany = regexp.MustCompile(`(?is)<SVCURRENTCOMPANY>(.*?)</SVCURRENTCOMPANY>`)
	reTagID      = regexp.MustCompile(`(?is)<ID>(.*?)</ID>`)
	reFormula    = regexp.MustCompile(`(?is)<SYSTEM[^>]*>(.*?)</SYSTEM>`)
)

func unescapeXMLSimple(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&apos;", `'`)
	return s
}

func (m *statefulTallyMock) RoundTrip(req *http.Request) (*http.Response, error) {
	bodyBytes, _ := io.ReadAll(req.Body)
	body := string(bodyBytes)

	m.mu.Lock()
	defer m.mu.Unlock()

	company := "Alpha & Co"
	if cm := reTagCompany.FindStringSubmatch(body); len(cm) > 1 {
		company = unescapeXMLSimple(strings.TrimSpace(cm[1]))
	}

	var respXML string
	switch {
	case strings.Contains(body, "<ID>Company</ID>"):
		var b strings.Builder
		b.WriteString(`<ENVELOPE><BODY><DATA><COLLECTION>`)
		for _, c := range m.companies {
			b.WriteString(fmt.Sprintf(`<COMPANY NAME="%s"><BOOKSFROM>%s</BOOKSFROM><ENDINGAT>%s</ENDINGAT><ISACTIVECOMPANY>%s</ISACTIVECOMPANY></COMPANY>`,
				tally.XMLEscapeAttr(c.Name), c.BooksFrom, c.EndingAt, c.IsActiveCompany))
		}
		b.WriteString(`</COLLECTION></DATA></BODY></ENVELOPE>`)
		respXML = b.String()

	case strings.Contains(body, "<ID>Bills Payable</ID>"):
		respXML = `<ENVELOPE><BODY><DATA>
			<BILLFIXED><BILLDATE>10-Oct-25</BILLDATE><BILLREF>BILL-99</BILLREF><BILLPARTY>Vendor X</BILLPARTY></BILLFIXED>
			<BILLCL>4500.00 Cr</BILLCL>
			<BILLDUE>25-Oct-25</BILLDUE>
			<BILLOVERDUE>32 Days</BILLOVERDUE>
		</DATA></BODY></ENVELOPE>`

	case strings.Contains(body, "<ID>Bills Receivable</ID>"):
		respXML = `<ENVELOPE><BODY><DATA>
			<BILLFIXED><BILLDATE>10-Oct-25</BILLDATE><BILLREF>S-101</BILLREF><BILLPARTY>Acme &amp; Co</BILLPARTY></BILLFIXED>
			<BILLCL>11800.00 Dr</BILLCL>
			<BILLDUE>25-Oct-25</BILLDUE>
			<BILLOVERDUE>15 Days</BILLOVERDUE>
		</DATA></BODY></ENVELOPE>`

	case strings.Contains(body, "<ID>GroupColl</ID>"):
		var b strings.Builder
		b.WriteString(`<ENVELOPE><BODY><DATA><COLLECTION>`)
		for _, g := range m.groups[company] {
			b.WriteString(fmt.Sprintf(`<GROUP NAME="%s"><PARENT>%s</PARENT><OPENINGBALANCE>%.2f</OPENINGBALANCE><CLOSINGBALANCE>%.2f</CLOSINGBALANCE></GROUP>`,
				tally.XMLEscapeAttr(g.Name), tally.XMLEscapeAttr(g.Parent), g.OpeningBalance, g.ClosingBalance))
		}
		b.WriteString(`</COLLECTION></DATA></BODY></ENVELOPE>`)
		respXML = b.String()

	case strings.Contains(body, "<ID>LedgerColl</ID>"):
		var b strings.Builder
		b.WriteString(`<ENVELOPE><BODY><DATA><COLLECTION>`)
		for _, l := range m.ledgers[company] {
			b.WriteString(m.renderLedgerXML(l))
		}
		b.WriteString(`</COLLECTION></DATA></BODY></ENVELOPE>`)
		respXML = b.String()

	case strings.Contains(body, "<ID>SingleLedgerColl</ID>"):
		target := ""
		if fm := reFormula.FindStringSubmatch(body); len(fm) > 1 {
			if idx := strings.Index(fm[1], `"`); idx >= 0 {
				rest := fm[1][idx+1:]
				if end := strings.Index(rest, `"`); end >= 0 {
					target = unescapeXMLSimple(rest[:end])
				}
			}
		}
		var b strings.Builder
		b.WriteString(`<ENVELOPE><BODY><DATA><COLLECTION>`)
		if l, ok := m.ledgers[company][strings.ToLower(strings.TrimSpace(target))]; ok {
			b.WriteString(m.renderLedgerXML(l))
		}
		b.WriteString(`</COLLECTION></DATA></BODY></ENVELOPE>`)
		respXML = b.String()

	case strings.Contains(body, "<ID>LedgerStatementColl</ID>") || strings.Contains(body, "<ID>VoucherColl</ID>"):
		var b strings.Builder
		b.WriteString(`<ENVELOPE><BODY><DATA><COLLECTION>`)
		for _, v := range m.vouchers[company] {
			b.WriteString(m.renderVoucherXML(v))
		}
		b.WriteString(`</COLLECTION></DATA></BODY></ENVELOPE>`)
		respXML = b.String()

	case strings.Contains(body, "<ID>SingleVoucherColl</ID>"):
		targetID := ""
		if fm := reFormula.FindStringSubmatch(body); len(fm) > 1 {
			s := strings.TrimSpace(fm[1])
			if strings.HasPrefix(s, "$MasterId = ") {
				targetID = strings.TrimSpace(strings.TrimPrefix(s, "$MasterId = "))
			}
		}
		var b strings.Builder
		b.WriteString(`<ENVELOPE><BODY><DATA><COLLECTION>`)
		if v, ok := m.vouchers[company][targetID]; ok {
			b.WriteString(m.renderVoucherXML(v))
		}
		b.WriteString(`</COLLECTION></DATA></BODY></ENVELOPE>`)
		respXML = b.String()

	case strings.Contains(body, "<TALLYREQUEST>Import") && strings.Contains(body, "<LEDGER "):
		respXML = m.handleLedgerImport(company, body)

	case strings.Contains(body, "<TALLYREQUEST>Import") && strings.Contains(body, "<VOUCHER "):
		respXML = m.handleVoucherImport(company, body)

	default:
		_ = reTagID.FindStringSubmatch(body)
		respXML = `<ENVELOPE><BODY><DATA><COLLECTION></COLLECTION></DATA></BODY></ENVELOPE>`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(respXML)),
		Header:     make(http.Header),
	}, nil
}

func (m *statefulTallyMock) renderLedgerXML(l *tally.LedgerDetail) string {
	bw := "No"
	if l.MaintainBillByBill {
		bw = "Yes"
	}
	var bills strings.Builder
	for _, b := range l.OpeningBillAllocations {
		bills.WriteString(fmt.Sprintf(`<BILLALLOCATIONS.LIST><NAME>%s</NAME><BILLTYPE>%s</BILLTYPE><AMOUNT>%.2f</AMOUNT></BILLALLOCATIONS.LIST>`,
			tally.XMLEscapeValue(b.Name), tally.XMLEscapeValue(b.BillType), b.Amount))
	}
	return fmt.Sprintf(`<LEDGER NAME="%s"><PARENT>%s</PARENT><OPENINGBALANCE>%.2f</OPENINGBALANCE><CLOSINGBALANCE>%.2f</CLOSINGBALANCE><LEDGERSTATENAME>%s</LEDGERSTATENAME><INCOMETAXNUMBER>%s</INCOMETAXNUMBER><ISBILLWISEON>%s</ISBILLWISEON><LEDGSTREGDETAILS.LIST><GSTIN>%s</GSTIN><GSTREGISTRATIONTYPE>%s</GSTREGISTRATIONTYPE></LEDGSTREGDETAILS.LIST>%s</LEDGER>`,
		tally.XMLEscapeAttr(l.Name), tally.XMLEscapeValue(l.Parent),
		l.OpeningBalance, l.ClosingBalance, tally.XMLEscapeValue(l.State),
		tally.XMLEscapeValue(l.PAN), bw,
		tally.XMLEscapeValue(l.GSTIN), tally.XMLEscapeValue(l.GSTRegistrationType),
		bills.String(),
	)
}

func (m *statefulTallyMock) renderVoucherXML(v *tally.VoucherDetail) string {
	var legs strings.Builder
	for _, e := range v.LedgerEntries {
		var bills strings.Builder
		for _, b := range e.BillAllocations {
			bills.WriteString(fmt.Sprintf(`<BILLALLOCATIONS.LIST><NAME>%s</NAME><BILLTYPE>%s</BILLTYPE><AMOUNT>%.2f</AMOUNT></BILLALLOCATIONS.LIST>`,
				tally.XMLEscapeValue(b.Name), tally.XMLEscapeValue(b.BillType), b.Amount))
		}
		legs.WriteString(fmt.Sprintf(`<ALLLEDGERENTRIES.LIST><LEDGERNAME>%s</LEDGERNAME><AMOUNT>%.2f</AMOUNT>%s</ALLLEDGERENTRIES.LIST>`,
			tally.XMLEscapeValue(e.LedgerName), e.Amount, bills.String()))
	}
	return fmt.Sprintf(`<VOUCHER MASTERID="%s" VOUCHERNUMBER="%s" DATE="%s" VOUCHERTYPENAME="%s" PARTYLEDGERNAME="%s" AMOUNT="%.2f"><MASTERID>%s</MASTERID><VOUCHERNUMBER>%s</VOUCHERNUMBER><DATE>%s</DATE><VOUCHERTYPENAME>%s</VOUCHERTYPENAME><REFERENCE>%s</REFERENCE><PARTYLEDGERNAME>%s</PARTYLEDGERNAME><NARRATION>%s</NARRATION><AMOUNT>%.2f</AMOUNT>%s</VOUCHER>`,
		tally.XMLEscapeAttr(v.MasterID), tally.XMLEscapeAttr(v.VoucherNumber), v.Date, tally.XMLEscapeAttr(v.VoucherTypeName), tally.XMLEscapeAttr(v.PartyLedgerName), v.Amount,
		tally.XMLEscapeValue(v.MasterID), tally.XMLEscapeValue(v.VoucherNumber), v.Date, tally.XMLEscapeValue(v.VoucherTypeName), tally.XMLEscapeValue(v.Reference), tally.XMLEscapeValue(v.PartyLedgerName), tally.XMLEscapeValue(v.Narration), v.Amount,
		legs.String(),
	)
}

func (m *statefulTallyMock) recomputeClosingBalances(company string) {
	for _, l := range m.ledgers[company] {
		sum := l.OpeningBalance
		for _, v := range m.vouchers[company] {
			for _, e := range v.LedgerEntries {
				if strings.EqualFold(e.LedgerName, l.Name) {
					sum += e.Amount
				}
			}
		}
		l.ClosingBalance = sum
	}
}

func (m *statefulTallyMock) handleLedgerImport(company, body string) string {
	type parsedLedger struct {
		NameAttr     string                 `xml:"NAME,attr"`
		ActionAttr   string                 `xml:"ACTION,attr"`
		Name         string                 `xml:"NAME"`
		Parent       string                 `xml:"PARENT"`
		Opening      *float64               `xml:"OPENINGBALANCE"`
		State        string                 `xml:"LEDGERSTATENAME"`
		PAN          string                 `xml:"INCOMETAXNUMBER"`
		GSTIN        string                 `xml:"PARTYGSTIN"`
		GSTRegType   string                 `xml:"GSTREGISTRATIONTYPE"`
		IsBillWise   string                 `xml:"ISBILLWISEON"`
		OpeningBills []tally.BillAllocation `xml:"BILLALLOCATIONS.LIST"`
	}
	var env struct {
		Body struct {
			Data struct {
				Msgs []struct {
					Ledger *parsedLedger `xml:"LEDGER"`
				} `xml:"TALLYMESSAGE"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}
	if err := xml.Unmarshal([]byte(body), &env); err != nil || len(env.Body.Data.Msgs) == 0 || env.Body.Data.Msgs[0].Ledger == nil {
		return `<RESPONSE><ERRORS>1</ERRORS><LINEERROR>Invalid ledger XML</LINEERROR></RESPONSE>`
	}
	in := env.Body.Data.Msgs[0].Ledger
	compMap := m.ledgers[company]
	key := strings.ToLower(strings.TrimSpace(in.NameAttr))

	switch strings.ToLower(in.ActionAttr) {
	case "delete":
		delete(compMap, key)
		return `<RESPONSE><DELETED>1</DELETED><ERRORS>0</ERRORS></RESPONSE>`
	case "create":
		l := &tally.LedgerDetail{
			Name:                in.Name,
			Parent:              in.Parent,
			State:               in.State,
			PAN:                 in.PAN,
			GSTIN:               in.GSTIN,
			GSTRegistrationType: in.GSTRegType,
			MaintainBillByBill:  strings.EqualFold(in.IsBillWise, "Yes"),
		}
		if in.Opening != nil {
			l.OpeningBalance = *in.Opening
			l.ClosingBalance = *in.Opening
		}
		compMap[strings.ToLower(strings.TrimSpace(l.Name))] = l
		m.recomputeClosingBalances(company)
		return `<RESPONSE><CREATED>1</CREATED><ERRORS>0</ERRORS></RESPONSE>`
	case "alter":
		existing, ok := compMap[key]
		if !ok {
			return `<RESPONSE><ERRORS>1</ERRORS><LINEERROR>Ledger not found</LINEERROR></RESPONSE>`
		}
		if in.Name != "" && !strings.EqualFold(in.Name, existing.Name) {
			delete(compMap, key)
			// Also update vouchers on rename in Tally
			for _, v := range m.vouchers[company] {
				if strings.EqualFold(v.PartyLedgerName, existing.Name) {
					v.PartyLedgerName = in.Name
				}
				for i := range v.LedgerEntries {
					if strings.EqualFold(v.LedgerEntries[i].LedgerName, existing.Name) {
						v.LedgerEntries[i].LedgerName = in.Name
					}
				}
			}
			existing.Name = in.Name
			compMap[strings.ToLower(strings.TrimSpace(in.Name))] = existing
		}
		if in.Parent != "" {
			existing.Parent = in.Parent
		}
		if in.Opening != nil {
			existing.OpeningBalance = *in.Opening
		}
		if in.State != "" {
			existing.State = in.State
		}
		if in.PAN != "" {
			existing.PAN = in.PAN
		}
		if in.GSTIN != "" {
			existing.GSTIN = in.GSTIN
		}
		if in.GSTRegType != "" {
			existing.GSTRegistrationType = in.GSTRegType
		}
		if in.IsBillWise != "" {
			existing.MaintainBillByBill = strings.EqualFold(in.IsBillWise, "Yes")
		}
		if len(in.OpeningBills) > 0 || strings.Contains(body, "<OPENINGBALANCE>0.00</OPENINGBALANCE>") {
			existing.OpeningBillAllocations = in.OpeningBills
		}
		m.recomputeClosingBalances(company)
		return `<RESPONSE><ALTERED>1</ALTERED><ERRORS>0</ERRORS></RESPONSE>`
	}
	return `<RESPONSE><ERRORS>1</ERRORS></RESPONSE>`
}

func (m *statefulTallyMock) handleVoucherImport(company, body string) string {
	type parsedV struct {
		ActionAttr      string                     `xml:"ACTION,attr"`
		MasterID        string                     `xml:"MASTERID"`
		Date            string                     `xml:"DATE"`
		VoucherTypeName string                     `xml:"VOUCHERTYPENAME"`
		VoucherNumber   string                     `xml:"VOUCHERNUMBER"`
		Reference       string                     `xml:"REFERENCE"`
		PartyLedgerName string                     `xml:"PARTYLEDGERNAME"`
		Narration       string                     `xml:"NARRATION"`
		Entries         []tally.VoucherLedgerEntry `xml:"ALLLEDGERENTRIES.LIST"`
	}
	var env struct {
		Body struct {
			ImportData struct {
				RequestData struct {
					Msgs []struct {
						Voucher *parsedV `xml:"VOUCHER"`
					} `xml:"TALLYMESSAGE"`
				} `xml:"REQUESTDATA"`
			} `xml:"IMPORTDATA"`
		} `xml:"BODY"`
	}
	if err := xml.Unmarshal([]byte(body), &env); err != nil || len(env.Body.ImportData.RequestData.Msgs) == 0 || env.Body.ImportData.RequestData.Msgs[0].Voucher == nil {
		return `<RESPONSE><ERRORS>1</ERRORS><LINEERROR>Invalid voucher XML</LINEERROR></RESPONSE>`
	}
	in := env.Body.ImportData.RequestData.Msgs[0].Voucher
	vMap := m.vouchers[company]

	switch strings.ToLower(in.ActionAttr) {
	case "delete":
		delete(vMap, in.MasterID)
		m.recomputeClosingBalances(company)
		return `<RESPONSE><DELETED>1</DELETED><ERRORS>0</ERRORS></RESPONSE>`
	case "create":
		m.nextID++
		id := fmt.Sprintf("%d", m.nextID)
		vMap[id] = &tally.VoucherDetail{
			MasterID:        id,
			Date:            in.Date,
			VoucherTypeName: in.VoucherTypeName,
			VoucherNumber:   in.VoucherNumber,
			Reference:       in.Reference,
			PartyLedgerName: in.PartyLedgerName,
			Narration:       in.Narration,
			LedgerEntries:   in.Entries,
		}
		m.recomputeClosingBalances(company)
		return fmt.Sprintf(`<RESPONSE><CREATED>1</CREATED><LASTVCHID>%s</LASTVCHID><ERRORS>0</ERRORS></RESPONSE>`, id)
	case "alter":
		v, ok := vMap[in.MasterID]
		if !ok {
			return `<RESPONSE><ERRORS>1</ERRORS><LINEERROR>Voucher not found</LINEERROR></RESPONSE>`
		}
		v.Date = in.Date
		v.VoucherNumber = in.VoucherNumber
		v.Reference = in.Reference
		v.PartyLedgerName = in.PartyLedgerName
		v.Narration = in.Narration
		v.LedgerEntries = in.Entries
		m.recomputeClosingBalances(company)
		return `<RESPONSE><ALTERED>1</ALTERED><ERRORS>0</ERRORS></RESPONSE>`
	}
	return `<RESPONSE><ERRORS>1</ERRORS></RESPONSE>`
}

func TestFullWorkflowIntegration_MultiCompanyDryRunCRUDMerge(t *testing.T) {
	mock := newStatefulTallyMock()
	client := tally.NewClient(config.TallyConfig{URL: "http://mock-tally:9000", CacheTTLSeconds: 30})
	client.SetTransport(mock)

	cfg := config.DefaultConfig()
	srv := mcppkg.NewServer(cfg, client, "v1.0.25")
	handlers := mcppkg.NewToolHandlers(client, "v1.0.25", "gops/TallyMCP", "json")
	ctx := context.Background()

	// 1. Tool registration parity & absence of tally_execute_xml
	listResp := string(srv.ServeRelayMCP(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`), true))
	if strings.Contains(listResp, `"tally_execute_xml"`) {
		t.Fatalf("tally_execute_xml must NOT be registered")
	}
	for _, requiredTool := range []string{
		"tally_get_outstanding_payables",
		"tally_get_outstanding_receivables",
		"tally_get_ledger_vouchers",
		"tally_list_groups",
		"tally_get_group_summary",
		"tally_create_ledger",
		"tally_update_ledger",
		"tally_delete_ledger",
		"tally_merge_ledgers",
		"tally_create_voucher",
		"tally_update_voucher",
		"tally_delete_voucher",
	} {
		if !strings.Contains(listResp, `"`+requiredTool+`"`) {
			t.Errorf("expected tool %q to be registered in tools/list", requiredTool)
		}
	}

	// 2. tally_get_outstanding_payables works end-to-end via MCP dispatch
	payResp := string(srv.ServeRelayMCP(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tally_get_outstanding_payables","arguments":{"company":"Alpha & Co"}}}`), false))
	if !strings.Contains(payResp, "BILL-99") || !strings.Contains(payResp, "Vendor X") {
		t.Errorf("expected BILL-99 and Vendor X from tally_get_outstanding_payables, got: %s", payResp)
	}

	// 3. Upgraded tally_list_ledgers & tally_get_ledger fields + filters
	listLRes, err := handlers.HandleListLedgers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_list_ledgers",
			Arguments: map[string]any{"company": "Alpha & Co", "gstin": "27AABCU9603R1ZM", "non_zero_only": true},
		},
	})
	if err != nil || listLRes.IsError {
		t.Fatalf("HandleListLedgers failed: err=%v res=%+v", err, listLRes)
	}
	lTxt := listLRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(lTxt, "27AABCU9603R1ZM") || !strings.Contains(lTxt, `"maintain_bill_by_bill":true`) || !strings.Contains(lTxt, `"pan":"AABCU9603R"`) {
		t.Errorf("expected GSTIN, PAN, and maintain_bill_by_bill in tally_list_ledgers, got: %s", lTxt)
	}

	// 4. Multi-company validation: switching to Beta Pvt. Ltd. works; unknown company fails
	betaRes, err := handlers.HandleListLedgers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_list_ledgers", Arguments: map[string]any{"company": "Beta Pvt. Ltd."}},
	})
	if err != nil || betaRes.IsError || !strings.Contains(betaRes.Content[0].(mcp.TextContent).Text, "Beta Buyer") {
		t.Fatalf("expected Beta Buyer for Beta Pvt. Ltd., got err=%v res=%+v", err, betaRes)
	}
	badCompRes, _ := handlers.HandleListLedgers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_list_ledgers", Arguments: map[string]any{"company": "Nonexistent Corp"}},
	})
	if !badCompRes.IsError {
		t.Errorf("expected error for unknown company")
	}

	// 5. tally_get_ledger_vouchers for expense ledger ("Office Rent" is in ALLLEDGERENTRIES, NOT PARTYLEDGERNAME)
	lvRes, err := handlers.HandleGetLedgerVouchers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_get_ledger_vouchers",
			Arguments: map[string]any{"company": "Alpha & Co", "ledger_name": "Office Rent", "from_date": "20250401", "to_date": "20260331"},
		},
	})
	if err != nil || lvRes.IsError {
		t.Fatalf("HandleGetLedgerVouchers failed: err=%v res=%+v", err, lvRes)
	}
	lvTxt := lvRes.Content[0].(mcp.TextContent).Text
	if !strings.Contains(lvTxt, `"master_id":"103"`) || !strings.Contains(lvTxt, `"debit":3000`) || !strings.Contains(lvTxt, "HDFC Bank") {
		t.Errorf("expected master_id=103, debit=3000, HDFC Bank for Office Rent, got: %s", lvTxt)
	}

	// 6. tally_list_groups & tally_get_group_summary
	gsRes, err := handlers.HandleGetGroupSummary(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_get_group_summary", Arguments: map[string]any{"company": "Alpha & Co", "group": "Suspense A/c"}},
	})
	if err != nil || gsRes.IsError || !strings.Contains(gsRes.Content[0].(mcp.TextContent).Text, "SUSPANCE A/C.") {
		t.Fatalf("HandleGetGroupSummary failed: err=%v res=%+v", err, gsRes)
	}

	// 7. tally_update_ledger: dry_run=true vs live, plus rename collision check
	updDry, err := handlers.HandleUpdateLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_update_ledger",
			Arguments: map[string]any{"company": "Beta Pvt. Ltd.", "name": "Beta Buyer", "new_name": "Beta Buyer Updated", "dry_run": true},
		},
	})
	if err != nil || updDry.IsError || !strings.Contains(updDry.Content[0].(mcp.TextContent).Text, `"status":"preview"`) {
		t.Fatalf("HandleUpdateLedger dry_run failed: err=%v res=%+v", err, updDry)
	}
	if _, exists := mock.ledgers["Beta Pvt. Ltd."]["beta buyer"]; !exists {
		t.Fatalf("dry_run must NOT rename ledger in Tally")
	}

	updLive, err := handlers.HandleUpdateLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_update_ledger",
			Arguments: map[string]any{"company": "Beta Pvt. Ltd.", "name": "Beta Buyer", "new_name": "Beta Buyer Updated", "dry_run": false},
		},
	})
	if err != nil || updLive.IsError || !strings.Contains(updLive.Content[0].(mcp.TextContent).Text, `"status":"updated"`) {
		t.Fatalf("HandleUpdateLedger live failed: err=%v res=%+v", err, updLive)
	}
	if _, exists := mock.ledgers["Beta Pvt. Ltd."]["beta buyer updated"]; !exists {
		t.Fatalf("expected ledger to be renamed in Beta Pvt. Ltd.")
	}

	// Rename collision check on Alpha & Co
	collRes, _ := handlers.HandleUpdateLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_update_ledger",
			Arguments: map[string]any{"company": "Alpha & Co", "name": "Dup Acme", "new_name": "Acme & Co"},
		},
	})
	if !collRes.IsError || !strings.Contains(collRes.Content[0].(mcp.TextContent).Text, "already exists") {
		t.Errorf("expected rename collision error, got %+v", collRes)
	}

	// 8. tally_merge_ledgers: dry_run=true then dry_run=false
	mergeDry, err := handlers.HandleMergeLedgers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_merge_ledgers",
			Arguments: map[string]any{"company": "Alpha & Co", "source_ledger": "Dup Acme", "target_ledger": "Acme & Co", "dry_run": true},
		},
	})
	if err != nil || mergeDry.IsError {
		t.Fatalf("HandleMergeLedgers dry_run failed: err=%v res=%+v", err, mergeDry)
	}
	mdTxt := mergeDry.Content[0].(mcp.TextContent).Text
	if !strings.Contains(mdTxt, `"voucher_count":1`) || !strings.Contains(mdTxt, `"expected_target_closing_balance":-26800`) {
		t.Errorf("unexpected dry_run merge plan: %s", mdTxt)
	}

	mergeLive, err := handlers.HandleMergeLedgers(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tally_merge_ledgers",
			Arguments: map[string]any{"company": "Alpha & Co", "source_ledger": "Dup Acme", "target_ledger": "Acme & Co", "dry_run": false},
		},
	})
	if err != nil || mergeLive.IsError {
		t.Fatalf("HandleMergeLedgers live failed: err=%v res=%+v", err, mergeLive)
	}
	mlTxt := mergeLive.Content[0].(mcp.TextContent).Text
	if !strings.Contains(mlTxt, `"status":"merged"`) || !strings.Contains(mlTxt, `"source_deleted":true`) || !strings.Contains(mlTxt, `"verified_target_closing_balance":-26800`) {
		t.Errorf("unexpected live merge result: %s", mlTxt)
	}
	if _, stillThere := mock.ledgers["Alpha & Co"]["dup acme"]; stillThere {
		t.Errorf("expected Dup Acme to be deleted after merge verification")
	}

	// 9. tally_update_voucher: reclassify SUSPANCE A/C. -> Acme & Co with Agst Ref + dry_run vs live
	uvDry, err := handlers.HandleUpdateVoucher(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_update_voucher",
			Arguments: map[string]any{
				"company":   "Alpha & Co",
				"master_id": "102",
				"new_date":  "2025-10-16",
				"narration": "Cleared suspense against S-101",
				"reclassify_ledger": map[string]any{
					"from_ledger": "SUSPANCE A/C.",
					"to_ledger":   "Acme & Co",
					"bill_allocations": []map[string]any{
						{"bill_name": "S-101", "bill_type": "Agst Ref", "amount": 5000},
					},
				},
				"dry_run": true,
			},
		},
	})
	if err != nil || uvDry.IsError || !strings.Contains(uvDry.Content[0].(mcp.TextContent).Text, `"status":"preview"`) {
		t.Fatalf("HandleUpdateVoucher dry_run failed: err=%v res=%+v", err, uvDry)
	}
	if mock.vouchers["Alpha & Co"]["102"].Date != "20251015" {
		t.Fatalf("dry_run must NOT alter voucher date")
	}

	uvLive, err := handlers.HandleUpdateVoucher(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tally_update_voucher",
			Arguments: map[string]any{
				"company":   "Alpha & Co",
				"master_id": "102",
				"new_date":  "2025-10-16",
				"narration": "Cleared suspense against S-101",
				"reclassify_ledger": map[string]any{
					"from_ledger": "SUSPANCE A/C.",
					"to_ledger":   "Acme & Co",
					"bill_allocations": []map[string]any{
						{"bill_name": "S-101", "bill_type": "Agst Ref", "amount": 5000},
					},
				},
				"dry_run": false,
			},
		},
	})
	if err != nil || uvLive.IsError || !strings.Contains(uvLive.Content[0].(mcp.TextContent).Text, `"status":"updated"`) {
		t.Fatalf("HandleUpdateVoucher live failed: err=%v res=%+v", err, uvLive)
	}
	if mock.vouchers["Alpha & Co"]["102"].Date != "20251016" || mock.vouchers["Alpha & Co"]["102"].LedgerEntries[0].LedgerName != "Acme & Co" {
		t.Errorf("expected voucher 102 reclassified and re-dated, got %+v", mock.vouchers["Alpha & Co"]["102"])
	}

	// 10. tally_delete_voucher dry_run=true then dry_run=false, and tally_delete_ledger on unused ledger
	dvDry, err := handlers.HandleDeleteVoucher(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_delete_voucher", Arguments: map[string]any{"company": "Alpha & Co", "master_id": "103", "dry_run": true}},
	})
	if err != nil || dvDry.IsError || !strings.Contains(dvDry.Content[0].(mcp.TextContent).Text, `"status":"preview"`) {
		t.Fatalf("HandleDeleteVoucher dry_run failed: err=%v res=%+v", err, dvDry)
	}
	if _, exists := mock.vouchers["Alpha & Co"]["103"]; !exists {
		t.Fatalf("dry_run must NOT delete voucher 103")
	}

	dvLive, err := handlers.HandleDeleteVoucher(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_delete_voucher", Arguments: map[string]any{"company": "Alpha & Co", "master_id": "103", "dry_run": false}},
	})
	if err != nil || dvLive.IsError || !strings.Contains(dvLive.Content[0].(mcp.TextContent).Text, `"status":"deleted"`) {
		t.Fatalf("HandleDeleteVoucher live failed: err=%v res=%+v", err, dvLive)
	}
	if _, exists := mock.vouchers["Alpha & Co"]["103"]; exists {
		t.Fatalf("expected voucher 103 deleted")
	}

	dlRes, err := handlers.HandleDeleteLedger(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "tally_delete_ledger", Arguments: map[string]any{"company": "Alpha & Co", "name": "Unused Ledger", "dry_run": false}},
	})
	if err != nil || dlRes.IsError || !strings.Contains(dlRes.Content[0].(mcp.TextContent).Text, `"status":"deleted"`) {
		t.Fatalf("HandleDeleteLedger on empty ledger failed: err=%v res=%+v", err, dlRes)
	}

	// 11. Verify structured audit logs were recorded for mutations & previews
	audits := client.AuditLogs()
	if len(audits) < 7 {
		t.Errorf("expected structured audit logs for all preview/live mutations, got %d", len(audits))
	}
}
