package passes

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/numbering"
	"accessnow/internal/platform/tenancy"
)

// NumberFormatRequest is one tenant edit to the scheme printed on a pass. The key is in the
// URL, because it is the stable name modules ask for and a body that could rename it would
// break every issue path still holding the old one.
type NumberFormatRequest struct {
	Name          string  `json:"name" validate:"required,max=128"`
	Pattern       string  `json:"pattern" validate:"required,max=96"`
	Prefix        *string `json:"prefix" validate:"omitempty,max=16"`
	CategoryKey   *string `json:"category_key" validate:"omitempty,max=64"`
	ModuleKey     *string `json:"module_key" validate:"omitempty,max=64"`
	SequenceWidth int     `json:"sequence_width" validate:"required,min=1,max=10"`
	Separator     string  `json:"separator_char" validate:"omitempty,max=1"`
	ResetPeriod   string  `json:"reset_period" validate:"required,max=16"`
	GapRequired   bool    `json:"gap_required"`
}

type NumberFormatView struct {
	Key           string  `json:"format_key"`
	Name          string  `json:"name"`
	Pattern       string  `json:"pattern"`
	Prefix        *string `json:"prefix,omitempty"`
	CategoryKey   *string `json:"category_key,omitempty"`
	ModuleKey     *string `json:"module_key,omitempty"`
	SequenceWidth int     `json:"sequence_width"`
	Separator     string  `json:"separator_char"`
	ResetPeriod   string  `json:"reset_period"`
	GapRequired   bool    `json:"gap_required"`
}

func formatView(f numbering.Format) NumberFormatView {
	return NumberFormatView{
		Key: f.FormatKey, Name: f.Name, Pattern: f.Pattern, Prefix: f.Prefix,
		CategoryKey: f.CategoryKey, ModuleKey: f.ModuleKey, SequenceWidth: f.SequenceWidth,
		Separator: f.Separator, ResetPeriod: f.ResetPeriod, GapRequired: f.GapRequired,
	}
}

var formatTokens = []string{"{PREFIX}", "{CATEGORY}", "{MODULE}", "{YYYY}", "{YY}", "{MM}", "{DD}", "{SEQ}"}

func (in NumberFormatRequest) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errs.BadField("name", "is required")
	}
	in.Pattern = strings.TrimSpace(in.Pattern)
	if in.Pattern == "" {
		return errs.BadField("pattern", "is required")
	}
	if !strings.Contains(in.Pattern, "{SEQ}") {
		return errs.BadField("pattern", "must contain {SEQ}, or every pass gets the same number")
	}
	left := in.Pattern
	for _, token := range formatTokens {
		left = strings.ReplaceAll(left, token, "")
	}
	if strings.ContainsAny(left, "{}") {
		return errs.BadField("pattern", "contains a token this system does not know")
	}
	if in.SequenceWidth < 1 || in.SequenceWidth > 10 {
		return errs.BadField("sequence_width", "must be between 1 and 10")
	}
	switch in.ResetPeriod {
	case "hourly", "daily", "monthly", "yearly", "never":
	default:
		return errs.BadField("reset_period", "must be hourly, daily, monthly, yearly or never")
	}
	if len(in.Separator) > 1 {
		return errs.BadField("separator_char", "must be empty or one character")
	}
	if in.Prefix != nil && len(*in.Prefix) > 16 {
		return errs.BadField("prefix", "must be 16 characters or fewer")
	}
	return nil
}

// NumberFormats lists the tenant's scheme. It is a read of configuration, guarded by the pass
// read permission because the numbers are what the pass register is keyed by.
func (h *Handler) NumberFormats(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	formats, err := h.svc.NumberFormats(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]NumberFormatView, 0, len(formats))
	for _, format := range formats {
		views = append(views, formatView(format))
	}
	httpx.OK(w, map[string]any{"items": views})
}

// SaveNumberFormat edits one series. Existing passes keep the numbers they were printed with;
// the change applies to the next number claimed, which is why this is safe to do at midday.
func (h *Handler) SaveNumberFormat(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	key := strings.TrimSpace(chiURLParam(r, "formatKey"))
	if key == "" || len(key) > 64 {
		httpx.Fail(r.Context(), w, r, errs.BadField("format_key", "must be 1 to 64 characters"))
		return
	}
	var req NumberFormatRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	format, err := h.svc.SaveNumberFormat(r.Context(), scope, key, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, formatView(format))
}

func (s *Service) NumberFormats(ctx context.Context, tenantID int64) ([]numbering.Format, error) {
	return s.numbers.Formats(ctx, tenantID)
}

func (s *Service) SaveNumberFormat(ctx context.Context, scope tenancy.Scope, key string,
	in NumberFormatRequest) (numbering.Format, error) {
	if err := in.validate(); err != nil {
		return numbering.Format{}, err
	}
	format := numbering.Format{
		TenantID: scope.TenantID, FormatKey: key, Name: strings.TrimSpace(in.Name),
		Pattern: strings.TrimSpace(in.Pattern), Prefix: in.Prefix, CategoryKey: in.CategoryKey,
		ModuleKey: in.ModuleKey, SequenceWidth: in.SequenceWidth, Separator: in.Separator,
		ResetPeriod: in.ResetPeriod, GapRequired: in.GapRequired,
	}
	// A render at a sample value is the only honest validation of the whole format: it catches
	// a pattern that is individually legal but 40 characters wide once its tokens land.
	if _, err := format.Render(1, numbering.Values{At: time.Now().UTC()}, time.UTC); err != nil {
		return numbering.Format{}, errs.BadField("pattern", err.Error())
	}
	if err := s.numbers.SaveFormat(ctx, format); err != nil {
		return numbering.Format{}, err
	}
	if err := s.chain.Write(ctx, s.pool, audit.Entry{
		TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "pass.number_format_saved",
		SubjectType: "pass_number_format", SubjectID: 0, SiteID: scope.SiteID,
		After: map[string]any{"format_key": key, "pattern": format.Pattern,
			"sequence_width": format.SequenceWidth, "reset_period": format.ResetPeriod},
	}); err != nil {
		return numbering.Format{}, err
	}
	return s.numbers.Format(ctx, scope.TenantID, key)
}

// chiURLParam keeps this file from importing chi just for one lookup; the handler is mounted
// by chi, so the route context is the source either way.
func chiURLParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}
