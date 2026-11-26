package tally

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// GenericXMLNode represents an arbitrary XML node for dynamic tree parsing.
type GenericXMLNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr       `json:"-"`
	Children []GenericXMLNode `json:"-"`
	Content  string           `json:"-"`
}

// XMLToMap parses arbitrary XML into a nested map[string]any structure for clean JSON serialization.
func XMLToMap(xmlData []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var root *rawNode

	var stack []*rawNode
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch elem := token.(type) {
		case xml.StartElement:
			node := &rawNode{
				name:     elem.Name.Local,
				attrs:    elem.Attr,
				children: make([]*rawNode, 0),
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			} else {
				root = node
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			text := strings.TrimSpace(string(elem))
			if text != "" && len(stack) > 0 {
				stack[len(stack)-1].text += text
			}
		}
	}

	if root == nil {
		return map[string]any{}, nil
	}

	return map[string]any{root.name: nodeToValue(root)}, nil
}

type rawNode struct {
	name     string
	attrs    []xml.Attr
	text     string
	children []*rawNode
}

func nodeToValue(n *rawNode) any {
	if len(n.children) == 0 {
		if len(n.attrs) == 0 {
			return n.text
		}
		res := make(map[string]any)
		for _, a := range n.attrs {
			res["@"+a.Name.Local] = a.Value
		}
		if n.text != "" {
			res["#text"] = n.text
		}
		return res
	}

	res := make(map[string]any)
	for _, a := range n.attrs {
		res["@"+a.Name.Local] = a.Value
	}

	// Group children by name
	childGroups := make(map[string][]*rawNode)
	for _, child := range n.children {
		childGroups[child.name] = append(childGroups[child.name], child)
	}

	for name, group := range childGroups {
		if len(group) == 1 {
			res[name] = nodeToValue(group[0])
		} else {
			items := make([]any, len(group))
			for i, item := range group {
				items[i] = nodeToValue(item)
			}
			res[name] = items
		}
	}

	return res
}

// CleanXMLString ensures XML declaration or BOM doesn't interfere with parsing.
func CleanXMLString(raw string) string {
	raw = strings.TrimPrefix(raw, "\ufeff") // Strip UTF-8 BOM
	return strings.TrimSpace(raw)
}

var (
	reLineErrorTag  = regexp.MustCompile(`(?is)<LINEERROR[^>]*>(.*?)</LINEERROR>`)
	reExceptionsTag = regexp.MustCompile(`(?is)<EXCEPTIONS[^>]*>(.*?)</EXCEPTIONS>`)
)

// CheckTallyError inspects raw XML response for Tally error indicators and returns actionable hints.
func CheckTallyError(raw []byte) error {
	str := string(raw)

	var msgs []string
	for _, m := range reLineErrorTag.FindAllStringSubmatch(str, -1) {
		if len(m) > 1 {
			txt := strings.TrimSpace(m[1])
			if txt != "" {
				msgs = append(msgs, txt)
			}
		}
	}
	for _, m := range reExceptionsTag.FindAllStringSubmatch(str, -1) {
		if len(m) > 1 {
			txt := strings.TrimSpace(m[1])
			if txt != "" && txt != "0" {
				msgs = append(msgs, txt)
			}
		}
	}

	if len(msgs) > 0 {
		joined := strings.Join(msgs, "; ")
		hint := inferTallyErrorHint(joined)
		if hint != "" {
			return fmt.Errorf("tally error: %s (hint: %s)", joined, hint)
		}
		return fmt.Errorf("tally error: %s", joined)
	}

	if strings.Contains(str, "<STATUS>0</STATUS>") {
		return fmt.Errorf("tally returned failure status (0); check company selection, date range, or XML request structure")
	}
	return nil
}

