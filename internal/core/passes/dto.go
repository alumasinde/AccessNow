package passes

import (
	"net/http"
	"strconv"
	"time"

	"accessnow/internal/platform/errs"
)

// PassView is the register row. PII is present because the reader already cleared
// core.pass.view; the retention job blanks the columns behind it, not this view.
type PassView struct {
	ID           int64      `json:"id"`
	UUID         string     `json:"uuid"`
	PassNo       string     `json:"pass_no"`
	Status       string     `json:"status"`
	Category     string     `json:"category"`
	Module       string     `json:"module"`
	Origin       string     `json:"origin"`
	SiteID       int64      `json:"site_id"`
	SubjectName  string     `json:"subject_name"`
	SubjectPhone *string    `json:"subject_phone"`
	Purpose      *string    `json:"purpose"`
	HostUserID   *int64     `json:"host_user_id"`
	ValidFrom    time.Time  `json:"valid_from"`
	ValidUntil   *time.Time `json:"valid_until"`
	EntryCount   int        `json:"entry_count"`
	IssuedAt     *time.Time `json:"issued_at"`
	DecidedAt    *time.Time `json:"decided_at"`
	DecisionNote *string    `json:"decision_note"`
	CreatedAt    time.Time  `json:"created_at"`
}

func View(p Pass) PassView {
	return PassView{
		ID: p.ID, UUID: p.UUID, PassNo: p.PassNo, Status: p.Status,
		Category: p.CategoryKey, Module: p.SourceModuleKey, Origin: p.Origin,
		SiteID: p.SiteID, SubjectName: p.SubjectName, SubjectPhone: p.SubjectPhone,
		Purpose: p.Purpose, HostUserID: p.HostUserID, ValidFrom: p.ValidFrom,
		ValidUntil: p.ValidUntil, EntryCount: p.EntryCount, IssuedAt: p.IssuedAt,
		DecidedAt: p.DecidedAt, DecisionNote: p.DecisionNote, CreatedAt: p.CreatedAt,
	}
}

// Filter is the register's query string. Everything is optional and additive.
type Filter struct {
	SiteID   int64
	Status   string
	Category string
	Module   string
	Search   string
}

// ParseFilter reads ?site_id=&status=&category=&module=&q= and refuses a status the
// enum does not know, so a typo returns 400 instead of an empty page.
func ParseFilter(r *http.Request) (Filter, error) {
	query := r.URL.Query()
	filter := Filter{
		Status:   query.Get("status"),
		Category: query.Get("category"),
		Module:   query.Get("module"),
		Search:   query.Get("q"),
	}
	if raw := query.Get("site_id"); raw != "" {
		siteID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || siteID <= 0 {
			return Filter{}, errs.BadField("site_id", "not a positive id")
		}
		filter.SiteID = siteID
	}
	if filter.Status != "" && !Valid(Status(filter.Status)) {
		return Filter{}, errs.BadField("status", filter.Status+" is not a pass status")
	}
	return filter, nil
}
