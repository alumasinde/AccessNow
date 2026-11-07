package reports

import (
	"bytes"
	"fmt"
	"math"
	"strings"
)

// A PDF is written here for the same reason the XLSX is: the format this module needs is one
// table of three columns on a page, and a PDF library is a large dependency with its own font
// licensing for that. The writer uses the two standard Helvetica faces every reader ships, so
// nothing is embedded and no font file is shipped.

const (
	pdfWidth      = 595.28 // A4 portrait, in points
	pdfHeight     = 841.89
	pdfMargin     = 40.0
	pdfTitleY     = 790.0
	pdfHeaderY    = 752.0
	pdfRuleY      = 744.0
	pdfFirstY     = 726.0
	pdfLineHeight = 14.0
	pdfBottom     = 56.0
	pdfTitleSize  = 14.0
	pdfBodySize   = 9.0
	pdfFooterY    = 30.0
)

// The three column starts. A4's text width is 515 points, which splits into a date, a label
// wide enough for a gate or host name, and a metric.
var pdfColumns = [3]float64{pdfMargin, 150, 432}

// renderPDF lays the rows out page by page, repeating the title and the column headings on each
// page so a printed bundle can be read without the first sheet.
func renderPDF(title string, headers []string, rows []Row) ([]byte, error) {
	perPage := int(math.Floor((pdfFirstY - pdfBottom) / pdfLineHeight))
	if perPage < 1 {
		perPage = 1
	}
	pages := 1
	if len(rows) > 0 {
		pages = (len(rows) + perPage - 1) / perPage
	}

	streams := make([]string, 0, pages)
	for page := 0; page < pages; page++ {
		var body strings.Builder
		body.WriteString(pdfText(pdfColumns[0], pdfTitleY, "F2", pdfTitleSize, clipRunes(title, 74)))
		for column, header := range headers {
			if column >= len(pdfColumns) {
				break
			}
			body.WriteString(pdfText(pdfColumns[column], pdfHeaderY, "F2", pdfBodySize,
				clipRunes(header, pdfColumnWidth(column))))
		}
		body.WriteString(pdfRule())

		start := page * perPage
		end := start + perPage
		if end > len(rows) {
			end = len(rows)
		}
		y := pdfFirstY
		for _, row := range rows[start:end] {
			body.WriteString(pdfText(pdfColumns[0], y, "F1", pdfBodySize, clipRunes(row.Bucket, 24)))
			body.WriteString(pdfText(pdfColumns[1], y, "F1", pdfBodySize, clipRunes(row.Label, 52)))
			body.WriteString(pdfText(pdfColumns[2], y, "F1", pdfBodySize, clipRunes(number(row.Value), 16)))
			y -= pdfLineHeight
		}
		body.WriteString(pdfText(pdfColumns[0], pdfFooterY, "F1", 8,
			fmt.Sprintf("Page %d of %d", page+1, pages)))
		streams = append(streams, body.String())
	}
	return assemblePDF(streams), nil
}

func pdfColumnWidth(column int) int {
	switch column {
	case 0:
		return 24
	case 1:
		return 52
	default:
		return 16
	}
}

// pdfText places one run of text at an absolute position. Tm rather than Td keeps every line
// independent of the one before it, which is what makes a page a simple loop.
func pdfText(x, y float64, font string, size float64, value string) string {
	return fmt.Sprintf("BT /%s %.1f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n",
		font, size, x, y, pdfEscape(value))
}

func pdfRule() string {
	return fmt.Sprintf("0.6 w %.2f %.2f m %.2f %.2f l S\n",
		pdfMargin, pdfRuleY, pdfWidth-pdfMargin, pdfRuleY)
}

// assemblePDF numbers the objects, records each offset and writes the cross-reference table.
// The offsets are why this cannot be a template string: the table points at byte positions.
func assemblePDF(pages []string) []byte {
	objects := make([]string, 0, 4+2*len(pages))
	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")

	kids := make([]string, 0, len(pages))
	for index := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 5+2*index))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), len(pages)))
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica "+
		"/Encoding /WinAnsiEncoding >>")
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold "+
		"/Encoding /WinAnsiEncoding >>")

	for index, stream := range pages {
		pageID := 5 + 2*index
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] "+
				"/Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>",
				pdfWidth, pdfHeight, pageID+1),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	// The binary comment marks the file as binary for tools that would otherwise translate it.
	buf.WriteString("%\xe2\xe3\xcf\xd3\n")

	offsets := make([]int, len(objects)+1)
	for index, body := range objects {
		offsets[index+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", index+1, body)
	}

	start := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, start)
	return buf.Bytes()
}

// pdfEscape turns a Go string into a PDF literal string body. Text is written as WinAnsi
// (cp1252), which is what the standard fonts are defined over, so an accented name survives and
// a character the encoding has no room for becomes a question mark rather than a broken file.
func pdfEscape(value string) string {
	var buf strings.Builder
	for _, r := range value {
		switch r {
		case '\\':
			buf.WriteString(`\\`)
		case '(':
			buf.WriteString(`\(`)
		case ')':
			buf.WriteString(`\)`)
		case '\n', '\r', '\t':
			buf.WriteByte(' ')
		default:
			if code, ok := winAnsi(r); ok {
				buf.WriteByte(code)
			} else {
				buf.WriteByte('?')
			}
		}
	}
	return buf.String()
}

// winAnsi maps a rune to its cp1252 byte. Latin-1 is the low half verbatim; the 0x80-0x9F
// range is the handful of typographic characters a report label actually uses.
func winAnsi(r rune) (byte, bool) {
	if r >= 0x20 && r <= 0x7e {
		return byte(r), true
	}
	if r >= 0xa0 && r <= 0xff {
		return byte(r), true
	}
	specials := map[rune]byte{
		0x20ac: 0x80, 0x201a: 0x82, 0x0192: 0x83, 0x201e: 0x84, 0x2026: 0x85,
		0x2020: 0x86, 0x2021: 0x87, 0x02c6: 0x88, 0x2030: 0x89, 0x0160: 0x8a,
		0x2039: 0x8b, 0x0152: 0x8c, 0x017d: 0x8e, 0x2018: 0x91, 0x2019: 0x92,
		0x201c: 0x93, 0x201d: 0x94, 0x2022: 0x95, 0x2013: 0x96, 0x2014: 0x97,
		0x02dc: 0x98, 0x2122: 0x99, 0x0161: 0x9a, 0x203a: 0x9b, 0x0153: 0x9c,
		0x017e: 0x9e, 0x0178: 0x9f,
	}
	code, ok := specials[r]
	return code, ok
}

// clipRunes shortens a value to the characters a column can hold. Counting runes rather than
// bytes keeps a clipped name from ending in half a character.
func clipRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
