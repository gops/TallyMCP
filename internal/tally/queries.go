package tally

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"
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
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
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

const listGroupsTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>GroupColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="GroupColl" ISMODIFY="No" ISFIXED="No">
            <TYPE>Group</TYPE>
            <FETCH>Name, Parent</FETCH>
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
            {{if .IncludeEntries}}<FETCH>Date, VoucherTypeName, VoucherNumber, Reference, PartyLedgerName, Amount, Narration, MasterId, AllLedgerEntries.*</FETCH>{{else}}<FETCH>Date, VoucherTypeName, VoucherNumber, Reference, PartyLedgerName, Amount, Narration, MasterId</FETCH>{{end}}
          </COLLECTION>
        </TDLMESSAGE>
      </TDL>
    </DESC>
  </BODY>
</ENVELOPE>`

const ledgerVouchersTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Collection</TYPE>
    <ID>LedgerStatementColl</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:XML</SVEXPORTFORMAT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
      </STATICVARIABLES>
      <TDL>
        <TDLMESSAGE>
          <COLLECTION NAME="LedgerStatementColl">
            <TYPE>Voucher</TYPE>
            <FILTER>LedgerMatchFilter</FILTER>
            <FETCH>Date, VoucherTypeName, VoucherNumber, Reference, PartyLedgerName, Narration, MasterId, AllLedgerEntries.*</FETCH>
          </COLLECTION>
          <SYSTEM TYPE="Formulae" NAME="LedgerMatchFilter">
            $LedgerEntries[1, @@IsLedgerMatch].LedgerName = "{{.LedgerName}}" OR $AllLedgerEntries[1, @@IsLedgerMatch].LedgerName = "{{.LedgerName}}"
          </SYSTEM>
          <SYSTEM TYPE="Formulae" NAME="IsLedgerMatch">
            $LedgerName = "{{.LedgerName}}"
          </SYSTEM>
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
          <SYSTEM TYPE="Formulae" NAME="VoucherFilter">{{.VoucherFilterFormula}}</SYSTEM>
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
        <EXPLODEFLAG>{{if .Explode}}Yes{{else}}No{{end}}</EXPLODEFLAG>
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
        <EXPLODEFLAG>{{if .Explode}}Yes{{else}}No{{end}}</EXPLODEFLAG>
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
        <EXPLODEFLAG>{{if .Explode}}Yes{{else}}No{{end}}</EXPLODEFLAG>
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
        <EXPLODEFLAG>{{if .Explode}}Yes{{else}}No{{end}}</EXPLODEFLAG>
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
        <EXPLODEFLAG>{{if .Explode}}Yes{{else}}No{{end}}</EXPLODEFLAG>
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

const exportPDFTemplate = `<ENVELOPE>
  <HEADER>
    <VERSION>1</VERSION>
    <TALLYREQUEST>Export</TALLYREQUEST>
    <TYPE>Data</TYPE>
    <ID>{{.ReportID}}</ID>
  </HEADER>
  <BODY>
    <DESC>
      <STATICVARIABLES>
        <SVEXPORTFORMAT>$$SysName:PDF</SVEXPORTFORMAT>
        <SVEXPORTLOCATION>{{.ExportLocation}}</SVEXPORTLOCATION>
        <SVPRINTFILENAME>{{.PrintFileName}}</SVPRINTFILENAME>
        <SVOPENFILEAFTEREXPORT>No</SVOPENFILEAFTEREXPORT>
        {{if .Company}}<SVCURRENTCOMPANY>{{.Company}}</SVCURRENTCOMPANY>{{end}}
        {{if .FromDate}}<SVFROMDATE>{{.FromDate}}</SVFROMDATE>{{end}}
        {{if .ToDate}}<SVTODATE>{{.ToDate}}</SVTODATE>{{end}}
        {{if .Date}}<SVFROMDATE>{{.Date}}</SVFROMDATE><SVTODATE>{{.Date}}</SVTODATE>{{end}}
        {{if .Party}}<LEDGERNAME>{{.Party}}</LEDGERNAME>{{end}}
        {{if .MasterID}}<MASTERID>{{.MasterID}}</MASTERID>{{end}}
        {{if .VoucherNo}}<VOUCHERNUMBER>{{.VoucherNo}}</VOUCHERNUMBER>{{end}}
        {{if .VoucherType}}<VOUCHERTYPENAME>{{.VoucherType}}</VOUCHERTYPENAME>{{end}}
        <EXPLODEFLAG>Yes</EXPLODEFLAG>
      </STATICVARIABLES>
    </DESC>
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

