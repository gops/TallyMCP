package tally

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Standard TDL XML envelopes for Tally ERP 9 and TallyPrime.
// Tally uses XML envelopes with <ENVELOPE><HEADER>...</HEADER><BODY>...</BODY></ENVELOPE>.

const listCompaniesTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>Company</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const listLedgersTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>LedgerColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="LedgerColl" ISMODIFY="No" ISFIXED="No">
            <TYPE>Ledger</TYPE>
            {{if .Group}}<CHILDOF>{{.Group}}</CHILDOF>{{end}}
            <FETCH>Name, Parent, OpeningBalance, ClosingBalance, LedgerPhone, Email, LedgerContact, CountryName, LedgerStateName, PartyGSTIN, CreditLimit</FETCH>
          </COLLECTION>
        </TDLMESSAGE>
      </TDL>
    </DESC>
  </BODY>
</ENVELOPE>`

const getLedgerTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>SingleLedgerColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="SingleLedgerColl" ISMODIFY="No">
            <TYPE>Ledger</TYPE>
            <FILTER>LedgerFilter</FILTER>
            <FETCH>Name, Parent, OpeningBalance, ClosingBalance, LedgerPhone, Email, LedgerContact, CountryName, LedgerStateName, PartyGSTIN, CreditLimit, Address</FETCH>
          </COLLECTION>
          <SYSTEM TYPE="Formulae" NAME="LedgerFilter">$Name = "{{.LedgerName}}"</SYSTEM>
        </TDLMESSAGE>
      </TDL>
    </DESC>
  </BODY>
</ENVELOPE>`

const listStockItemsTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>StockItemColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="StockItemColl" ISMODIFY="No" ISFIXED="No">
            <TYPE>StockItem</TYPE>
            {{if .Group}}<CHILDOF>{{.Group}}</CHILDOF>{{end}}
            <FETCH>Name, Parent, BaseUnits, OpeningBalance, OpeningValue, OpeningRate, ClosingBalance, ClosingValue, ClosingRate</FETCH>
          </COLLECTION>
        </TDLMESSAGE>
      </TDL>
    </DESC>
  </BODY>
</ENVELOPE>`

const listVouchersTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>VoucherColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
        {{if .VoucherType}}<VOUCHERTYPENAME>{{.VoucherType}}</VOUCHERTYPENAME>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="VoucherColl" ISMODIFY="No" ISFIXED="No">
            <TYPE>Voucher</TYPE>
            <FETCH>Date, VoucherTypeName, VoucherNumber, Reference, PartyLedgerName, Amount, Narration, AllLedgerEntries.List.*</FETCH>
          </COLLECTION>
        </TDLMESSAGE>
      </TDL>
    </DESC>
  </BODY>
</ENVELOPE>`

const getVoucherTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>SingleVoucherColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .Date}}<SVFROMDATE>{{.Date}}</SVFROMDATE><SVTODATE>{{.Date}}</SVTODATE>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="SingleVoucherColl" ISMODIFY="No">
            <TYPE>Voucher</TYPE>
            <FILTER>VoucherFilter</FILTER>
            <FETCH>Date, EffectiveDate, VoucherTypeName, VoucherNumber, Reference, ReferenceDate, PartyLedgerName, PartyName, Amount, Narration, MasterId, Guid, AllLedgerEntries.List.*, LedgerEntries.List.*, AllLedgerEntries.BillAllocations.List.*, AllLedgerEntries.BankAllocations.List.*, AllInventoryEntries.List.*, InventoryEntries.List.*, AllInventoryEntries.BatchAllocations.List.*, AllInventoryEntries.AccountingAllocations.List.*</FETCH>
          </COLLECTION>
          <SYSTEM TYPE="Formulae" NAME="VoucherFilter">{{if .MasterID}}$MasterId = {{.MasterID}}{{else if .VoucherNumber}}$VoucherNumber = "{{.VoucherNumber}}"{{if .VoucherType}} AND $VoucherTypeName = "{{.VoucherType}}"{{end}}{{end}}</SYSTEM>
        </TDLMESSAGE>
      </TDL>
    </DESC>
  </BODY>
</ENVELOPE>`

const trialBalanceTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>Trial Balance</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const profitAndLossTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>Profit and Loss</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
        <EXPLODEFLAG>Yes</EXPLODEFLAG>
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const balanceSheetTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>Balance Sheet</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .AsOnDate}}<SVTODATE>{{.AsOnDate}}</SVTODATE>{{end}}
        <EXPLODEFLAG>Yes</EXPLODEFLAG>
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const billsReceivableTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>Bills Receivable</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
        {{if .Party}}<LEDGERNAME>{{.Party}}</LEDGERNAME>{{end}}
        <EXPLODEFLAG>Yes</EXPLODEFLAG>
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const billsPayableTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>Bills Payable</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
        {{if .Party}}<LEDGERNAME>{{.Party}}</LEDGERNAME>{{end}}
        <EXPLODEFLAG>Yes</EXPLODEFLAG>
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const dayBookTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>Day Book</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .Date}}<SVFROMDATE>{{.Date}}</SVFROMDATE><SVTODATE>{{.Date}}</SVTODATE>{{end}}
        <EXPLODEFLAG>Yes</EXPLODEFLAG>
      </STATICVARIABLES>
    </DESC>
  </BODY>
</ENVELOPE>`

const createLedgerTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Import</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>All Masters</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
      </STATICVARIABLES>
    </DESC>
    <DATA>
      <TALLYMESSAGE xmlns:UDF="TallyUDF">
        <LEDGER NAME="{{.Name}}" ACTION="Create">
          <NAME>{{.Name}}</NAME>
          <PARENT>{{.Parent}}</PARENT>
          <OPENINGBALANCE>{{.OpeningBalance}}</OPENINGBALANCE>
        </LEDGER>
      </TALLYMESSAGE>
    </DATA>
  </BODY>
</ENVELOPE>`

func executeTemplate(name, tmplStr string, data any) (string, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template %s: %w", name, err)
	}
	return buf.String(), nil
}

// BuildListCompaniesXML generates the XML for exporting all loaded companies.
func BuildListCompaniesXML() string {
	return listCompaniesTemplate
}

// BuildListLedgersXML generates the XML for exporting ledgers, optionally filtered by group.
func BuildListLedgersXML(company, group string) (string, error) {
	return executeTemplate("listLedgers", listLedgersTemplate, map[string]string{
		"Company": company,
		"Group":   group,
	})
}

// BuildGetLedgerXML generates the XML to fetch a specific ledger by name.
func BuildGetLedgerXML(company, ledgerName string) (string, error) {
	return executeTemplate("getLedger", getLedgerTemplate, map[string]string{
		"Company":    company,
		"LedgerName": ledgerName,
	})
}

// BuildListStockItemsXML generates the XML for exporting inventory stock items.
func BuildListStockItemsXML(company, group string) (string, error) {
	return executeTemplate("listStockItems", listStockItemsTemplate, map[string]string{
		"Company": company,
		"Group":   group,
	})
}

// CleanDate strips hyphens and slashes from dates so formats like YYYY-MM-DD or YYYY/MM/DD convert to YYYYMMDD required by Tally.
func CleanDate(d string) string {
	d = strings.TrimSpace(d)
	d = strings.ReplaceAll(d, "-", "")
	d = strings.ReplaceAll(d, "/", "")
	return d
}

// BuildListVouchersXML generates the XML for exporting vouchers with optional date and type filters.
func BuildListVouchersXML(company, voucherType, fromDate, toDate string) (string, error) {
	return executeTemplate("listVouchers", listVouchersTemplate, map[string]string{
		"Company":     company,
		"VoucherType": voucherType,
		"FromDate":    CleanDate(fromDate),
		"ToDate":      CleanDate(toDate),
	})
}

// BuildGetVoucherXML generates the XML for exporting a single voucher drilldown.
func BuildGetVoucherXML(company, masterID, voucherNumber, voucherType, date string) (string, error) {
	return executeTemplate("getVoucher", getVoucherTemplate, map[string]string{
		"Company":       company,
		"MasterID":      strings.TrimSpace(masterID),
		"VoucherNumber": strings.TrimSpace(voucherNumber),
		"VoucherType":   strings.TrimSpace(voucherType),
		"Date":          CleanDate(date),
	})
}

// BuildTrialBalanceXML generates the XML for exporting Trial Balance.
func BuildTrialBalanceXML(company, fromDate, toDate string) (string, error) {
	return executeTemplate("trialBalance", trialBalanceTemplate, map[string]string{
		"Company":  company,
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
	})
}

// BuildCreateLedgerXML generates the XML for creating a new ledger in Tally.
func BuildCreateLedgerXML(company, name, parent string, openingBalance float64) (string, error) {
	return executeTemplate("createLedger", createLedgerTemplate, map[string]any{
		"Company":        company,
		"Name":           name,
		"Parent":         parent,
		"OpeningBalance": openingBalance,
	})
}

// BuildProfitAndLossXML generates the XML for exporting the Profit and Loss statement.
func BuildProfitAndLossXML(company, fromDate, toDate string) (string, error) {
	return executeTemplate("profitAndLoss", profitAndLossTemplate, map[string]string{
		"Company":  company,
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
	})
}

// BuildBalanceSheetXML generates the XML for exporting the Balance Sheet statement.
func BuildBalanceSheetXML(company, asOnDate, fromDate string) (string, error) {
	return executeTemplate("balanceSheet", balanceSheetTemplate, map[string]string{
		"Company":  company,
		"AsOnDate": CleanDate(asOnDate),
		"FromDate": CleanDate(fromDate),
	})
}

// BuildBillsReceivableXML generates the XML for exporting the Bills Receivable report.
func BuildBillsReceivableXML(company, party, fromDate, toDate string) (string, error) {
	return executeTemplate("billsReceivable", billsReceivableTemplate, map[string]string{
		"Company":  company,
		"Party":    strings.TrimSpace(party),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
	})
}

// BuildBillsPayableXML generates the XML for exporting the Bills Payable report.
func BuildBillsPayableXML(company, party, fromDate, toDate string) (string, error) {
	return executeTemplate("billsPayable", billsPayableTemplate, map[string]string{
		"Company":  company,
		"Party":    strings.TrimSpace(party),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
	})
}

// BuildDayBookXML generates the XML for exporting the Day Book report.
func BuildDayBookXML(company, date string) (string, error) {
	return executeTemplate("dayBook", dayBookTemplate, map[string]string{
		"Company": company,
		"Date":    CleanDate(date),
	})
}
