package tally

import (
	"encoding/xml"
	"math"
)

// round2 rounds monetary values to 2 decimal places to prevent IEEE-754 noise in LLM payloads.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

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
	Parent         string  `xml:"PARENT" json:"parent,omitempty"`
	ParentAttr     string  `xml:"PARENT,attr" json:"-"`
	OpeningBalance float64 `xml:"OPENINGBALANCE" json:"opening_balance,omitempty"`
	ClosingBalance float64 `xml:"CLOSINGBALANCE" json:"closing_balance,omitempty"`
	Phone          string  `xml:"LEDGERPHONE,omitempty" json:"phone,omitempty"`
	Email          string  `xml:"EMAIL,omitempty" json:"email,omitempty"`
	Contact        string  `xml:"LEDGERCONTACT,omitempty" json:"contact,omitempty"`
	State          string  `xml:"LEDGERSTATENAME,omitempty" json:"state,omitempty"`
	Country        string  `xml:"COUNTRYNAME,omitempty" json:"country,omitempty"`
	GSTIN          string  `xml:"PARTYGSTIN,omitempty" json:"gstin,omitempty"`
	CreditLimit    string  `xml:"CREDITLIMIT,omitempty" json:"credit_limit,omitempty"`
}

// LedgerDetail represents a clean, flat single-ledger profile without raw Tally XML wrapper boilerplate.
type LedgerDetail struct {
	Name           string   `xml:"NAME,attr" json:"name"`
	Parent         string   `xml:"PARENT" json:"parent,omitempty"`
	ParentAttr     string   `xml:"PARENT,attr" json:"-"`
	OpeningBalance float64  `xml:"OPENINGBALANCE" json:"opening_balance,omitempty"`
	ClosingBalance float64  `xml:"CLOSINGBALANCE" json:"closing_balance,omitempty"`
	Phone          string   `xml:"LEDGERPHONE,omitempty" json:"phone,omitempty"`
	Email          string   `xml:"EMAIL,omitempty" json:"email,omitempty"`
	Contact        string   `xml:"LEDGERCONTACT,omitempty" json:"contact,omitempty"`
	State          string   `xml:"LEDGERSTATENAME,omitempty" json:"state,omitempty"`
	Country        string   `xml:"COUNTRYNAME,omitempty" json:"country,omitempty"`
	GSTIN          string   `xml:"PARTYGSTIN,omitempty" json:"gstin,omitempty"`
	CreditLimit    string   `xml:"CREDITLIMIT,omitempty" json:"credit_limit,omitempty"`
	Address        []string `xml:"ADDRESS.LIST>ADDRESS" json:"address,omitempty"`
	ResolvedFrom   string   `xml:"-" json:"resolved_from,omitempty"`
	ResolvedLedger string   `xml:"-" json:"resolved_ledger,omitempty"`
}

// PartyOverview combines a party's ledger profile, outstanding bills, and recent transactions in a single call.
type PartyOverview struct {
	Name             string               `json:"name"`
	ResolvedFrom     string               `json:"resolved_from,omitempty"`
	Parent           string               `json:"parent,omitempty"`
	ClosingBalance   float64              `json:"closing_balance"`
	BalanceType      string               `json:"balance_type"`
	Phone            string               `json:"phone,omitempty"`
	Email            string               `json:"email,omitempty"`
	GSTIN            string               `json:"gstin,omitempty"`
	State            string               `json:"state,omitempty"`
	OutstandingsCount int                 `json:"outstandings_count"`
	OutstandingBills []OutstandingBillRow `json:"outstanding_bills,omitempty"`
	RecentCount      int                  `json:"recent_count"`
	RecentVouchers   []VoucherItem        `json:"recent_vouchers,omitempty"`
}