// isDigitsOnly checks if a string contains exclusively ASCII digits.
func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isUUID checks if a string matches UUID format (32 hex characters, with or without hyphens).
func isUUID(s string) bool {
	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 32 {
		return false
	}
	for _, r := range clean {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// BuildListCompaniesXML generates the XML for exporting all loaded companies.
func BuildListCompaniesXML() string {
	return listCompaniesTemplate
}

// BuildListLedgersXML generates the XML for exporting ledgers, optionally filtered by group.
func BuildListLedgersXML(company, group string) (string, error) {
	return BuildListLedgersXMLWithDates(company, group, "", "")
}

// BuildListLedgersXMLWithDates generates the XML for exporting ledgers with optional group and date scope.
func BuildListLedgersXMLWithDates(company, group, fromDate, toDate string) (string, error) {
	return executeTemplate("listLedgers", listLedgersTemplate, map[string]string{
		"Company":  XMLEscapeText(company),
		"Group":    XMLEscapeText(group),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
	})
}

// BuildListGroupsXML generates the XML for exporting account group hierarchy.
func BuildListGroupsXML(company string) (string, error) {
	return executeTemplate("listGroups", listGroupsTemplate, map[string]string{
		"Company": XMLEscapeText(company),
	})
}

// BuildGetLedgerXML generates the XML to fetch a specific ledger by name safely.
// Strips double quotes and control characters to prevent TDL syntax corruption.
func BuildGetLedgerXML(company, ledgerName string) (string, error) {
	cleanName := SanitizeTDLString(ledgerName)
	if cleanName == "" {
		return "", fmt.Errorf("ledger name must not be empty")
	}
	return executeTemplate("getLedger", getLedgerTemplate, map[string]string{
		"Company":    XMLEscapeText(company),
		"LedgerName": XMLEscapeText(cleanName),
	})
}

// BuildListStockItemsXML generates the XML for exporting inventory stock items.
func BuildListStockItemsXML(company, group string) (string, error) {
	return executeTemplate("listStockItems", listStockItemsTemplate, map[string]string{
		"Company": XMLEscapeText(company),
		"Group":   XMLEscapeText(group),
	})
}

// CleanDate normalizes common agent date formats (YYYYMMDD, YYYY-MM-DD, DD-MM-YYYY, DD/MM/YYYY,
// RFC3339 timestamps, "02 Jan 2006", or relative keywords like "today", "yesterday", "this_fy")
// into the YYYYMMDD format required by Tally.
func CleanDate(d string) string {
	return CleanDateWithNow(d, time.Now())
}

// CleanDateWithNow normalizes dates and relative date keywords against a reference time.
func CleanDateWithNow(d string, now time.Time) string {
	s := strings.TrimSpace(d)
	if s == "" {
		return ""
	}
	if len(s) == 8 && isDigitsOnly(s) {
		return s
	}

	switch strings.ToLower(strings.ReplaceAll(s, " ", "_")) {
	case "today":
		return now.Format("20060102")
	case "yesterday":
		return now.AddDate(0, 0, -1).Format("20060102")
	case "this_month", "month_start":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("20060102")
	case "month_end":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return first.AddDate(0, 1, -1).Format("20060102")
	case "last_month":
		firstLast := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0)
		return firstLast.Format("20060102")
	case "this_fy", "fy_start":
		return CurrentFinancialYearStart(now)
	case "fy_end":
		return CurrentFinancialYearEnd(now)
	case "last_fy":
		prev := now.AddDate(-1, 0, 0)
		return CurrentFinancialYearStart(prev)
	}

	// Handle ISO-8601 timestamps like 2026-10-01T00:00:00Z
	if idx := strings.IndexByte(s, 'T'); idx == 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t.Format("20060102")
		}
	}

	layouts := []string{
		"2006-01-02",
		"2006/01/02",
		"02-01-2006",
		"02/01/2006",
		"2-1-2006",
		"2/1/2006",
		"02 Jan 2006",
		"2 Jan 2006",
		"02-Jan-2006",
		"2-Jan-2006",
		"Jan 02, 2006",
		"Jan 2, 2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("20060102")
		}
	}

	// Fallback: strip hyphens and slashes
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "/", "")
	return s
}

