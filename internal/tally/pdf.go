package tally

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// PDFReportData holds structured data used to render a formatted PDF report or invoice.
type PDFReportData struct {
	Title       string
	Company     string
	DateRange   string
	Party       string
	VoucherNo   string
	VoucherType string
	Headers     []string
	Rows        [][]string
	Summary     map[string]string
	Notes       string
}

// GenerateReportPDF creates a standard, valid PDF-1.4 document containing formatted report or voucher data.
// It requires zero external dependencies or CGO and produces standard A4 documents viewable in all PDF readers.
func GenerateReportPDF(data PDFReportData) ([]byte, error) {
	var stream bytes.Buffer

	// Page dimensions: A4 (595 x 842 pt)
	// Margins: left=40, right=555 (content width=515)
	const (
		pageWidth   = 595.0
		pageHeight  = 842.0
		leftMargin  = 40.0
		rightMargin = 555.0
		width       = rightMargin - leftMargin
	)

	// Background page border / top accent header box
	fmt.Fprintf(&stream, "q\n")
	fmt.Fprintf(&stream, "0.06 0.22 0.42 rg\n") // Navy banner
	fmt.Fprintf(&stream, "%.2f %.2f %.2f %.2f re\n", leftMargin, pageHeight-75, width, 40.0)
	fmt.Fprintf(&stream, "f\n")
	fmt.Fprintf(&stream, "Q\n")

	// Company Title in Navy Banner (White text)
	company := data.Company
	if company == "" {
		company = "Tally ERP Accounting"
	}
	fmt.Fprintf(&stream, "BT\n")
	fmt.Fprintf(&stream, "/F2 16 Tf\n")
	fmt.Fprintf(&stream, "1.0 1.0 1.0 rg\n") // White
	fmt.Fprintf(&stream, "%.2f %.2f Td\n", leftMargin+12, pageHeight-58)
	fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(company))
	fmt.Fprintf(&stream, "ET\n")

	// Report Title
	title := data.Title
	if title == "" {
		title = "Financial Report"
	}
	y := pageHeight - 105.0

	fmt.Fprintf(&stream, "BT\n")
	fmt.Fprintf(&stream, "/F2 14 Tf\n")
	fmt.Fprintf(&stream, "0.1 0.1 0.1 rg\n")
	fmt.Fprintf(&stream, "%.2f %.2f Td\n", leftMargin, y)
	fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(title))
	fmt.Fprintf(&stream, "ET\n")

	// Metadata Line (Voucher / Date range / Party)
	y -= 18.0
	var metaParts []string
	if data.VoucherNo != "" {
		vType := data.VoucherType
		if vType == "" {
			vType = "Voucher"
		}
		metaParts = append(metaParts, fmt.Sprintf("%s #: %s", vType, data.VoucherNo))
	}
	if data.DateRange != "" {
		metaParts = append(metaParts, fmt.Sprintf("Period / Date: %s", data.DateRange))
	}
	if data.Party != "" {
		metaParts = append(metaParts, fmt.Sprintf("Party / Account: %s", data.Party))
	}

	if len(metaParts) > 0 {
		fmt.Fprintf(&stream, "BT\n")
		fmt.Fprintf(&stream, "/F1 9 Tf\n")
		fmt.Fprintf(&stream, "0.35 0.35 0.35 rg\n")
		fmt.Fprintf(&stream, "%.2f %.2f Td\n", leftMargin, y)
		fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(strings.Join(metaParts, "   |   ")))
		fmt.Fprintf(&stream, "ET\n")
		y -= 12.0
	}

	// Accent horizontal rule
	y -= 4.0
	fmt.Fprintf(&stream, "q\n")
	fmt.Fprintf(&stream, "0.08 0.40 0.75 RG\n")
	fmt.Fprintf(&stream, "1.5 w\n")
	fmt.Fprintf(&stream, "%.2f %.2f m %.2f %.2f l S\n", leftMargin, y, rightMargin, y)
	fmt.Fprintf(&stream, "Q\n")

	y -= 20.0

	// Summary Cards / Key-Value Blocks if provided
	if len(data.Summary) > 0 {
		fmt.Fprintf(&stream, "q\n")
		fmt.Fprintf(&stream, "0.95 0.97 0.99 rg\n")
		fmt.Fprintf(&stream, "0.80 0.85 0.90 RG\n")
		fmt.Fprintf(&stream, "0.5 w\n")
		boxHeight := float64(len(data.Summary)*16 + 12)
		fmt.Fprintf(&stream, "%.2f %.2f %.2f %.2f re B\n", leftMargin, y-boxHeight+14, width, boxHeight)
		fmt.Fprintf(&stream, "Q\n")

		sy := y
		for k, v := range data.Summary {
			fmt.Fprintf(&stream, "BT\n")
			fmt.Fprintf(&stream, "/F2 9 Tf\n")
			fmt.Fprintf(&stream, "0.2 0.25 0.35 rg\n")
			fmt.Fprintf(&stream, "%.2f %.2f Td\n", leftMargin+10, sy)
			fmt.Fprintf(&stream, "(%s:) Tj\n", escapePDFText(k))
			fmt.Fprintf(&stream, "/F1 9 Tf\n")
			fmt.Fprintf(&stream, "0.1 0.1 0.1 rg\n")
			fmt.Fprintf(&stream, "%.2f 0 Td\n", 130.0)
			fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(v))
			fmt.Fprintf(&stream, "ET\n")
			sy -= 16.0
		}
		y -= (boxHeight + 16.0)
	}

	// Table (Headers and Rows)
	if len(data.Headers) > 0 {
		colCount := len(data.Headers)
		colWidth := width / float64(colCount)

		// Table header background
		fmt.Fprintf(&stream, "q\n")
		fmt.Fprintf(&stream, "0.91 0.94 0.98 rg\n")
		fmt.Fprintf(&stream, "%.2f %.2f %.2f 20.00 re f\n", leftMargin, y-5.0, width)
		fmt.Fprintf(&stream, "0.75 0.80 0.88 RG\n")
		fmt.Fprintf(&stream, "0.5 w\n")
		fmt.Fprintf(&stream, "%.2f %.2f m %.2f %.2f l S\n", leftMargin, y-5.0, rightMargin, y-5.0)
		fmt.Fprintf(&stream, "Q\n")

		// Header texts
		fmt.Fprintf(&stream, "BT\n")
		fmt.Fprintf(&stream, "/F2 9 Tf\n")
		fmt.Fprintf(&stream, "0.1 0.2 0.35 rg\n")
		for i, h := range data.Headers {
			xPos := leftMargin + float64(i)*colWidth + 6.0
			fmt.Fprintf(&stream, "%.2f %.2f Td\n", xPos, y)
			fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(h))
			fmt.Fprintf(&stream, "%.2f 0 Td\n", -xPos) // reset X for next iteration
		}
		fmt.Fprintf(&stream, "ET\n")
		y -= 22.0

		// Rows
		for rIdx, row := range data.Rows {
			if y < 70.0 {
				// Prevent overflow beyond bottom margin
				break
			}

			// Subtle alternating row background
			if rIdx%2 == 1 {
				fmt.Fprintf(&stream, "q\n")
				fmt.Fprintf(&stream, "0.98 0.98 0.99 rg\n")
				fmt.Fprintf(&stream, "%.2f %.2f %.2f 16.00 re f\n", leftMargin, y-4.0, width)
				fmt.Fprintf(&stream, "Q\n")
			}

			fmt.Fprintf(&stream, "BT\n")
			fmt.Fprintf(&stream, "/F1 8.5 Tf\n")
			fmt.Fprintf(&stream, "0.15 0.15 0.15 rg\n")
			for i, val := range row {
				if i >= colCount {
					break
				}
				xPos := leftMargin + float64(i)*colWidth + 6.0
				fmt.Fprintf(&stream, "%.2f %.2f Td\n", xPos, y)
				fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(val))
				fmt.Fprintf(&stream, "%.2f 0 Td\n", -xPos)
			}
			fmt.Fprintf(&stream, "ET\n")

			// Divider line between rows
			fmt.Fprintf(&stream, "q\n")
			fmt.Fprintf(&stream, "0.90 0.90 0.92 RG\n")
			fmt.Fprintf(&stream, "0.25 w\n")
			fmt.Fprintf(&stream, "%.2f %.2f m %.2f %.2f l S\n", leftMargin, y-4.0, rightMargin, y-4.0)
			fmt.Fprintf(&stream, "Q\n")

			y -= 17.0
		}
	}

	// Notes / Narration if provided
	if data.Notes != "" && y > 80.0 {
		y -= 10.0
		fmt.Fprintf(&stream, "BT\n")
		fmt.Fprintf(&stream, "/F2 8.5 Tf\n")
		fmt.Fprintf(&stream, "0.3 0.3 0.3 rg\n")
		fmt.Fprintf(&stream, "%.2f %.2f Td\n", leftMargin, y)
		fmt.Fprintf(&stream, "(Notes / Narration:) Tj\n")
		fmt.Fprintf(&stream, "/F1 8.5 Tf\n")
		fmt.Fprintf(&stream, "0.4 0.4 0.4 rg\n")
		fmt.Fprintf(&stream, "90.0 0 Td\n")
		fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFText(data.Notes))
		fmt.Fprintf(&stream, "ET\n")
	}

	// Bottom Footer
	fmt.Fprintf(&stream, "q\n")
	fmt.Fprintf(&stream, "0.85 0.85 0.85 RG\n")
	fmt.Fprintf(&stream, "0.5 w\n")
	fmt.Fprintf(&stream, "%.2f 45.00 m %.2f 45.00 l S\n", leftMargin, rightMargin)
	fmt.Fprintf(&stream, "Q\n")

	fmt.Fprintf(&stream, "BT\n")
	fmt.Fprintf(&stream, "/F1 8 Tf\n")
	fmt.Fprintf(&stream, "0.5 0.5 0.5 rg\n")
	fmt.Fprintf(&stream, "%.2f 32.00 Td\n", leftMargin)
	fmt.Fprintf(&stream, "(Generated via Tally MCP Server • %s) Tj\n", escapePDFText(time.Now().Format("02-Jan-2006 15:04:05")))
	fmt.Fprintf(&stream, "ET\n")

	streamBytes := stream.Bytes()

	// Assemble final PDF-1.4 file with object offsets
	var doc bytes.Buffer
	var offsets [7]int

	doc.WriteString("%PDF-1.4\n")
	doc.WriteString("%\xe2\xe3\xcf\xd3\n")

	// Object 1: Catalog
	offsets[1] = doc.Len()
	doc.WriteString("1 0 obj\n")
	doc.WriteString("<< /Type /Catalog /Pages 2 0 R >>\n")
	doc.WriteString("endobj\n")

	// Object 2: Pages
	offsets[2] = doc.Len()
	doc.WriteString("2 0 obj\n")
	doc.WriteString("<< /Type /Pages /Kids [3 0 R] /Count 1 >>\n")
	doc.WriteString("endobj\n")

	// Object 3: Page
	offsets[3] = doc.Len()
	doc.WriteString("3 0 obj\n")
	doc.WriteString("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595.28 841.89] /Resources << /Font << /F1 4 0 R /F2 5 0 R >> >> /Contents 6 0 R >>\n")
	doc.WriteString("endobj\n")

	// Object 4: Font F1 (Helvetica Regular)
	offsets[4] = doc.Len()
	doc.WriteString("4 0 obj\n")
	doc.WriteString("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\n")
	doc.WriteString("endobj\n")

	// Object 5: Font F2 (Helvetica Bold)
	offsets[5] = doc.Len()
	doc.WriteString("5 0 obj\n")
	doc.WriteString("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>\n")
	doc.WriteString("endobj\n")

	// Object 6: Stream Contents
	offsets[6] = doc.Len()
	doc.WriteString("6 0 obj\n")
	doc.WriteString(fmt.Sprintf("<< /Length %d >>\n", len(streamBytes)))
	doc.WriteString("stream\n")
	doc.Write(streamBytes)
	doc.WriteString("\nendstream\n")
	doc.WriteString("endobj\n")

	// Cross-reference table
	xrefOffset := doc.Len()
	doc.WriteString("xref\n")
	doc.WriteString("0 7\n")
	doc.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 6; i++ {
		doc.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}

	// Trailer
	doc.WriteString("trailer\n")
	doc.WriteString("<< /Size 7 /Root 1 0 R >>\n")
	doc.WriteString("startxref\n")
	doc.WriteString(fmt.Sprintf("%d\n", xrefOffset))
	doc.WriteString("%%EOF\n")

	return doc.Bytes(), nil
}

// escapePDFText cleans strings for PDF syntax and removes unsupported control characters.
func escapePDFText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '(':
			b.WriteString(`\(`)
		case ')':
			b.WriteString(`\)`)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			if r >= 32 && r <= 126 {
				b.WriteRune(r)
			} else {
				// Replace non-ASCII or unsupported symbols with safe substitute
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}
