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

// PaginatedVouchers represents a page of voucher results with metadata for LLM safety.
type PaginatedVouchers struct {
	Total      int           `json:"total"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
	Count      int           `json:"count"`
	HasMore    bool          `json:"has_more"`
	NextOffset *int          `json:"next_offset,omitempty"`
	Vouchers   []VoucherItem `json:"vouchers"`
}

// LedgerEntryRecord represents a line entry in a voucher.
type LedgerEntryRecord struct {
	LedgerName string  `xml:"LEDGERNAME" json:"ledger_name"`
	Amount     float64 `xml:"AMOUNT" json:"amount"`
	IsDeemedPositive string `xml:"ISDEEMEDPOSITIVE,omitempty" json:"is_deemed_positive,omitempty"`
}

// VoucherDetail represents a full voucher drilldown with complete accounting and inventory splits.
type VoucherDetail struct {
	MasterID             string                  `xml:"MASTERID,omitempty" json:"master_id,omitempty"`
	VoucherTypeName      string                  `xml:"VOUCHERTYPENAME" json:"voucher_type"`
	VoucherNumber        string                  `xml:"VOUCHERNUMBER" json:"voucher_number"`
	Date                 string                  `xml:"DATE" json:"date"`
	EffectiveDate        string                  `xml:"EFFECTIVEDATE,omitempty" json:"effective_date,omitempty"`
	Reference            string                  `xml:"REFERENCE,omitempty" json:"reference,omitempty"`
	ReferenceDate        string                  `xml:"REFERENCEDATE,omitempty" json:"reference_date,omitempty"`
	PartyLedgerName      string                  `xml:"PARTYLEDGERNAME,omitempty" json:"party_ledger_name,omitempty"`
	PartyName            string                  `xml:"PARTYNAME,omitempty" json:"party_name,omitempty"`
	Amount               float64                 `xml:"AMOUNT" json:"amount"`
	Narration            string                  `xml:"NARRATION,omitempty" json:"narration,omitempty"`
	Guid                 string                  `xml:"GUID,omitempty" json:"guid,omitempty"`
	AllLedgerEntries     []VoucherLedgerEntry    `xml:"ALLLEDGERENTRIES.LIST" json:"-"`
	LedgerEntriesList    []VoucherLedgerEntry    `xml:"LEDGERENTRIES.LIST" json:"-"`
	LedgerEntries        []VoucherLedgerEntry    `xml:"-" json:"ledger_entries,omitempty"`
	AllInventoryEntries  []VoucherInventoryEntry `xml:"ALLINVENTORYENTRIES.LIST" json:"-"`
	InventoryEntriesList []VoucherInventoryEntry `xml:"INVENTORYENTRIES.LIST" json:"-"`
	InventoryEntries     []VoucherInventoryEntry `xml:"-" json:"inventory_entries,omitempty"`
	Taxes                []TaxSummary            `xml:"-" json:"taxes,omitempty"`
}

// VoucherLedgerEntry represents an individual debit or credit leg of a voucher.
type VoucherLedgerEntry struct {
	LedgerName         string           `xml:"LEDGERNAME" json:"ledger_name"`
	Amount             float64          `xml:"AMOUNT" json:"amount"`
	IsDeemedPositive   string           `xml:"ISDEEMEDPOSITIVE,omitempty" json:"is_deemed_positive,omitempty"`
	BillAllocations    []BillAllocation `xml:"BILLALLOCATIONS.LIST" json:"bill_allocations,omitempty"`
	BillAllocationsAlt []BillAllocation `xml:"BILLALLOCATIONS" json:"-"`
	BankAllocations    []BankAllocation `xml:"BANKALLOCATIONS.LIST" json:"bank_allocations,omitempty"`
	BankAllocationsAlt []BankAllocation `xml:"BANKALLOCATIONS" json:"-"`
}

// BillAllocation represents invoice reference tracking (New Ref, Agst Ref, Advance, On Account).
type BillAllocation struct {
	Name     string  `xml:"NAME" json:"name"`
	BillType string  `xml:"BILLTYPE" json:"bill_type"`
	Amount   float64 `xml:"AMOUNT" json:"amount"`
}

// BankAllocation represents banking/cheque/e-transfer payment particulars.
type BankAllocation struct {
	PaymentFavouring string  `xml:"PAYMENTFAVOURING,omitempty" json:"payment_favouring,omitempty"`
	TransactionType  string  `xml:"TRANSACTIONTYPE,omitempty" json:"transaction_type,omitempty"`
	InstrumentNumber string  `xml:"INSTRUMENTNUMBER,omitempty" json:"instrument_number,omitempty"`
	InstrumentDate   string  `xml:"INSTRUMENTDATE,omitempty" json:"instrument_date,omitempty"`
	Amount           float64 `xml:"AMOUNT,omitempty" json:"amount,omitempty"`
}

// VoucherInventoryEntry represents a stock line item in a sales or purchase invoice.
type VoucherInventoryEntry struct {
	StockItemName         string                `xml:"STOCKITEMNAME" json:"stock_item_name"`
	Rate                  string                `xml:"RATE,omitempty" json:"rate,omitempty"`
	BilledQty             string                `xml:"BILLEDQTY,omitempty" json:"billed_qty,omitempty"`
	ActualQty             string                `xml:"ACTUALQTY,omitempty" json:"actual_qty,omitempty"`
	Amount                float64               `xml:"AMOUNT" json:"amount"`
	Discount              string                `xml:"DISCOUNT,omitempty" json:"discount,omitempty"`
	BatchAllocations      []BatchAllocation     `xml:"BATCHALLOCATIONS.LIST" json:"batch_allocations,omitempty"`
	BatchAllocationsAlt   []BatchAllocation     `xml:"BATCHALLOCATIONS" json:"-"`
	AccountingAllocations []VoucherLedgerEntry `xml:"ACCOUNTINGALLOCATIONS.LIST" json:"accounting_allocations,omitempty"`
}

// BatchAllocation represents warehouse godown and batch allocations for inventory.
type BatchAllocation struct {
	GodownName string  `xml:"GODOWNNAME,omitempty" json:"godown_name,omitempty"`
	BatchName  string  `xml:"BATCHNAME,omitempty" json:"batch_name,omitempty"`
	Amount     float64 `xml:"AMOUNT,omitempty" json:"amount,omitempty"`
	ActualQty  string  `xml:"ACTUALQTY,omitempty" json:"actual_qty,omitempty"`
	BilledQty  string  `xml:"BILLEDQTY,omitempty" json:"billed_qty,omitempty"`
}

// TaxSummary summarizes GST and duties extracted from voucher legs.
type TaxSummary struct {
	LedgerName string  `json:"ledger_name"`
	Amount     float64 `json:"amount"`
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