// ResolveDateRange expands relative period keywords (e.g. "this_month", "last_month", "this_fy", "last_fy", "today", "yesterday", "last_7_days", "last_30_days")
// into explicit YYYYMMDD fromDate and toDate pairs.
func ResolveDateRange(fromDate, toDate string, now time.Time) (string, string) {
	fRaw := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fromDate), " ", "_"))
	tRaw := strings.TrimSpace(toDate)

	if tRaw == "" {
		switch fRaw {
		case "today":
			d := now.Format("20060102")
			return d, d
		case "yesterday":
			d := now.AddDate(0, 0, -1).Format("20060102")
			return d, d
		case "this_month", "mtd":
			start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("20060102")
			return start, now.Format("20060102")
		case "last_month":
			firstThis := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			firstLast := firstThis.AddDate(0, -1, 0)
			lastDayLast := firstThis.AddDate(0, 0, -1)
			return firstLast.Format("20060102"), lastDayLast.Format("20060102")
		case "this_fy", "ytd":
			return CurrentFinancialYearStart(now), CurrentFinancialYearEnd(now)
		case "last_fy":
			prev := now.AddDate(-1, 0, 0)
			return CurrentFinancialYearStart(prev), CurrentFinancialYearEnd(prev)
		case "last_7_days":
			return now.AddDate(0, 0, -6).Format("20060102"), now.Format("20060102")
		case "last_30_days":
			return now.AddDate(0, 0, -29).Format("20060102"), now.Format("20060102")
		}
	}
	return CleanDateWithNow(fromDate, now), CleanDateWithNow(toDate, now)
}

// CurrentFinancialYearStart returns the YYYYMMDD start date (April 1st) of the Indian Financial Year for the given time.
// In India, the financial year runs from April 1 to March 31.
func CurrentFinancialYearStart(t time.Time) string {
	year := t.Year()
	if t.Month() < time.April {
		year--
	}
	return fmt.Sprintf("%04d0401", year)
}

// CurrentFinancialYearEnd returns the YYYYMMDD end date (March 31st) of the Indian Financial Year for the given time.
func CurrentFinancialYearEnd(t time.Time) string {
	year := t.Year()
	if t.Month() >= time.April {
		year++
	}
	return fmt.Sprintf("%04d0331", year)
}

// BuildLedgerStatementXML generates the XML for exporting vouchers for a specific ledger statement.
func BuildLedgerStatementXML(company, ledgerName, fromDate, toDate string) (string, error) {
	cleanName := SanitizeTDLString(ledgerName)
	if cleanName == "" {
		return "", fmt.Errorf("ledger name must not be empty")
	}
	data := map[string]any{
		"Company":    XMLEscapeText(company),
		"LedgerName": XMLEscapeText(cleanName),
		"FromDate":   CleanDate(fromDate),
		"ToDate":     CleanDate(toDate),
	}
	return executeTemplate("ledgerVouchers", ledgerVouchersTemplate, data)
}

// BuildListVouchersXML generates the XML for exporting vouchers with optional date and type filters.
func BuildListVouchersXML(company, voucherType, fromDate, toDate string) (string, error) {
	return BuildFilteredListVouchersXML(company, VoucherFilterOptions{
		VoucherType: voucherType,
		FromDate:    fromDate,
		ToDate:      toDate,
	})
}

// BuildFilteredListVouchersXML generates the XML for exporting vouchers scoped safely by date range and type.
// Multi-attribute searches (party, amount tolerance, narration, reference) are processed cleanly in Go memory
// to prevent brittle TDL formula compilation errors ("Bad formula") from crashing Tally.
// AllLedgerEntries.* is only fetched when filtering by a specific ledger leg or explicitly requested,
// keeping default list exports fast and TOON-tabular.
func BuildFilteredListVouchersXML(company string, opts VoucherFilterOptions) (string, error) {
	includeEntries := opts.IncludeEntries || strings.TrimSpace(opts.Ledger) != ""
	data := map[string]any{
		"Company":        XMLEscapeText(company),
		"VoucherType":    XMLEscapeText(opts.VoucherType),
		"FromDate":       CleanDate(opts.FromDate),
		"ToDate":         CleanDate(opts.ToDate),
		"IncludeEntries": includeEntries,
	}

	return executeTemplate("listVouchers", listVouchersTemplate, data)
}

