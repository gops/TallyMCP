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

// CheckTallyError inspects raw XML response for Tally error indicators.
func CheckTallyError(raw []byte) error {
	str := string(raw)
	if strings.Contains(str, "<LINEERROR>") {
		// Extract error message
		start := strings.Index(str, "<LINEERROR>")
		end := strings.Index(str, "</LINEERROR>")
		if start != -1 && end != -1 && end > start {
			errText := str[start+11 : end]
			return fmt.Errorf("tally line error: %s", strings.TrimSpace(errText))
		}
	}
	if strings.Contains(str, "<STATUS>0</STATUS>") {
		return fmt.Errorf("tally returned failure status (0)")
	}
	return nil
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
				// If formulas are defined in the XML, the filter must match one of them or be a known TDL system filter
				if len(definedFormulas) > 0 && !definedFormulas[fNameLower] && !isStandardTDLFilter(fNameLower) {
					return "", fmt.Errorf("collection specifies <FILTER>%s</FILTER> but no matching <SYSTEM TYPE=\"Formulae\" NAME=\"%s\"> exists in XML; causes Tally to crash with 'Can not understand, bad formula'", fName, fName)
				}
			}
		}
	}

	return cleaned, nil
}