// StockItemModel represents inventory stock in Tally.
type StockItemModel struct {
	Name           string  `xml:"NAME,attr" json:"name"`
	Parent         string  `xml:"PARENT" json:"parent"`
	ParentAttr     string  `xml:"PARENT,attr" json:"-"`
	BaseUnits      string  `xml:"BASEUNITS" json:"base_units"`
	OpeningBalance string  `xml:"OPENINGBALANCE,omitempty" json:"opening_balance,omitempty"`
	OpeningValue   float64 `xml:"OPENINGVALUE,omitempty" json:"opening_value,omitempty"`
	OpeningRate    string  `xml:"OPENINGRATE,omitempty" json:"opening_rate,omitempty"`
	ClosingBalance string  `xml:"CLOSINGBALANCE,omitempty" json:"closing_balance,omitempty"`
	ClosingValue   float64 `xml:"CLOSINGVALUE,omitempty" json:"closing_value,omitempty"`
	ClosingRate    string  `xml:"CLOSINGRATE,omitempty" json:"closing_rate,omitempty"`
}

// CompactLedgerItem provides a lightweight, token-minimized representation of a ledger.
type CompactLedgerItem struct {
	Name           string  `json:"name"`
	Parent         string  `json:"parent,omitempty"`
	ClosingBalance float64 `json:"balance"`
	State          string  `json:"state,omitempty"`
}

// CompactStockItem provides a lightweight, token-minimized representation of an inventory item.
type CompactStockItem struct {
	Name           string  `json:"name"`
	Parent         string  `json:"parent,omitempty"`
	ClosingBalance string  `json:"balance,omitempty"`
	ClosingValue   float64 `json:"value,omitempty"`
}

// PaginatedLedgers contains filtered/paginated ledgers along with paging metadata.
type PaginatedLedgers struct {
	Total      int  `json:"total"`
	Count      int  `json:"count"`
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset,omitempty"`
	Ledgers    any  `json:"ledgers"`
}

// PaginatedStockItems contains filtered/paginated stock items along with paging metadata.
type PaginatedStockItems struct {
	Total      int  `json:"total"`
	Count      int  `json:"count"`
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset,omitempty"`
	StockItems any  `json:"stock_items"`
}

// FinancialSummary provides an executive-level KPI snapshot answering standard AI prompts in < 350 tokens.
type FinancialSummary struct {
	AsOnDate        string         `json:"as_on_date"`
	Company         string         `json:"company"`
	CashAndBank     float64        `json:"cash_and_bank"`
	TotalReceivable float64        `json:"total_receivable"`
	TopDebtors      []PartyBalance `json:"top_debtors,omitempty"`
	TotalPayable    float64        `json:"total_payable"`
	TopCreditors    []PartyBalance `json:"top_creditors,omitempty"`
	RevenueYTD      float64        `json:"revenue_ytd,omitempty"`
	NetProfitYTD    float64        `json:"net_profit_ytd,omitempty"`
	WorkingCapital  float64        `json:"working_capital,omitempty"`
	Summary         string         `json:"-"` // Retained for Go callers; omitted from serialized output to avoid duplicating KPI numbers.
}

// PartyBalance represents an individual party's outstanding dues.
type PartyBalance struct {
	Name    string  `json:"name"`
	Balance float64 `json:"balance"`
}

// GroupItem represents an account group hierarchy node in Tally.
type GroupItem struct {
	Name       string `xml:"NAME,attr" json:"name"`
	Parent     string `xml:"PARENT" json:"parent"`
	ParentAttr string `xml:"PARENT,attr" json:"-"`
}

// ImportResult represents parsed Tally XML import status counts.
type ImportResult struct {
	Created    int    `xml:"CREATED" json:"created"`
	Altered    int    `xml:"ALTERED" json:"altered"`
	Deleted    int    `xml:"DELETED" json:"deleted"`
	Errors     int    `xml:"ERRORS" json:"errors"`
	Exceptions int    `xml:"EXCEPTIONS" json:"exceptions"`
	LastVchID  int    `xml:"LASTVCHID" json:"last_vch_id,omitempty"`
	LineError  string `xml:"LINEERROR" json:"line_error,omitempty"`
}