// BuildGetVoucherXML generates the XML for exporting a single voucher drilldown safely.
// Validates types and strictly constructs TDL formulas to eliminate "Bad formula" crashes.
func BuildGetVoucherXML(company, masterID, voucherNumber, voucherType, date string) (string, error) {
	cleanMasterID := strings.TrimSpace(masterID)
	cleanVoucherNumber := SanitizeTDLString(voucherNumber)
	cleanVoucherType := SanitizeTDLString(voucherType)

	var formula string
	if cleanMasterID != "" {
		// Strict validation: MasterID in Tally is numeric. Passing non-digits into $MasterId = ...
		// causes Tally's C++ core to fail compilation and terminate with "Can not understand, bad formula".
		if isDigitsOnly(cleanMasterID) {
			formula = fmt.Sprintf("$MasterId = %s", cleanMasterID)
		} else if isUUID(cleanMasterID) {
			formula = fmt.Sprintf("$Guid = %q", cleanMasterID)
		} else {
			return "", fmt.Errorf("invalid master_id %q: must contain only digits or valid GUID to prevent Tally 'Bad formula' crashes", masterID)
		}
	} else if cleanVoucherNumber != "" {
		formula = fmt.Sprintf("$VoucherNumber = %q", cleanVoucherNumber)
		if cleanVoucherType != "" {
			formula += fmt.Sprintf(" AND $VoucherTypeName = %q", cleanVoucherType)
		}
	} else {
		return "", fmt.Errorf("either master_id or voucher_number must be provided to fetch a voucher")
	}

	return executeTemplate("getVoucher", getVoucherTemplate, map[string]string{
		"Company":              XMLEscapeText(company),
		"Date":                 CleanDate(date),
		"VoucherFilterFormula": XMLEscapeText(formula),
	})
}

// BuildTrialBalanceXML generates the XML for exporting Trial Balance.
func BuildTrialBalanceXML(company, fromDate, toDate string) (string, error) {
	return executeTemplate("trialBalance", trialBalanceTemplate, map[string]string{
		"Company":  XMLEscapeText(company),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
	})
}

// BuildCreateLedgerXML generates the XML for creating a new ledger in Tally.
func BuildCreateLedgerXML(company, name, parent string, openingBalance float64) (string, error) {
	cleanName := SanitizeTDLString(name)
	if cleanName == "" {
		return "", fmt.Errorf("ledger name cannot be empty")
	}
	cleanParent := SanitizeTDLString(parent)
	if cleanParent == "" {
		return "", fmt.Errorf("parent group cannot be empty")
	}
	return executeTemplate("createLedger", createLedgerTemplate, map[string]any{
		"Company":        XMLEscapeText(company),
		"Name":           XMLEscapeAttr(cleanName),
		"Parent":         XMLEscapeText(cleanParent),
		"OpeningBalance": openingBalance,
	})
}

// BuildProfitAndLossXML generates the XML for exporting the Profit and Loss statement.
func BuildProfitAndLossXML(company, fromDate, toDate string) (string, error) {
	return BuildProfitAndLossXMLWithOptions(company, fromDate, toDate, true)
}

// BuildProfitAndLossXMLWithOptions generates Profit & Loss XML with configurable explosion.
func BuildProfitAndLossXMLWithOptions(company, fromDate, toDate string, explode bool) (string, error) {
	return executeTemplate("profitAndLoss", profitAndLossTemplate, map[string]any{
		"Company":  XMLEscapeText(company),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
		"Explode":  explode,
	})
}

// BuildBalanceSheetXML generates the XML for exporting the Balance Sheet statement.
func BuildBalanceSheetXML(company, asOnDate, fromDate string) (string, error) {
	return BuildBalanceSheetXMLWithOptions(company, asOnDate, fromDate, true)
}

// BuildBalanceSheetXMLWithOptions generates Balance Sheet XML with configurable explosion.
func BuildBalanceSheetXMLWithOptions(company, asOnDate, fromDate string, explode bool) (string, error) {
	return executeTemplate("balanceSheet", balanceSheetTemplate, map[string]any{
		"Company":  XMLEscapeText(company),
		"AsOnDate": CleanDate(asOnDate),
		"FromDate": CleanDate(fromDate),
		"Explode":  explode,
	})
}

// BuildBillsReceivableXML generates the XML for exporting the Bills Receivable report.
func BuildBillsReceivableXML(company, party, fromDate, toDate string) (string, error) {
	return BuildBillsReceivableXMLWithOptions(company, party, fromDate, toDate, true)
}

