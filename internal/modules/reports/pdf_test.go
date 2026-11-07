package reports

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// TestRenderPDFPagesAndEscaping checks the parts a reader depends on: the header and trailer,
// one page per screenful, parentheses escaped so a label cannot end a string early, and the
// cross-reference offset pointing at the table it claims.
func TestRenderPDFPagesAndEscaping(t *testing.T) {
	rows := make([]Row, 0, 60)
	for index := 0; index < 60; index++ {
		rows = append(rows, Row{Bucket: "2026-03-01", Label: "Main (west)", Value: float64(index)})
	}
	body, err := render(FormatPDF, "Gate movements 2026-03-01 to 2026-03-02",
		[]string{"Day", "Gate", "Movements"}, rows)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.HasPrefix(body, []byte("%PDF-1.4\n")) {
		t.Fatalf("pdf does not start with a version header: %q", body[:16])
	}
	if !bytes.HasSuffix(bytes.TrimSpace(body), []byte("%%EOF")) {
		t.Fatal("pdf does not end with its trailer")
	}
	if !strings.Contains(string(body), "/Count 2") {
		t.Fatal("60 rows should spill onto a second page")
	}
	if !strings.Contains(string(body), "Page 1 of 2") || !strings.Contains(string(body), "Page 2 of 2") {
		t.Fatal("each page should carry its number")
	}
	if !strings.Contains(string(body), `Main \(west\)`) {
		t.Fatal("parentheses in a label must be escaped")
	}

	start := strings.Index(string(body), "\nxref\n")
	if start < 0 {
		t.Fatal("pdf has no xref table")
	}
	marker := strings.Index(string(body), "startxref\n")
	if marker < 0 {
		t.Fatal("pdf has no startxref")
	}
	rest := string(body)[marker+len("startxref\n"):]
	digits := rest[:strings.IndexByte(rest, '\n')]
	offset, err := strconv.Atoi(digits)
	if err != nil {
		t.Fatalf("startxref is not a number: %q", digits)
	}
	if offset != start+1 {
		t.Fatalf("startxref points at %d, want %d", offset, start+1)
	}
}

// TestRenderPDFUsesWinAnsi keeps an accented name readable: cp1252 code points, not UTF-8 bytes
// the standard fonts would render as garbage.
func TestRenderPDFUsesWinAnsi(t *testing.T) {
	body, err := renderPDF("Café register", []string{"Day", "Host", "Visits"},
		[]Row{{Bucket: "2026-03-01", Label: "Café Noir", Value: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("Caf\xe9 Noir")) {
		t.Fatal("expected the label encoded as cp1252 0xe9")
	}
	if bytes.Contains(body, []byte("Caf\xc3\xa9")) {
		t.Fatal("utf-8 bytes would not render in a WinAnsi font")
	}
}

func TestPDFEscapeClosesItsString(t *testing.T) {
	got := pdfEscape(`back\slash (open) close and a newline` + "\n")
	if strings.ContainsAny(got, "\n\r") {
		t.Fatal("a control character must not split a PDF string")
	}
	if !strings.Contains(got, `back\\slash`) || !strings.Contains(got, `\(open\)`) {
		t.Fatalf("escaping is wrong: %s", got)
	}
}