// VoucherFilterOptions defines search and filtering criteria for vouchers.
type VoucherFilterOptions struct {
	Company         string   `json:"company,omitempty"`
	VoucherType     string   `json:"voucher_type,omitempty"`
	FromDate        string   `json:"from_date,omitempty"`
	ToDate          string   `json:"to_date,omitempty"`
	Party           string   `json:"party,omitempty"`
	Ledger          string   `json:"ledger,omitempty"`
	Amount          *float64 `json:"amount,omitempty"`
	AmountTolerance *float64 `json:"amount_tolerance,omitempty"`
	MinAmount       *float64 `json:"min_amount,omitempty"`
	MaxAmount       *float64 `json:"max_amount,omitempty"`
	Search          string   `json:"search,omitempty"`
	IncludeEntries  bool     `json:"include_entries,omitempty"`
	Sort            string   `json:"sort,omitempty"` // "date_desc" (default), "date_asc", "amount_desc", "amount_asc"
	Limit           int      `json:"limit,omitempty"`
	Offset          int      `json:"offset,omitempty"`
}

// VoucherItem represents an accounting/inventory transaction in Tally.
type VoucherItem struct {
	MasterID        string              `xml:"MASTERID,omitempty" json:"master_id,omitempty"`
	VoucherTypeName string              `xml:"VOUCHERTYPENAME" json:"voucher_type,omitempty"`
	VoucherNumber   string              `xml:"VOUCHERNUMBER" json:"voucher_number,omitempty"`
	Date            string              `xml:"DATE" json:"date,omitempty"`
	EffectiveDate   string              `xml:"EFFECTIVEDATE,omitempty" json:"effective_date,omitempty"`
	Reference       string              `xml:"REFERENCE,omitempty" json:"reference,omitempty"`
	PartyLedgerName string              `xml:"PARTYLEDGERNAME,omitempty" json:"party_ledger_name,omitempty"`
	Amount          float64             `xml:"AMOUNT,omitempty" json:"amount,omitempty"`
	Narration       string              `xml:"NARRATION,omitempty" json:"narration,omitempty"`
	EntriesSummary  string              `xml:"-" json:"entries_summary,omitempty"`
	LedgerEntries   []LedgerEntryRecord `xml:"ALLLEDGERENTRIES.LIST" json:"ledger_entries,omitempty"`
}

// VoucherSummaryGroup holds aggregated counts and totals for one bucket (type, party, or month).
type VoucherSummaryGroup struct {
	Key         string  `json:"key"`
	Count       int     `json:"count"`
	TotalAmount float64 `json:"total_amount"`
}

// VoucherSummary provides a server-side aggregation of matching vouchers in minimal tokens.
type VoucherSummary struct {
	TotalCount  int                   `json:"total_count"`
	TotalAmount float64               `json:"total_amount"`
	FromDate    string                `json:"from_date,omitempty"`
	ToDate      string                `json:"to_date,omitempty"`
	GroupBy     string                `json:"group_by,omitempty"`
	Note        string                `json:"note,omitempty"`
	Groups      []VoucherSummaryGroup `json:"groups,omitempty"`
}

