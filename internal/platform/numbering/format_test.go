package numbering

import (
	"strings"
	"testing"
	"time"
)

func str(s string) *string { return &s }

func TestRenderReplacesTokens(t *testing.T) {
	format := Format{
		FormatKey: "material", Pattern: "{PREFIX}{CATEGORY}/{MODULE}/{YYYY}{MM}{DD}/{SEQ}",
		Prefix: str("MP-"), CategoryKey: str("goods"), ModuleKey: str("mat"),
		SequenceWidth: 6,
	}
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	// Nothing on the request, so the format's own category and module are used.
	got, err := format.Render(42, Values{At: at}, time.UTC)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "MP-goods/mat/20261105/000042"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// A request-level category or module beats the format's own default.
func TestRenderValuesOverrideFormat(t *testing.T) {
	format := Format{FormatKey: "visitor", Pattern: "{CATEGORY}/{MODULE}/{SEQ}",
		CategoryKey: str("visitor"), ModuleKey: str("visitor_management"), SequenceWidth: 3}

	got, err := format.Render(7, Values{Category: "vip", Module: "desk", At: time.UnixMilli(0)}, time.UTC)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "vip/desk/007"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderRefusesBadPatterns(t *testing.T) {
	at := time.UnixMilli(0)
	tests := []struct {
		name    string
		pattern string
	}{
		{"unknown token", "{PREFIX}{WW}{SEQ}"},
		{"too long", "{PREFIX}{YYYY}{SEQ}"},
	}
	for _, tc := range tests {
		format := Format{FormatKey: "default", Pattern: tc.pattern,
			Prefix: str(strings.Repeat("X", 30)), SequenceWidth: 5}
		if _, err := format.Render(1, Values{At: at}, time.UTC); err == nil {
			t.Errorf("%s: Render accepted %q", tc.name, tc.pattern)
		}
	}
}

// Wide sequences do not truncate: a counter past the width still renders in full.
func TestPadDoesNotTruncate(t *testing.T) {
	if got := pad(123456, 3); got != "123456" {
		t.Errorf("pad = %q, want %q", got, "123456")
	}
	if got := pad(9, 4); got != "0009" {
		t.Errorf("pad = %q, want %q", got, "0009")
	}
}

// Every starter format must work the moment a tenant is created, so none of them may
// render past the pass_no column or carry a stray token.
func TestStarterFormatsRender(t *testing.T) {
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	seen := map[string]bool{}

	for _, format := range starter {
		if seen[format.FormatKey] {
			t.Fatalf("duplicate starter format %q", format.FormatKey)
		}
		seen[format.FormatKey] = true

		number, err := format.Render(1, Values{At: at}, time.UTC)
		if err != nil {
			t.Fatalf("format %q: %v", format.FormatKey, err)
		}
		if len(number) > maxRenderedLen {
			t.Errorf("format %q renders %q (%d chars)", format.FormatKey, number, len(number))
		}
	}
	if !seen[DefaultFormatKey] {
		t.Errorf("starter set has no %q format; Allocator falls back to it", DefaultFormatKey)
	}
}
