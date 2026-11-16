package toon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Marshal serializes any Go value into TOON (Token-Oriented Object Notation).
func Marshal(v any) (string, error) {
	// Normalize through JSON to respect json tags, omitempty, custom types, etc.
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("toon: json marshal failed: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return "", fmt.Errorf("toon: json decode failed: %w", err)
	}

	var sb strings.Builder
	encodeValue(&sb, "", normalized, 0, false)
	return strings.TrimRight(sb.String(), "\n"), nil
}

func encodeValue(sb *strings.Builder, key string, val any, indentLevel int, inList bool) {
	indent := strings.Repeat("  ", indentLevel)

	switch v := val.(type) {
	case nil, bool, string, json.Number, float64, int, int64:
		formatted := formatPrimitive(v)
		if key != "" {
			sb.WriteString(fmt.Sprintf("%s%s: %s\n", indent, key, formatted))
		} else {
			sb.WriteString(fmt.Sprintf("%s%s\n", indent, formatted))
		}

	case map[string]any:
		if len(v) == 0 {
			if key != "" {
				sb.WriteString(fmt.Sprintf("%s%s:\n", indent, key))
			}
			return
		}

		if key != "" {
			sb.WriteString(fmt.Sprintf("%s%s:\n", indent, key))
			encodeMap(sb, v, indentLevel+1)
		} else {
			encodeMap(sb, v, indentLevel)
		}

	case []any:
		encodeSlice(sb, key, v, indentLevel)

	default:
		// Fallback for any unexpected types
		formatted := fmt.Sprintf("%v", v)
		if key != "" {
			sb.WriteString(fmt.Sprintf("%s%s: %s\n", indent, key, formatted))
		} else {
			sb.WriteString(fmt.Sprintf("%s%s\n", indent, formatted))
		}
	}
}

func encodeMap(sb *strings.Builder, m map[string]any, indentLevel int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		val := m[k]
		encodeValue(sb, k, val, indentLevel, false)
	}
}

func encodeSlice(sb *strings.Builder, key string, slice []any, indentLevel int) {
	indent := strings.Repeat("  ", indentLevel)
	n := len(slice)

	// Prefix for the array header: e.g. "ledgers" or "" (root array)
	headerPrefix := key
	if headerPrefix != "" {
		headerPrefix = fmt.Sprintf("%s%s[%d]", indent, key, n)
	} else {
		headerPrefix = fmt.Sprintf("%s[%d]", indent, n)
	}

	if n == 0 {
		sb.WriteString(fmt.Sprintf("%s:\n", headerPrefix))
		return
	}

	// 1. Check if all elements are primitives (Inline Form)
	if allPrimitives(slice) {
		vals := make([]string, len(slice))
		for i, item := range slice {
			vals[i] = formatPrimitive(item)
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", headerPrefix, strings.Join(vals, ",")))
		return
	}

	// 2. Check if all elements are maps and can be rendered as Tabular Form
	if cols, ok := getTabularColumns(slice); ok {
		// Header line: key[N]{col1,col2,...}:
		colHeader := strings.Join(cols, ",")
		sb.WriteString(fmt.Sprintf("%s{%s}:\n", headerPrefix, colHeader))

		rowIndent := strings.Repeat("  ", indentLevel+1)
		for _, item := range slice {
			rowMap := item.(map[string]any)
			rowVals := make([]string, len(cols))
			for cIdx, col := range cols {
				colVal, exists := rowMap[col]
				if !exists || colVal == nil {
					rowVals[cIdx] = ""
				} else {
					rowVals[cIdx] = formatPrimitive(colVal)
				}
			}
			sb.WriteString(fmt.Sprintf("%s%s\n", rowIndent, strings.Join(rowVals, ",")))
		}
		return
	}

	// 3. Fallback: List Form
	sb.WriteString(fmt.Sprintf("%s:\n", headerPrefix))
	itemIndent := strings.Repeat("  ", indentLevel+1)
	for _, item := range slice {
		switch it := item.(type) {
		case map[string]any:
			if len(it) == 0 {
				sb.WriteString(fmt.Sprintf("%s-\n", itemIndent))
				continue
			}
			keys := make([]string, 0, len(it))
			for k := range it {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			first := true
			for _, k := range keys {
				val := it[k]
				if first {
					sb.WriteString(fmt.Sprintf("%s- %s: ", itemIndent, k))
					writeListVal(sb, val, indentLevel+2)
					first = false
				} else {
					sb.WriteString(fmt.Sprintf("%s  %s: ", itemIndent, k))
					writeListVal(sb, val, indentLevel+2)
				}
			}
		default:
			sb.WriteString(fmt.Sprintf("%s- %s\n", itemIndent, formatPrimitive(it)))
		}
	}
}

func writeListVal(sb *strings.Builder, val any, indentLevel int) {
	switch v := val.(type) {
	case nil, bool, string, json.Number, float64, int, int64:
		sb.WriteString(fmt.Sprintf("%s\n", formatPrimitive(v)))
	case map[string]any:
		sb.WriteString("\n")
		encodeMap(sb, v, indentLevel)
	case []any:
		sb.WriteString("\n")
		encodeSlice(sb, "", v, indentLevel)
	default:
		sb.WriteString(fmt.Sprintf("%v\n", v))
	}
}

func allPrimitives(slice []any) bool {
	for _, item := range slice {
		if !isPrimitive(item) {
			return false
		}
	}
	return true
}

func isPrimitive(val any) bool {
	if val == nil {
		return true
	}
	switch val.(type) {
	case bool, string, json.Number, float64, int, int64, float32, int32, uint, uint32, uint64:
		return true
	default:
		return false
	}
}

// getTabularColumns checks if all elements are maps with primitive values.
// Returns sorted column headers if tabular format is suitable.
func getTabularColumns(slice []any) ([]string, bool) {
	if len(slice) == 0 {
		return nil, false
	}

	colSet := make(map[string]struct{})
	for _, item := range slice {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		for k, val := range m {
			// All field values must be primitive for standard 2D tabular row
			if !isPrimitive(val) {
				return nil, false
			}
			colSet[k] = struct{}{}
		}
	}

	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols, true
}

func formatPrimitive(val any) string {
	if val == nil {
		return "null"
	}

	switch v := val.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"

	case json.Number:
		return v.String()

	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "null"
		}
		return strconv.FormatFloat(v, 'f', -1, 64)

	case int:
		return strconv.Itoa(v)

	case int64:
		return strconv.FormatInt(v, 10)

	case string:
		return formatString(v)

	default:
		return formatString(fmt.Sprintf("%v", v))
	}
}

func formatString(s string) string {
	// Rules for quotes:
	// 1. Empty string
	if s == "" {
		return `""`
	}

	// 2. Leading or trailing whitespace
	if s != strings.TrimSpace(s) {
		return quoteJSONString(s)
	}

	// 3. Exact boolean or null keywords
	if s == "true" || s == "false" || s == "null" {
		return quoteJSONString(s)
	}

	// 4. Looks syntactically like a number
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return quoteJSONString(s)
	}

	// 5. Contains structural or escape characters
	if strings.ContainsAny(s, ",:[]{}\"\n\r\t") {
		return quoteJSONString(s)
	}

	// Otherwise, return bare string
	return s
}

func quoteJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