func inferTallyErrorHint(errMsg string) string {
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "company") && (strings.Contains(lower, "not") || strings.Contains(lower, "open") || strings.Contains(lower, "exist")):
		return "call tally_list_companies to verify loaded company names and pass the exact 'company' parameter"
	case strings.Contains(lower, "ledger") && strings.Contains(lower, "exist"):
		return "call tally_list_ledgers with 'search' to find the exact ledger name"
	case strings.Contains(lower, "group") && strings.Contains(lower, "exist"):
		return "use a valid Tally parent group such as 'Sundry Debtors', 'Sundry Creditors', 'Bank Accounts', 'Direct Expenses', or 'Indirect Expenses'"
	case strings.Contains(lower, "date"):
		return "provide dates in YYYYMMDD or YYYY-MM-DD format within the loaded company's books period"
	case strings.Contains(lower, "bad formula"):
		return "verify TDL formula syntax, single '=' equality, and XML entity escaping"
	default:
		return ""
	}
}

// PrettyJSON converts any Go value to formatted JSON string.
func PrettyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CompactJSON converts any Go value to a compact JSON string without unnecessary whitespace to minimize token usage.
func CompactJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

var (
	ctrlCharRegex    = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)
	formulaTagRegex  = regexp.MustCompile(`(?is)<SYSTEM\s+TYPE=["']Formulae["'][^>]*>(.*?)</SYSTEM>`)
	formulaNameRegex = regexp.MustCompile(`(?is)<SYSTEM\s+TYPE=["']Formulae["'][^>]*NAME=["']([^"']+)["'][^>]*>`)
	filterTagRegex   = regexp.MustCompile(`(?is)<FILTER>([^<]+)</FILTER>`)
)

// XMLEscapeText escapes XML characters for text content inside tags (& -> &amp;, < -> &lt;, > -> &gt;).
// Does not escape quotes as quotes are valid inside XML text content and needed for TDL string literals.
func XMLEscapeText(s string) string {
	s = ctrlCharRegex.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// XMLEscapeAttr escapes XML characters for attribute values (& -> &amp;, < -> &lt;, > -> &gt;, " -> &quot;, ' -> &apos;).
func XMLEscapeAttr(s string) string {
	s = XMLEscapeText(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// SanitizeTDLString cleans a string for safe inclusion in TDL formula string literals.
// Strips quotes, semicolons (TDL comments), control chars, and newlines.
func SanitizeTDLString(s string) string {
	s = ctrlCharRegex.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\"", "")
	s = strings.ReplaceAll(s, ";", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}

// isStandardTDLFilter checks if a filter name is a known built-in Tally system filter.
func isStandardTDLFilter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "isnonaccountingvoucher", "isaccountingvoucher", "isinventoryvoucher",
		"isorderfilter", "iscancelledvoucher", "isoptionalvoucher", "isdeleted":
		return true
	default:
		return false
	}
}

// ValidateAndSanitizeTallyXML performs pre-flight static analysis on outgoing XML envelopes
// to guarantee that no malformed XML or broken TDL formulas reach Tally.
// Tally's C++ core terminates or halts with "Can not understand, bad formula" when an invalid
// formula or malformed XML expression is received. This function intercepts and prevents crashes.
func ValidateAndSanitizeTallyXML(xmlContent string) (string, error) {
	trimmed := CleanXMLString(xmlContent)
	if trimmed == "" {
		return "", fmt.Errorf("XML query cannot be empty")
	}

	// Strip illegal control characters that cause Tally XML parser failures
	cleaned := ctrlCharRegex.ReplaceAllString(trimmed, "")

	// 1. Basic Envelope structure check
	if !strings.Contains(cleaned, "<ENVELOPE>") && !strings.Contains(cleaned, "<ENVELOPE ") {
		return "", fmt.Errorf("XML query missing <ENVELOPE> root tag")
	}
	if !strings.Contains(cleaned, "</ENVELOPE>") {
		return "", fmt.Errorf("XML query missing </ENVELOPE> closing tag")
	}

	// 2. Well-formed XML validation
	// Decoding all tokens ensures there are no unclosed tags, unescaped raw '<', or unescaped '&' entities
	dec := xml.NewDecoder(strings.NewReader(cleaned))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("malformed XML syntax (%w); sending malformed XML crashes Tally with 'Bad formula'", err)
		}
	}

	// 3. Static Analysis on TDL Formulas (<SYSTEM TYPE="Formulae">)
	formulaMatches := formulaTagRegex.FindAllStringSubmatch(cleaned, -1)
	for _, match := range formulaMatches {
		if len(match) > 1 {
			formulaBody := strings.TrimSpace(match[1])
			if formulaBody == "" {
				return "", fmt.Errorf("empty TDL formula in <SYSTEM TYPE=\"Formulae\">; empty formulas cause Tally to crash with 'Can not understand, bad formula'")
			}

			// Check unbalanced double quotes in formula
			if strings.Count(formulaBody, "\"")%2 != 0 {
				return "", fmt.Errorf("unbalanced quotes in TDL formula %q; causes Tally to crash with 'Can not understand, bad formula'", formulaBody)
			}

			// Check unbalanced parentheses in formula
			if strings.Count(formulaBody, "(") != strings.Count(formulaBody, ")") {
				return "", fmt.Errorf("unbalanced parentheses in TDL formula %q; causes Tally to crash with 'Can not understand, bad formula'", formulaBody)
			}

			// Check invalid '==' operator (TDL uses single '=')
			if strings.Contains(formulaBody, "==") {
				return "", fmt.Errorf("invalid operator '==' in TDL formula %q; TDL uses single '='", formulaBody)
			}

			// Check for known crash patterns like $$Abs on compound Amount
			if strings.Contains(formulaBody, "$$Abs:$Amount") {
				return "", fmt.Errorf("unsupported operator $$Abs on compound Amount field in TDL formula; causes Tally C++ core type panic")
			}
		}
	}

	// 4. Verify that any collection <FILTER> has a matching formula definition
	filterMatches := filterTagRegex.FindAllStringSubmatch(cleaned, -1)
	if len(filterMatches) > 0 {
		definedFormulas := make(map[string]bool)
		for _, nameMatch := range formulaNameRegex.FindAllStringSubmatch(cleaned, -1) {
			if len(nameMatch) > 1 {
				definedFormulas[strings.ToLower(strings.TrimSpace(nameMatch[1]))] = true
			}
		}

		for _, fMatch := range filterMatches {
			if len(fMatch) > 1 {
				fName := strings.TrimSpace(fMatch[1])
				fNameLower := strings.ToLower(fName)
				// Every <FILTER> must reference either a defined <SYSTEM TYPE="Formulae"> or a known TDL system filter
				if !definedFormulas[fNameLower] && !isStandardTDLFilter(fNameLower) {
					return "", fmt.Errorf("collection specifies <FILTER>%s</FILTER> but no matching <SYSTEM TYPE=\"Formulae\" NAME=\"%s\"> exists in XML; causes Tally to crash with 'Can not understand, bad formula'", fName, fName)
				}
			}
		}
	}

	return cleaned, nil
}

