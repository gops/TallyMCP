package tally

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ReportLineItem represents a flattened row extracted from Tally native financial reports
// (Balance Sheet, Profit & Loss, Trial Balance).
type ReportLineItem struct {
	Name   string  `json:"name"`
	Amount float64 `json:"amount,omitempty"`
	Debit  float64 `json:"debit,omitempty"`
	Credit float64 `json:"credit,omitempty"`
}

// OutstandingBillRow represents a flattened bill row from Bills Receivable / Bills Payable.
type OutstandingBillRow struct {
	BillDate      string  `json:"bill_date,omitempty"`
	BillRef       string  `json:"bill_ref,omitempty"`
	Party         string  `json:"party,omitempty"`
	PendingAmount float64 `json:"pending_amount,omitempty"`
	BalanceType   string  `json:"balance_type,omitempty"`
	DueDate       string  `json:"due_date,omitempty"`
	OverdueDays   int     `json:"overdue_days,omitempty"`
}

// DayBookEntryRow represents a flattened voucher row from Day Book.
type DayBookEntryRow struct {
	Date        string  `json:"date,omitempty"`
	Particulars string  `json:"particulars,omitempty"`
	VoucherType string  `json:"voucher_type,omitempty"`
	Debit       float64 `json:"debit,omitempty"`
	Credit      float64 `json:"credit,omitempty"`
}

// ReportQueryOptions controls pagination, filtering, and sorting on normalized reports.
type ReportQueryOptions struct {
	Limit       int
	Offset      int
	IncludeZero bool
	MinAmount   float64
	OverdueOnly bool
	Search      string
	VoucherType string
	Sort        string
}

// NormalizeReportForAgent flattens Tally's verbose display-XML trees into compact tabular payloads for TOON.
func NormalizeReportForAgent(reportKind string, raw map[string]any) any {
	return NormalizeReportWithOptions(reportKind, raw, ReportQueryOptions{})
}

