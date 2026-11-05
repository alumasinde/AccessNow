// Package numbering issues the human readable reference printed on a badge. Each
// tenant owns its own formats, so one site can run MP-26-000123 while the next runs
// VIS/2026/0045.
package numbering

import (
	"fmt"
	"strings"
	"time"
)

const maxRenderedLen = 32

type Format struct {
	ID            int64   `db:"id"`
	TenantID      int64   `db:"tenant_id"`
	FormatKey     string  `db:"format_key"`
	Name          string  `db:"name"`
	Pattern       string  `db:"pattern"`
	Prefix        *string `db:"prefix"`
	CategoryKey   *string `db:"category_key"`
	ModuleKey     *string `db:"module_key"`
	SequenceWidth int     `db:"sequence_width"`
	Separator     string  `db:"separator_char"`
	ResetPeriod   string  `db:"reset_period"`
	GapRequired   bool    `db:"gap_required"`
}

type Values struct {
	Category string
	Module   string
	At       time.Time
}

// Render substitutes the pattern tokens. An unrecognised token or a result that will
// not fit the column is refused here rather than at insert time.
func (f Format) Render(value uint64, v Values, loc *time.Location) (string, error) {
	local := v.At.In(loc)
	tokens := map[string]string{
		"{PREFIX}":   deref(f.Prefix),
		"{CATEGORY}": firstNonEmpty(v.Category, deref(f.CategoryKey)),
		"{MODULE}":   firstNonEmpty(v.Module, deref(f.ModuleKey)),
		"{YYYY}":     local.Format("2006"),
		"{YY}":       local.Format("06"),
		"{MM}":       local.Format("01"),
		"{DD}":       local.Format("02"),
		"{SEQ}":      pad(value, f.SequenceWidth),
	}

	out := f.Pattern
	for token, replacement := range tokens {
		out = strings.ReplaceAll(out, token, replacement)
	}
	if strings.ContainsAny(out, "{}") {
		return "", fmt.Errorf("pattern %q for %s has an unknown token", f.Pattern, f.FormatKey)
	}
	if len(out) > maxRenderedLen {
		return "", fmt.Errorf("pattern %q renders %d characters, the column holds %d",
			f.Pattern, len(out), maxRenderedLen)
	}
	return out, nil
}

func pad(value uint64, width int) string {
	s := fmt.Sprintf("%d", value)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}