// PaginatedVouchers represents a page of voucher results with metadata for LLM safety.
type PaginatedVouchers struct {
	Total      int           `json:"total"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
	Count      int           `json:"count"`
	HasMore    bool          `json:"has_more"`
	NextOffset *int          `json:"next_offset,omitempty"`
	FromDate   string        `json:"from_date,omitempty"`
	ToDate     string        `json:"to_date,omitempty"`
	Note       string        `json:"note,omitempty"`
	Vouchers   []VoucherItem `json:"vouchers"`
}

// LedgerEntryRecord represents a line entry in a voucher.
type LedgerEntryRecord struct {
	LedgerName       string  `xml:"LEDGERNAME" json:"ledger_name"`
	Amount           float64 `xml:"AMOUNT" json:"amount"`
	IsDeemedPositive string  `xml:"ISDEEMEDPOSITIVE,omitempty" json:"-"`
}

// LedgerStatementEntry represents a single transaction entry in a ledger statement.
type LedgerStatementEntry struct {
	Date          string  `json:"date"`
	VoucherType   string  `json:"voucher_type,omitempty"`
	VoucherNumber string  `json:"voucher_number,omitempty"`
	ContraLedger  string  `json:"contra_ledger,omitempty"`
	Debit         float64 `json:"debit,omitempty"`
	Credit        float64 `json:"credit,omitempty"`
	Balance       float64 `json:"balance"`
	BalanceType   string  `json:"balance_type,omitempty"`
	Narration     string  `json:"narration,omitempty"`
}

// CompactStatementEntry merges debit/credit into a single signed amount (negative=Dr, positive=Cr) to save one column per row.
type CompactStatementEntry struct {
	Date          string  `json:"date"`
	VoucherType   string  `json:"voucher_type,omitempty"`
	VoucherNumber string  `json:"voucher_number,omitempty"`
	ContraLedger  string  `json:"contra_ledger,omitempty"`
	Amount        float64 `json:"amount"`
	Balance       float64 `json:"balance"`
	BalanceType   string  `json:"balance_type,omitempty"`
	Narration     string  `json:"narration,omitempty"`
}

// CompactLedgerStatementResult is the token-minimized ledger statement response.
type CompactLedgerStatementResult struct {
	LedgerName         string                  `json:"ledger_name"`
	ResolvedFrom       string                  `json:"resolved_from,omitempty"`
	Parent             string                  `json:"parent,omitempty"`
	FromDate           string                  `json:"from_date"`
	ToDate             string                  `json:"to_date"`
	OpeningBalance     float64                 `json:"opening_balance"`
	OpeningBalanceType string                  `json:"opening_balance_type"`
	TotalDebit         float64                 `json:"total_debit"`
	TotalCredit        float64                 `json:"total_credit"`
	ClosingBalance     float64                 `json:"closing_balance"`
	ClosingBalanceType string                  `json:"closing_balance_type"`
	EntriesCount       int                     `json:"entries_count"`
	TotalEntries       int                     `json:"total_entries,omitempty"`
	Limit              int                     `json:"limit,omitempty"`
	Offset             int                     `json:"offset,omitempty"`
	HasMore            bool                    `json:"has_more,omitempty"`
	NextOffset         *int                    `json:"next_offset,omitempty"`
	Entries            []CompactStatementEntry `json:"entries"`
}

// LedgerStatementResult represents the complete ledger statement response.
type LedgerStatementResult struct {
	LedgerName         string                 `json:"ledger_name"`
	ResolvedFrom       string                 `json:"resolved_from,omitempty"`
	Parent             string                 `json:"parent"`
	FromDate           string                 `json:"from_date"`
	ToDate             string                 `json:"to_date"`
	OpeningBalance     float64                `json:"opening_balance"`
	OpeningBalanceType string                 `json:"opening_balance_type"`
	TotalDebit         float64                `json:"total_debit"`
	TotalCredit        float64                `json:"total_credit"`
	ClosingBalance     float64                `json:"closing_balance"`
	ClosingBalanceType string                 `json:"closing_balance_type"`
	EntriesCount       int                    `json:"entries_count"`
	TotalEntries       int                    `json:"total_entries,omitempty"`
	Limit              int                    `json:"limit,omitempty"`
	Offset             int                    `json:"offset,omitempty"`
	HasMore            bool                   `json:"has_more,omitempty"`
	NextOffset         *int                   `json:"next_offset,omitempty"`
	Entries            []LedgerStatementEntry `json:"entries"`
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
	Guid                 string                  `xml:"GUID,omitempty" json:"-"`
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
	IsDeemedPositive   string           `xml:"ISDEEMEDPOSITIVE,omitempty" json:"-"`
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
	QueueLength    int           `json:"queue_length"`
	QueueCapacity  int           `json:"queue_capacity"`
	Error          string        `json:"error,omitempty"`
}