// NormalizeReportWithOptions flattens, filters, strips zero rows, and paginates native Tally reports.
func NormalizeReportWithOptions(reportKind string, raw map[string]any, opts ReportQueryOptions) any {
	if len(raw) == 0 {
		return raw
	}

	env, ok := raw["ENVELOPE"].(map[string]any)
	if !ok {
		env = raw
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	searchLower := strings.ToLower(strings.TrimSpace(opts.Search))
	vTypeLower := strings.ToLower(strings.TrimSpace(opts.VoucherType))

	switch strings.ToLower(strings.TrimSpace(reportKind)) {
	case "bills_receivable", "bills_payable", "receivable", "payable":
		if rows := extractOutstandingRows(env); len(rows) > 0 {
			filtered := make([]OutstandingBillRow, 0, len(rows))
			for _, r := range rows {
				if !opts.IncludeZero && r.PendingAmount == 0 {
					continue
				}
				if opts.OverdueOnly && r.OverdueDays <= 0 {
					continue
				}
				if opts.MinAmount > 0 && math.Abs(r.PendingAmount) < opts.MinAmount {
					continue
				}
				if searchLower != "" &&
					!strings.Contains(strings.ToLower(r.Party), searchLower) &&
					!strings.Contains(strings.ToLower(r.BillRef), searchLower) {
					continue
				}
				filtered = append(filtered, r)
			}
			switch strings.ToLower(strings.TrimSpace(opts.Sort)) {
			case "amount_desc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return math.Abs(filtered[i].PendingAmount) > math.Abs(filtered[j].PendingAmount)
				})
			case "amount_asc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return math.Abs(filtered[i].PendingAmount) < math.Abs(filtered[j].PendingAmount)
				})
			case "overdue_desc":
				sort.SliceStable(filtered, func(i, j int) bool {
					if filtered[i].OverdueDays != filtered[j].OverdueDays {
						return filtered[i].OverdueDays > filtered[j].OverdueDays
					}
					return math.Abs(filtered[i].PendingAmount) > math.Abs(filtered[j].PendingAmount)
				})
			case "date_asc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return filtered[i].BillDate < filtered[j].BillDate
				})
			case "date_desc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return filtered[i].BillDate > filtered[j].BillDate
				})
			}
			paged, hasMore, nextOffset := paginateSlice(filtered, limit, offset)
			res := map[string]any{
				"report":   reportKind,
				"total":    len(filtered),
				"count":    len(paged),
				"offset":   offset,
				"limit":    limit,
				"has_more": hasMore,
				"bills":    paged,
			}
			if nextOffset != nil {
				res["next_offset"] = *nextOffset
			}
			return res
		}
	case "day_book":
		if rows := extractDayBookRows(env); len(rows) > 0 {
			filtered := make([]DayBookEntryRow, 0, len(rows))
			for _, r := range rows {
				if !opts.IncludeZero && r.Debit == 0 && r.Credit == 0 {
					continue
				}
				if vTypeLower != "" && !strings.Contains(strings.ToLower(r.VoucherType), vTypeLower) {
					continue
				}
				if opts.MinAmount > 0 && math.Max(math.Abs(r.Debit), math.Abs(r.Credit)) < opts.MinAmount {
					continue
				}
				if searchLower != "" &&
					!strings.Contains(strings.ToLower(r.Particulars), searchLower) &&
					!strings.Contains(strings.ToLower(r.VoucherType), searchLower) {
					continue
				}
				filtered = append(filtered, r)
			}
			switch strings.ToLower(strings.TrimSpace(opts.Sort)) {
			case "amount_desc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return math.Max(math.Abs(filtered[i].Debit), math.Abs(filtered[i].Credit)) > math.Max(math.Abs(filtered[j].Debit), math.Abs(filtered[j].Credit))
				})
			case "date_asc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return filtered[i].Date < filtered[j].Date
				})
			case "date_desc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return filtered[i].Date > filtered[j].Date
				})
			}
			paged, hasMore, nextOffset := paginateSlice(filtered, limit, offset)
			res := map[string]any{
				"report":   "day_book",
				"total":    len(filtered),
				"count":    len(paged),
				"offset":   offset,
				"limit":    limit,
				"has_more": hasMore,
				"entries":  paged,
			}
			if nextOffset != nil {
				res["next_offset"] = *nextOffset
			}
			return res
		}
	default:
		if rows := extractStatementRows(env); len(rows) > 0 {
			filtered := make([]ReportLineItem, 0, len(rows))
			for _, r := range rows {
				if !opts.IncludeZero && r.Amount == 0 && r.Debit == 0 && r.Credit == 0 {
					continue
				}
				if opts.MinAmount > 0 && math.Max(math.Abs(r.Amount), math.Max(math.Abs(r.Debit), math.Abs(r.Credit))) < opts.MinAmount {
					continue
				}
				if searchLower != "" && !strings.Contains(strings.ToLower(r.Name), searchLower) {
					continue
				}
				filtered = append(filtered, r)
			}
			switch strings.ToLower(strings.TrimSpace(opts.Sort)) {
			case "amount_desc":
				sort.SliceStable(filtered, func(i, j int) bool {
					ai := math.Max(math.Abs(filtered[i].Amount), math.Max(math.Abs(filtered[i].Debit), math.Abs(filtered[i].Credit)))
					aj := math.Max(math.Abs(filtered[j].Amount), math.Max(math.Abs(filtered[j].Debit), math.Abs(filtered[j].Credit)))
					return ai > aj
				})
			case "amount_asc":
				sort.SliceStable(filtered, func(i, j int) bool {
					ai := math.Max(math.Abs(filtered[i].Amount), math.Max(math.Abs(filtered[i].Debit), math.Abs(filtered[i].Credit)))
					aj := math.Max(math.Abs(filtered[j].Amount), math.Max(math.Abs(filtered[j].Debit), math.Abs(filtered[j].Credit)))
					return ai < aj
				})
			case "name_asc":
				sort.SliceStable(filtered, func(i, j int) bool {
					return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
				})
			}
			paged, hasMore, nextOffset := paginateSlice(filtered, limit, offset)
			res := map[string]any{
				"report":   reportKind,
				"total":    len(filtered),
				"count":    len(paged),
				"offset":   offset,
				"limit":    limit,
				"has_more": hasMore,
				"rows":     paged,
			}
			if nextOffset != nil {
				res["next_offset"] = *nextOffset
			}
			return res
		}
	}

	// Fallback: strip ENVELOPE/HEADER wrapper boilerplate if DATA exists so raw XML headers don't waste tokens.
	if body, ok := env["BODY"].(map[string]any); ok {
		if data, ok := body["DATA"]; ok {
			return map[string]any{
				"report": reportKind,
				"data":   data,
			}
		}
	}
	return raw
}

