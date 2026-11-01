package tally

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
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