// ParseImportResult extracts structured import counters from Tally's XML response.
func ParseImportResult(raw []byte) *ImportResult {
	var env struct {
		Response *ImportResult `xml:"RESPONSE"`
		Body     struct {
			Data struct {
				ImportResult *ImportResult `xml:"IMPORTRESULT"`
			} `xml:"DATA"`
		} `xml:"BODY"`
	}
	// Try direct <RESPONSE> root first, then <ENVELOPE>
	var direct ImportResult
	if err := xml.Unmarshal(raw, &direct); err == nil && (direct.Created > 0 || direct.Altered > 0 || direct.Deleted > 0 || direct.Errors > 0 || direct.Exceptions > 0 || direct.LineError != "") {
		return &direct
	}
	if err := xml.Unmarshal(raw, &env); err == nil {
		if env.Response != nil {
			return env.Response
		}
		if env.Body.Data.ImportResult != nil {
			return env.Body.Data.ImportResult
		}
	}
	return nil
}

// IsMutatingXML returns true if an XML envelope appears to modify Tally data (Import / Create / Alter / Delete).
func IsMutatingXML(xmlContent string) bool {
	upper := strings.ToUpper(xmlContent)
	return strings.Contains(upper, "<TALLYREQUEST>IMPORT</TALLYREQUEST>") ||
		strings.Contains(upper, `ACTION="CREATE"`) ||
		strings.Contains(upper, `ACTION="ALTER"`) ||
		strings.Contains(upper, `ACTION="DELETE"`) ||
		strings.Contains(upper, `ACTION="CANCEL"`)
}