func paginateSlice[T any](items []T, limit, offset int) ([]T, bool, *int) {
	total := len(items)
	if offset >= total {
		return []T{}, false, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	var nextOffset *int
	hasMore := end < total
	if hasMore {
		n := end
		nextOffset = &n
	}
	return items[offset:end], hasMore, nextOffset
}

func asSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return []any{v}
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		if txt, ok := t["#text"]; ok {
			return asString(txt)
		}
		for _, val := range t {
			if s := asString(val); s != "" {
				return s
			}
		}
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func parseReportFloat(v any) float64 {
	amt, _ := parseReportAmountAndType(v)
	return amt
}

func parseReportAmountAndType(v any) (float64, string) {
	s := asString(v)
	if s == "" {
		return 0, ""
	}
	var balType string
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, " dr") || lower == "dr" {
		balType = "Dr"
		s = strings.TrimSpace(s[:len(s)-2])
	} else if strings.HasSuffix(lower, " cr") || lower == "cr" {
		balType = "Cr"
		s = strings.TrimSpace(s[:len(s)-2])
	}
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, ""
	}
	return round2(f), balType
}

func parseOverdueDays(v any) int {
	s := asString(v)
	if s == "" {
		return 0
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return 0
	}
	return n
}

func extractStatementRows(node map[string]any) []ReportLineItem {
	var rows []ReportLineItem

	// Pattern 1: DSPACCNAME + DSPACCINFO parallel lists (Trial Balance / P&L / Group Summary)
	if names := asSlice(node["DSPACCNAME"]); len(names) > 0 {
		infos := asSlice(node["DSPACCINFO"])
		for i, n := range names {
			nm, _ := n.(map[string]any)
			label := asString(nm["DSPDISPNAME"])
			if label == "" {
				label = asString(n)
			}
			if label == "" {
				continue
			}
			item := ReportLineItem{Name: label}
			if i < len(infos) {
				if inf, ok := infos[i].(map[string]any); ok {
					if drMap, ok := inf["DSPCLDRAMT"].(map[string]any); ok {
						item.Debit = parseReportFloat(drMap["DSPCLDRAMTA"])
					}
					if crMap, ok := inf["DSPCLCRAMT"].(map[string]any); ok {
						item.Credit = parseReportFloat(crMap["DSPCLCRAMTA"])
					}
					if clMap, ok := inf["DSPCLAMT"].(map[string]any); ok {
						item.Amount = parseReportFloat(clMap["DSPCLAMTA"])
					}
					if item.Debit == 0 && item.Credit == 0 && item.Amount == 0 {
						item.Amount = parseReportFloat(inf["DSPCLAMTA"])
					}
				}
			}
			rows = append(rows, item)
		}
	}

	// Pattern 2: BSNAME + BSAMT parallel lists (Balance Sheet)
	if bsNames := asSlice(node["BSNAME"]); len(bsNames) > 0 {
		bsAmts := asSlice(node["BSAMT"])
		for i, n := range bsNames {
			label := extractDSPDisplayName(n)
			if label == "" {
				continue
			}
			item := ReportLineItem{Name: label}
			if i < len(bsAmts) {
				if amtMap, ok := bsAmts[i].(map[string]any); ok {
					mainAmt := parseReportFloat(amtMap["BSMAINAMT"])
					subAmt := parseReportFloat(amtMap["BSSUBAMT"])
					if mainAmt != 0 {
						item.Amount = mainAmt
					} else {
						item.Amount = subAmt
					}
				}
			}
			rows = append(rows, item)
		}
	}

	// Pattern 3: PLNAME + PLAMT parallel lists (Profit & Loss)
	if plNames := asSlice(node["PLNAME"]); len(plNames) > 0 {
		plAmts := asSlice(node["PLAMT"])
		for i, n := range plNames {
			label := extractDSPDisplayName(n)
			if label == "" {
				continue
			}
			item := ReportLineItem{Name: label}
			if i < len(plAmts) {
				if amtMap, ok := plAmts[i].(map[string]any); ok {
					mainAmt := parseReportFloat(amtMap["PLMAINAMT"])
					subAmt := parseReportFloat(amtMap["PLSUBAMT"])
					if mainAmt != 0 {
						item.Amount = mainAmt
					} else {
						item.Amount = subAmt
					}
				}
			}
			rows = append(rows, item)
		}
	}

	if len(rows) > 0 {
		return rows
	}

	// Recurse into child maps/slices (e.g. BODY -> DATA)
	for _, v := range node {
		switch child := v.(type) {
		case map[string]any:
			if sub := extractStatementRows(child); len(sub) > 0 {
				return sub
			}
		case []any:
			for _, elem := range child {
				if m, ok := elem.(map[string]any); ok {
					rows = append(rows, extractStatementRows(m)...)
				}
			}
			if len(rows) > 0 {
				return rows
			}
		}
	}
	return nil
}

