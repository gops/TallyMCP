package tally

import (
	"bytes"
	"fmt"
	"sort"
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
// It supports automatic multi-page pagination, deterministic summary ordering, and requires zero external dependencies.
func GenerateReportPDF(data PDFReportData) ([]byte, error) {
	const (
		pageWidth   = 595.0
		pageHeight  = 842.0
		leftMargin  = 40.0
		rightMargin = 555.0
		width       = rightMargin - leftMargin
		bottomLimit = 70.0
	)

	company := data.Company
	if company == "" {
		company = "Tally ERP Accounting"
	}
	title := data.Title
	if title == "" {
		title = "Financial Report"
	}

	var pages []bytes.Buffer
	var stream bytes.Buffer

	drawBannerAndTitle := func(buf *bytes.Buffer, continuation bool) float64 {
		// Navy banner
		fmt.Fprintf(buf, "q\n0.06 0.22 0.42 rg\n%.2f %.2f %.2f %.2f re\nf\nQ\n", leftMargin, pageHeight-75, width, 40.0)

		// Company title in banner
		fmt.Fprintf(buf, "BT\n/F2 16 Tf\n1.0 1.0 1.0 rg\n%.2f %.2f Td\n(%s) Tj\nET\n", leftMargin+12, pageHeight-58, escapePDFText(company))

		y := pageHeight - 105.0
		displayTitle := title
		if continuation {
			displayTitle = title + " (Continued)"
		}
		fmt.Fprintf(buf, "BT\n/F2 14 Tf\n0.1 0.1 0.1 rg\n%.2f %.2f Td\n(%s) Tj\nET\n", leftMargin, y, escapePDFText(displayTitle))
		y -= 18.0

		if !continuation {
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
				fmt.Fprintf(buf, "BT\n/F1 9 Tf\n0.35 0.35 0.35 rg\n%.2f %.2f Td\n(%s) Tj\nET\n", leftMargin, y, escapePDFText(strings.Join(metaParts, "   |   ")))
				y -= 12.0
			}
		}

		// Accent horizontal rule
		y -= 4.0
		fmt.Fprintf(buf, "q\n0.08 0.40 0.75 RG\n1.5 w\n%.2f %.2f m %.2f %.2f l S\nQ\n", leftMargin, y, rightMargin, y)
		return y - 20.0
	}

	drawTableHeader := func(buf *bytes.Buffer, y float64) float64 {
		if len(data.Headers) == 0 {
			return y
		}
		colWidth := width / float64(len(data.Headers))
		fmt.Fprintf(buf, "q\n0.91 0.94 0.98 rg\n%.2f %.2f %.2f 20.00 re f\n0.75 0.80 0.88 RG\n0.5 w\n%.2f %.2f m %.2f %.2f l S\nQ\n",
			leftMargin, y-5.0, width, leftMargin, y-5.0, rightMargin, y-5.0)

		fmt.Fprintf(buf, "BT\n/F2 9 Tf\n0.1 0.2 0.35 rg\n")
		for i, h := range data.Headers {
			xPos := leftMargin + float64(i)*colWidth + 6.0
			fmt.Fprintf(buf, "%.2f %.2f Td\n(%s) Tj\n%.2f 0 Td\n", xPos, y, escapePDFText(h), -xPos)
		}
		fmt.Fprintf(buf, "ET\n")
		return y - 22.0
	}

	y := drawBannerAndTitle(&stream, false)

	// Summary Cards / Key-Value Blocks in deterministic key order
	if len(data.Summary) > 0 {
		keys := make([]string, 0, len(data.Summary))
		for k := range data.Summary {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		boxHeight := float64(len(keys)*16 + 12)
		fmt.Fprintf(&stream, "q\n0.95 0.97 0.99 rg\n0.80 0.85 0.90 RG\n0.5 w\n%.2f %.2f %.2f %.2f re B\nQ\n",
			leftMargin, y-boxHeight+14, width, boxHeight)

		sy := y
		for _, k := range keys {
			fmt.Fprintf(&stream, "BT\n/F2 9 Tf\n0.2 0.25 0.35 rg\n%.2f %.2f Td\n(%s:) Tj\n/F1 9 Tf\n0.1 0.1 0.1 rg\n130.00 0 Td\n(%s) Tj\nET\n",
				leftMargin+10, sy, escapePDFText(k), escapePDFText(data.Summary[k]))
			sy -= 16.0
		}
		y -= (boxHeight + 16.0)
	}

	// Table (Headers and Rows) with automatic page breaks
	if len(data.Headers) > 0 {
		colCount := len(data.Headers)
		colWidth := width / float64(colCount)
		y = drawTableHeader(&stream, y)

		for rIdx, row := range data.Rows {
			if y < bottomLimit {
				pages = append(pages, stream)
				stream = bytes.Buffer{}
				y = drawBannerAndTitle(&stream, true)
				y = drawTableHeader(&stream, y)
			}

			if rIdx%2 == 1 {
				fmt.Fprintf(&stream, "q\n0.98 0.98 0.99 rg\n%.2f %.2f %.2f 16.00 re f\nQ\n", leftMargin, y-4.0, width)
			}

			fmt.Fprintf(&stream, "BT\n/F1 8.5 Tf\n0.15 0.15 0.15 rg\n")
			for i, val := range row {
				if i >= colCount {
					break
				}
				xPos := leftMargin + float64(i)*colWidth + 6.0
				fmt.Fprintf(&stream, "%.2f %.2f Td\n(%s) Tj\n%.2f 0 Td\n", xPos, y, escapePDFText(val), -xPos)
			}
			fmt.Fprintf(&stream, "ET\n")

			fmt.Fprintf(&stream, "q\n0.90 0.90 0.92 RG\n0.25 w\n%.2f %.2f m %.2f %.2f l S\nQ\n", leftMargin, y-4.0, rightMargin, y-4.0)
			y -= 17.0
		}
	}

	// Notes / Narration if provided
	if data.Notes != "" {
		if y < 80.0 {
			pages = append(pages, stream)
			stream = bytes.Buffer{}
			y = drawBannerAndTitle(&stream, true)
		}
		y -= 10.0
		fmt.Fprintf(&stream, "BT\n/F2 8.5 Tf\n0.3 0.3 0.3 rg\n%.2f %.2f Td\n(Notes / Narration:) Tj\n/F1 8.5 Tf\n0.4 0.4 0.4 rg\n90.0 0 Td\n(%s) Tj\nET\n",
			leftMargin, y, escapePDFText(data.Notes))
	}

	pages = append(pages, stream)

	// Append footer with page numbers to every page
	totalPages := len(pages)
	timestamp := time.Now().Format("02-Jan-2006 15:04:05")
	for idx := range pages {
		fmt.Fprintf(&pages[idx], "q\n0.85 0.85 0.85 RG\n0.5 w\n%.2f 45.00 m %.2f 45.00 l S\nQ\n", leftMargin, rightMargin)
		footer := fmt.Sprintf("Generated via Tally MCP Server - %s   |   Page %d of %d", timestamp, idx+1, totalPages)
		fmt.Fprintf(&pages[idx], "BT\n/F1 8 Tf\n0.5 0.5 0.5 rg\n%.2f 32.00 Td\n(%s) Tj\nET\n", leftMargin, escapePDFText(footer))
	}

	// Assemble PDF-1.4 document.
	// Single-page PDFs preserve object IDs 1..6 (xref 0 7) for backwards compatibility;
	// additional pages append Page/Stream object pairs starting at object ID 7.
	totalObjects := 7 + (totalPages-1)*2
	offsets := make([]int, totalObjects)
	pageObjIDs := make([]int, totalPages)
	streamObjIDs := make([]int, totalPages)
	pageObjIDs[0] = 3
	streamObjIDs[0] = 6
	for p := 1; p < totalPages; p++ {
		pageObjIDs[p] = 7 + (p-1)*2
		streamObjIDs[p] = 8 + (p-1)*2
	}

	var kidsRefs []string
	for _, id := range pageObjIDs {
		kidsRefs = append(kidsRefs, fmt.Sprintf("%d 0 R", id))
	}

	var doc bytes.Buffer
	doc.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	// Object 1: Catalog
	offsets[1] = doc.Len()
	doc.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Object 2: Pages
	offsets[2] = doc.Len()
	fmt.Fprintf(&doc, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kidsRefs, " "), totalPages)

	// Object 3: Page 1
	offsets[3] = doc.Len()
	doc.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595.28 841.89] /Resources << /Font << /F1 4 0 R /F2 5 0 R >> >> /Contents 6 0 R >>\nendobj\n")

	// Object 4: Font F1 (Helvetica Regular)
	offsets[4] = doc.Len()
	doc.WriteString("4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	// Object 5: Font F2 (Helvetica Bold)
	offsets[5] = doc.Len()
	doc.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>\nendobj\n")

	// Object 6: Page 1 Stream
	p0Bytes := pages[0].Bytes()
	offsets[6] = doc.Len()
	fmt.Fprintf(&doc, "6 0 obj\n<< /Length %d >>\nstream\n", len(p0Bytes))
	doc.Write(p0Bytes)
	doc.WriteString("\nendstream\nendobj\n")

	// Additional pages (if any)
	for p := 1; p < totalPages; p++ {
		pID := pageObjIDs[p]
		sID := streamObjIDs[p]

		offsets[pID] = doc.Len()
		fmt.Fprintf(&doc, "%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595.28 841.89] /Resources << /Font << /F1 4 0 R /F2 5 0 R >> >> /Contents %d 0 R >>\nendobj\n", pID, sID)

		sBytes := pages[p].Bytes()
		offsets[sID] = doc.Len()
		fmt.Fprintf(&doc, "%d 0 obj\n<< /Length %d >>\nstream\n", sID, len(sBytes))
		doc.Write(sBytes)
		doc.WriteString("\nendstream\nendobj\n")
	}

	// Cross-reference table
	xrefOffset := doc.Len()
	fmt.Fprintf(&doc, "xref\n0 %d\n0000000000 65535 f \n", totalObjects)
	for i := 1; i < totalObjects; i++ {
		fmt.Fprintf(&doc, "%010d 00000 n \n", offsets[i])
	}

	// Trailer
	fmt.Fprintf(&doc, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjects, xrefOffset)

	return doc.Bytes(), nil
}

// escapePDFText cleans strings for PDF syntax and maps common Unicode punctuation/currency symbols to ASCII.
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
		case '₹':
			b.WriteString("Rs.")
		case '•', '–', '—':
			b.WriteByte('-')
		case '‘', '’':
			b.WriteByte('\'')
		case '“', '”':
			b.WriteString(`\"`)
		case '…':
			b.WriteString("...")
		default:
			if r >= 32 && r <= 126 {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}
