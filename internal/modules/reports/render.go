package reports

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Row is one line of every report: a period bucket, the grouping it was counted under, and
// the metric. Uniform on purpose - the catalogue promises nothing the schema's single value
// column cannot keep.
type Row struct {
	Bucket string  `db:"period_bucket"`
	Label  string  `db:"group_label"`
	Value  float64 `db:"metric_value"`
}

// Content type and extension per format. A browser that gets text/csv downloads a file it can
// open; a browser that gets an xlsx mime type does the same.
func contentTypeOf(format Format) string {
	switch format {
	case FormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case FormatPDF:
		return "application/pdf"
	default:
		return "text/csv; charset=utf-8"
	}
}

func extensionOf(format Format) string {
	switch format {
	case FormatXLSX:
		return ".xlsx"
	case FormatPDF:
		return ".pdf"
	default:
		return ".csv"
	}
}

// render turns one report into the bytes the caller gets. Nothing is written to disk, so
// there is no file to leak, expire or forget to purge. The title is for the PDF, which prints
// one on every page; the other two formats put the range in their own columns.
func render(format Format, title string, headers []string, rows []Row) ([]byte, error) {
	switch format {
	case FormatCSV:
		return renderCSV(headers, rows)
	case FormatXLSX:
		return renderXLSX(headers, rows)
	case FormatPDF:
		return renderPDF(title, headers, rows)
	default:
		return nil, fmt.Errorf("render %s: unsupported format", format)
	}
}

// reportTitle is the line the PDF prints above the table: what the report is and the window it
// covered, because a printed page with only column headings is a page nobody can file.
func reportTitle(name string, from, to time.Time) string {
	return name + " · " + from.UTC().Format("2006-01-02") + " to " + to.UTC().Format("2006-01-02")
}

func renderCSV(headers []string, rows []Row) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(headers); err != nil {
		return nil, fmt.Errorf("write csv header: %w", err)
	}
	for _, row := range rows {
		line := []string{row.Bucket, row.Label, number(row.Value)}
		if err := writer.Write(line); err != nil {
			return nil, fmt.Errorf("write csv row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("flush csv: %w", err)
	}
	return buf.Bytes(), nil
}

// number keeps a whole count reading as a count. AVG() rounded in SQL already arrives whole;
// the trailing .00 is noise a spreadsheet adds on its own.
func number(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

// The XLSX parts are written by hand rather than by a library: the format is a zip of four
// small XML documents, this module needs one sheet of three columns, and a dependency for
// that is a dependency in every deployment for a feature most tenants turn on once a week.
func renderXLSX(headers []string, rows []Row) ([]byte, error) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	parts := []struct {
		name string
		body []byte
	}{
		{"[Content_Types].xml", []byte(contentTypes)},
		{"_rels/.rels", []byte(rootRels)},
		{"xl/workbook.xml", []byte(workbook)},
		{"xl/_rels/workbook.xml.rels", []byte(workbookRels)},
		{"xl/worksheets/sheet1.xml", sheetXML(headers, rows)},
	}
	for _, part := range parts {
		file, err := archive.Create(part.name)
		if err != nil {
			return nil, fmt.Errorf("write xlsx %s: %w", part.name, err)
		}
		if _, err := file.Write(part.body); err != nil {
			return nil, fmt.Errorf("write xlsx %s: %w", part.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("close xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
</Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

const workbook = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Report" sheetId="1" r:id="rId1"/></sheets>
</workbook>`

const workbookRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`

func sheetXML(headers []string, rows []Row) []byte {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	buf.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)

	buf.WriteString(rowXML(1, headers, nil))
	for i, row := range rows {
		values := []string{row.Bucket, row.Label}
		buf.WriteString(rowXML(i+2, values, &row.Value))
	}
	buf.WriteString(`</sheetData></worksheet>`)
	return buf.Bytes()
}

// rowXML writes one row. The numeric cell is last and is the only one stored as a number, so
// a spreadsheet can total the metric without parsing text.
func rowXML(index int, texts []string, metric *float64) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, `<row r="%d">`, index)
	for i, text := range texts {
		fmt.Fprintf(&buf, `<c r="%s%d" t="inlineStr"><is><t>`, column(i), index)
		escape(&buf, text)
		buf.WriteString(`</t></is></c>`)
	}
	if metric != nil {
		fmt.Fprintf(&buf, `<c r="%s%d"><v>%s</v></c>`, column(len(texts)), index, number(*metric))
	}
	buf.WriteString(`</row>`)
	return buf.String()
}

// column is the spreadsheet letter for a zero-based index. One sheet of three columns needs
// A..C, but the function is written for the general case rather than returning a wrong letter
// the day a report gains a column.
func column(index int) string {
	var letters []byte
	for index >= 0 {
		letters = append([]byte{byte('A' + index%26)}, letters...)
		index = index/26 - 1
	}
	return string(letters)
}

func escape(buf *strings.Builder, value string) {
	_ = xml.EscapeText(buf, []byte(value))
}