// BuildBillsReceivableXMLWithOptions generates Bills Receivable XML with configurable explosion.
func BuildBillsReceivableXMLWithOptions(company, party, fromDate, toDate string, explode bool) (string, error) {
	return executeTemplate("billsReceivable", billsReceivableTemplate, map[string]any{
		"Company":  XMLEscapeText(company),
		"Party":    XMLEscapeText(SanitizeTDLString(party)),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
		"Explode":  explode,
	})
}

// BuildBillsPayableXML generates the XML for exporting the Bills Payable report.
func BuildBillsPayableXML(company, party, fromDate, toDate string) (string, error) {
	return BuildBillsPayableXMLWithOptions(company, party, fromDate, toDate, true)
}

// BuildBillsPayableXMLWithOptions generates Bills Payable XML with configurable explosion.
func BuildBillsPayableXMLWithOptions(company, party, fromDate, toDate string, explode bool) (string, error) {
	return executeTemplate("billsPayable", billsPayableTemplate, map[string]any{
		"Company":  XMLEscapeText(company),
		"Party":    XMLEscapeText(SanitizeTDLString(party)),
		"FromDate": CleanDate(fromDate),
		"ToDate":   CleanDate(toDate),
		"Explode":  explode,
	})
}

// BuildDayBookXML generates the XML for exporting the Day Book report.
func BuildDayBookXML(company, date string) (string, error) {
	return BuildDayBookXMLWithOptions(company, date, true)
}

// BuildDayBookXMLWithOptions generates Day Book XML with configurable explosion.
func BuildDayBookXMLWithOptions(company, date string, explode bool) (string, error) {
	return executeTemplate("dayBook", dayBookTemplate, map[string]any{
		"Company": XMLEscapeText(company),
		"Date":    CleanDate(date),
		"Explode": explode,
	})
}

// NormalizeReportID standardizes user/agent report names into Tally's expected ID.
func NormalizeReportID(reportType string) string {
	lower := strings.ToLower(strings.TrimSpace(reportType))
	switch lower {
	case "balance sheet", "balancesheet", "bs":
		return "Balance Sheet"
	case "profit and loss", "profit & loss", "p&l", "pl", "profitandloss":
		return "Profit and Loss"
	case "day book", "daybook", "db":
		return "Day Book"
	case "trial balance", "trialbalance", "tb":
		return "Trial Balance"
	case "bills receivable", "receivable", "receivables", "outstanding receivables", "outstanding_receivables":
		return "Bills Receivable"
	case "bills payable", "payable", "payables", "outstanding payables", "outstanding_payables":
		return "Bills Payable"
	case "stock summary", "stocksummary", "inventory":
		return "Stock Summary"
	case "voucher", "invoice", "voucher printing", "sales invoice", "bill":
		return "Voucher"
	default:
		if strings.TrimSpace(reportType) != "" {
			return strings.TrimSpace(reportType)
		}
		return "Balance Sheet"
	}
}

// BuildExportPDFXML generates the XML telling Tally to export a report or voucher directly to a PDF file on disk.
func BuildExportPDFXML(company, reportID, exportLocation, printFileName, fromDate, toDate, date, party, voucherNo, voucherType string) (string, error) {
	return BuildExportPDFXMLWithMasterID(company, reportID, exportLocation, printFileName, fromDate, toDate, date, party, voucherNo, voucherType, "")
}

// BuildExportPDFXMLWithMasterID generates the PDF export XML with optional MasterID support.
func BuildExportPDFXMLWithMasterID(company, reportID, exportLocation, printFileName, fromDate, toDate, date, party, voucherNo, voucherType, masterID string) (string, error) {
	cleanMasterID := strings.TrimSpace(masterID)
	if cleanMasterID != "" && !isDigitsOnly(cleanMasterID) {
		cleanMasterID = ""
	}
	return executeTemplate("exportPDF", exportPDFTemplate, map[string]string{
		"Company":        XMLEscapeText(company),
		"ReportID":       NormalizeReportID(reportID),
		"ExportLocation": XMLEscapeText(strings.TrimSpace(exportLocation)),
		"PrintFileName":  XMLEscapeText(strings.TrimSpace(printFileName)),
		"FromDate":       CleanDate(fromDate),
		"ToDate":         CleanDate(toDate),
		"Date":           CleanDate(date),
		"Party":          XMLEscapeText(SanitizeTDLString(party)),
		"MasterID":       cleanMasterID,
		"VoucherNo":      XMLEscapeText(SanitizeTDLString(voucherNo)),
		"VoucherType":    XMLEscapeText(SanitizeTDLString(voucherType)),
	})
}
