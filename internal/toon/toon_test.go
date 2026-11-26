package toon

import (
	"encoding/json"
	"strings"
	"testing"
)

type sampleLedger struct {
	Name           string  `json:"name"`
	Parent         string  `json:"parent"`
	ClosingBalance float64 `json:"closing_balance"`
}

type sampleResponse struct {
	Total   int            `json:"total"`
	Count   int            `json:"count"`
	HasMore bool           `json:"has_more"`
	Ledgers []sampleLedger `json:"ledgers"`
}

func TestMarshalPrimitives(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"int", 42, "42"},
		{"float", 125000.5, "125000.5"},
		{"bool_true", true, "true"},
		{"bool_false", false, "false"},
		{"null", nil, "null"},
		{"bare_string", "Tata Steel Ltd", "Tata Steel Ltd"},
		{"empty_string", "", `""`},
		{"string_with_comma", "Patel, Gopal", `"Patel, Gopal"`},
		{"string_with_colon", "Key: Value", `"Key: Value"`},
		{"string_with_whitespace", " leading", `" leading"`},
		{"numeric_string", "12345", `"12345"`},
		{"bool_string", "true", `"true"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(tt.input)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestMarshalPrimitiveArray(t *testing.T) {
	tags := []string{"sales", "mumbai", "q3"}
	got, err := Marshal(tags)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	expected := "[3]: sales,mumbai,q3"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestMarshalTabularArray(t *testing.T) {
	ledgers := []sampleLedger{
		{Name: "Tata Steel Ltd", Parent: "Sundry Debtors", ClosingBalance: -125000},
		{Name: "HDFC Bank", Parent: "Bank Accounts", ClosingBalance: 875000.5},
	}

	got, err := Marshal(ledgers)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	expected := strings.TrimSpace(`
[2]{closing_balance,name,parent}:
  -125000,Tata Steel Ltd,Sundry Debtors
  875000.5,HDFC Bank,Bank Accounts
`)

	if got != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestMarshalStructuredResponse(t *testing.T) {
	resp := sampleResponse{
		Total:   100,
		Count:   2,
		HasMore: true,
		Ledgers: []sampleLedger{
			{Name: "Cash", Parent: "Cash-in-Hand", ClosingBalance: 15000},
			{Name: "Tata, Motors", Parent: "Sundry Debtors", ClosingBalance: -50000},
		},
	}

	got, err := Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Should contain the header with keys, and row with quoted "Tata, Motors"
	if !strings.Contains(got, "total: 100") {
		t.Errorf("expected 'total: 100', got:\n%s", got)
	}
	if !strings.Contains(got, "count: 2") {
		t.Errorf("expected 'count: 2', got:\n%s", got)
	}
	if !strings.Contains(got, "has_more: true") {
		t.Errorf("expected 'has_more: true', got:\n%s", got)
	}
	if !strings.Contains(got, "ledgers[2]{closing_balance,name,parent}:") {
		t.Errorf("expected tabular header for ledgers, got:\n%s", got)
	}
	if !strings.Contains(got, `"Tata, Motors"`) {
		t.Errorf("expected quoted 'Tata, Motors' due to comma, got:\n%s", got)
	}
}

func TestMarshalEmptyArray(t *testing.T) {
	resp := map[string]any{
		"items": []any{},
	}
	got, err := Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if got != "items[0]:" {
		t.Errorf("expected 'items[0]:', got %q", got)
	}
}

func TestToonSavingsComparison(t *testing.T) {
	ledgers := make([]sampleLedger, 50)
	for i := 0; i < 50; i++ {
		ledgers[i] = sampleLedger{
			Name:           "Debtor Company " + string(rune('A'+(i%26))),
			Parent:         "Sundry Debtors",
			ClosingBalance: float64(-10000 - i*500),
		}
	}
	resp := sampleResponse{
		Total:   500,
		Count:   50,
		HasMore: true,
		Ledgers: ledgers,
	}

	jsonBytes, _ := json.Marshal(resp)
	toonStr, _ := Marshal(resp)

	jsonLen := len(jsonBytes)
	toonLen := len(toonStr)
	savings := float64(jsonLen-toonLen) / float64(jsonLen) * 100

	t.Logf("50 Ledgers JSON bytes: %d, TOON bytes: %d (Saved: %.1f%%)", jsonLen, toonLen, savings)
	if toonLen >= jsonLen {
		t.Errorf("expected TOON (%d) to be smaller than JSON (%d)", toonLen, jsonLen)
	}
}

func TestQuoteDoesNotEscapeHTMLAndEmptyTabularCell(t *testing.T) {
	rows := []map[string]any{
		{"name": "A & B, Pvt Ltd", "note": ""},
		{"name": "Sales < 5%, Co", "note": "ok"},
	}
	got, err := Marshal(rows)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(got, `\u0026`) || strings.Contains(got, `\u003c`) {
		t.Errorf("expected raw &, < in TOON without HTML escaping, got:\n%s", got)
	}
	if !strings.Contains(got, `"A & B, Pvt Ltd",`) {
		t.Errorf("expected empty tabular cell without \"\" quotes, got:\n%s", got)
	}
	if strings.Contains(got, `,"",`) || strings.HasSuffix(got, `,""` ) {
		t.Errorf("expected no literal \"\" in tabular empty cell, got:\n%s", got)
	}
}