func extractDSPDisplayName(v any) string {
	if m, ok := v.(map[string]any); ok {
		if dsp, ok := m["DSPACCNAME"].(map[string]any); ok {
			if s := asString(dsp["DSPDISPNAME"]); s != "" {
				return s
			}
		}
		if s := asString(m["DSPDISPNAME"]); s != "" {
			return s
		}
	}
	return asString(v)
}

func extractOutstandingRows(node map[string]any) []OutstandingBillRow {
	var rows []OutstandingBillRow
	if fixedList := asSlice(node["BILLFIXED"]); len(fixedList) > 0 {
		clList := asSlice(node["BILLCL"])
		dueList := asSlice(node["BILLDUE"])
		overdueList := asSlice(node["BILLOVERDUE"])

		for i, f := range fixedList {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			row := OutstandingBillRow{
				BillDate: asString(fm["BILLDATE"]),
				BillRef:  asString(fm["BILLREF"]),
				Party:    asString(fm["BILLPARTY"]),
			}
			if i < len(clList) {
				amt, bType := parseReportAmountAndType(clList[i])
				row.PendingAmount = amt
				row.BalanceType = bType
			}
			if i < len(dueList) {
				row.DueDate = asString(dueList[i])
			}
			if i < len(overdueList) {
				row.OverdueDays = parseOverdueDays(overdueList[i])
			}
			if row.BillRef != "" || row.Party != "" {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			return rows
		}
	}

	for _, v := range node {
		if child, ok := v.(map[string]any); ok {
			if sub := extractOutstandingRows(child); len(sub) > 0 {
				return sub
			}
		}
	}
	return nil
}

func extractDayBookRows(node map[string]any) []DayBookEntryRow {
	var rows []DayBookEntryRow
	if dates := asSlice(node["DSPVCHDATE"]); len(dates) > 0 {
		ledgers := asSlice(node["DSPVCHLEDACCOUNT"])
		types := asSlice(node["DSPVCHTYPE"])
		drs := asSlice(node["DSPVCHDRAMT"])
		crs := asSlice(node["DSPVCHCRAMT"])

		for i, d := range dates {
			row := DayBookEntryRow{
				Date: asString(d),
			}
			if i < len(ledgers) {
				row.Particulars = asString(ledgers[i])
			}
			if i < len(types) {
				row.VoucherType = asString(types[i])
			}
			if i < len(drs) {
				row.Debit = parseReportFloat(drs[i])
			}
			if i < len(crs) {
				row.Credit = parseReportFloat(crs[i])
			}
			if row.Date != "" || row.Particulars != "" {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			return rows
		}
	}

	for _, v := range node {
		if child, ok := v.(map[string]any); ok {
			if sub := extractDayBookRows(child); len(sub) > 0 {
				return sub
			}
		}
	}
	return nil
}
