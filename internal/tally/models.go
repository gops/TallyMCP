package tally

import "encoding/xml"

// TallyEnvelope represents generic XML envelope structure received from Tally.
type TallyEnvelope struct {
	XMLName xml.Name  `xml:"ENVELOPE"`
	Header  XMLHeader `xml:"HEADER"`
	Body    XMLBody   `xml:"BODY"`
}

type XMLHeader struct {
	Version      string `xml:"VERSION,omitempty"`
	TallyRequest string `xml:"TALLYREQUEST,omitempty"`
	Type         string `xml:"TYPE,omitempty"`
	ID           string `xml:"ID,omitempty"`
	Status       string `xml:"STATUS,omitempty"`
}

type XMLBody struct {
	Data        XMLData       `xml:"DATA"`
	ImportData  XMLImportData `xml:"IMPORTDATA,omitempty"`
	LineError   string        `xml:"LINEERROR,omitempty"`
	Description string        `xml:"DESC,omitempty"`
}

type XMLData struct {
	TallyMessage []TallyMessage `xml:"TALLYMESSAGE"`
	Collection   []XMLNode      `xml:",any"`
}

type XMLImportData struct {
	LinesErrors []string `xml:"LINESERROR"`
}

type XMLNode struct {
	XMLName xml.Name
	Content []byte `xml:",innerxml"`
}

// TallyMessage is a unit of data in Tally (Ledger, Company, Voucher, StockItem, etc.)
type TallyMessage struct {
	XMLName   xml.Name        `xml:"TALLYMESSAGE"`
	Company   *CompanyItem    `xml:"COMPANY"`
	Ledger    *LedgerItem     `xml:"LEDGER"`
	StockItem *StockItemModel `xml:"STOCKITEM"`
	Voucher   *VoucherItem    `xml:"VOUCHER"`
}

// CompanyItem represents a company loaded in Tally.
type CompanyItem struct {
	Name            string `xml:"NAME,attr" json:"name"`
	FormalName      string `xml:"FORMALNAME,omitempty" json:"formal_name,omitempty"`
	StartingFrom    string `xml:"STARTINGFROM,omitempty" json:"starting_from,omitempty"`
	BooksFrom       string `xml:"BOOKSFROM,omitempty" json:"books_from,omitempty"`
	State           string `xml:"STATENAME,omitempty" json:"state,omitempty"`
	Country         string `xml:"COUNTRYNAME,omitempty" json:"country,omitempty"`
	Currency        string `xml:"BASECURRENCYNAME,omitempty" json:"currency,omitempty"`
	IsActiveCompany string `xml:"ISACTIVECOMPANY,omitempty" json:"is_active,omitempty"`
}

// LedgerItem represents an account ledger in Tally.
type LedgerItem struct {
	Name           string  `xml:"NAME,attr" json:"name"`
	Parent         string  `xml:"PARENT" json:"parent"`
	OpeningBalance float64 `xml:"OPENINGBALANCE" json:"opening_balance"`
	ClosingBalance float64 `xml:"CLOSINGBALANCE" json:"closing_balance"`
	Phone          string  `xml:"LEDGERPHONE,omitempty" json:"phone,omitempty"`
	Email          string  `xml:"EMAIL,omitempty" json:"email,omitempty"`
	Contact        string  `xml:"LEDGERCONTACT,omitempty" json:"contact,omitempty"`
	State          string  `xml:"LEDGERSTATENAME,omitempty" json:"state,omitempty"`
	Country        string  `xml:"COUNTRYNAME,omitempty" json:"country,omitempty"`
	GSTIN          string  `xml:"PARTYGSTIN,omitempty" json:"gstin,omitempty"`
	CreditLimit    string  `xml:"CREDITLIMIT,omitempty" json:"credit_limit,omitempty"`
}

// StockItemModel represents inventory stock in Tally.
type StockItemModel struct {
	Name           string  `xml:"NAME,attr" json:"name"`
	Parent         string  `xml:"PARENT" json:"parent"`
	BaseUnits      string  `xml:"BASEUNITS" json:"base_units"`
	OpeningBalance string  `xml:"OPENINGBALANCE,omitempty" json:"opening_balance,omitempty"`
	OpeningValue   float64 `xml:"OPENINGVALUE,omitempty" json:"opening_value,omitempty"`
	OpeningRate    string  `xml:"OPENINGRATE,omitempty" json:"opening_rate,omitempty"`
	ClosingBalance string  `xml:"CLOSINGBALANCE,omitempty" json:"closing_balance,omitempty"`
	ClosingValue   float64 `xml:"CLOSINGVALUE,omitempty" json:"closing_value,omitempty"`
	ClosingRate    string  `xml:"CLOSINGRATE,omitempty" json:"closing_rate,omitempty"`
}

// VoucherItem represents an accounting/inventory transaction in Tally.
type VoucherItem struct {
	VoucherTypeName string              `xml:"VOUCHERTYPENAME" json:"voucher_type"`
	VoucherNumber   string              `xml:"VOUCHERNUMBER" json:"voucher_number"`
	Date            string              `xml:"DATE" json:"date"`
	EffectiveDate   string              `xml:"EFFECTIVEDATE,omitempty" json:"effective_date,omitempty"`
	Reference       string              `xml:"REFERENCE,omitempty" json:"reference,omitempty"`
	PartyLedgerName string              `xml:"PARTYLEDGERNAME,omitempty" json:"party_ledger_name,omitempty"`
	Amount          float64             `xml:"AMOUNT,omitempty" json:"amount,omitempty"`
	Narration       string              `xml:"NARRATION,omitempty" json:"narration,omitempty"`
	LedgerEntries   []LedgerEntryRecord `xml:"ALLLEDGERENTRIES.LIST" json:"ledger_entries,omitempty"`
}

// LedgerEntryRecord represents a line entry in a voucher.
type LedgerEntryRecord struct {
	LedgerName string  `xml:"LEDGERNAME" json:"ledger_name"`
	Amount     float64 `xml:"AMOUNT" json:"amount"`
	IsDeemedPositive string `xml:"ISDEEMEDPOSITIVE,omitempty" json:"is_deemed_positive,omitempty"`
}

// ServerStatus reports the status of Tally ERP connection.
type ServerStatus struct {
	Connected      bool          `json:"connected"`
	TallyURL       string        `json:"tally_url"`
	TargetCompany  string        `json:"target_company,omitempty"`
	CompaniesCount int           `json:"companies_count"`
	Companies      []CompanyItem `json:"companies,omitempty"`
	Error          string        `json:"error,omitempty"`
}
