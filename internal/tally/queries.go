package tally

import (
	"bytes"
	"fmt"
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

// BuildListVouchersXML generates the XML for exporting vouchers with optional date and type filters.
func BuildListVouchersXML(company, voucherType, fromDate, toDate string) (string, error) {
	return executeTemplate("listVouchers", listVouchersTemplate, map[string]string{
		"Company":     company,
		"VoucherType": voucherType,
		"FromDate":    fromDate,
		"ToDate":      toDate,
	})
}

// BuildTrialBalanceXML generates the XML for exporting Trial Balance.
func BuildTrialBalanceXML(company, fromDate, toDate string) (string, error) {
	return executeTemplate("trialBalance", trialBalanceTemplate, map[string]string{
		"Company":  company,
		"FromDate": fromDate,
		"ToDate":   toDate,
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
