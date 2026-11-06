package blocklist

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// EntryRequest is one listing as the desk files it. The value is typed the way a person would
// say it - a phone with a plus, a plate with a dash - because normalisation and hashing are
// this module's business, not the client's.
type EntryRequest struct {
	ListType     string     `json:"list_type" validate:"required,oneof=blocklist watchlist"`
	EntityType   string     `json:"entity_type" validate:"required,oneof=person vehicle company device"`
	MatchType    string     `json:"match_type" validate:"required,oneof=id_number phone plate email name_hash badge_no card_no photo_hash"`
	MatchValue   string     `json:"match_value" validate:"required,max=255"`
	DisplayName  string     `json:"display_name" validate:"omitempty,max=255"`
	Organisation string     `json:"organisation" validate:"omitempty,max=255"`
	Reason       string     `json:"reason" validate:"required,max=512"`
	Severity     string     `json:"severity" validate:"omitempty,oneof=low medium high"`
	Source       string     `json:"source" validate:"omitempty,oneof=police internal tenant incident import"`
	EvidenceRef  string     `json:"evidence_ref" validate:"omitempty,max=255"`
	IncidentPass int64      `json:"incident_pass_id" validate:"omitempty,gt=0"`
	AlertRoleKey string     `json:"alert_supervisor_role_key" validate:"omitempty,max=64"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Notes        string     `json:"notes" validate:"omitempty,max=2000"`
}

func (req EntryRequest) toNew() New {
	return New{
		ListType: ListType(req.ListType), EntityType: EntityType(req.EntityType),
		MatchType: MatchType(req.MatchType), MatchValue: req.MatchValue,
		DisplayName:  strings.TrimSpace(req.DisplayName),
		Organisation: strings.TrimSpace(req.Organisation),
		Reason:       strings.TrimSpace(req.Reason), Severity: Severity(req.Severity),
		Source: strings.TrimSpace(req.Source), EvidenceRef: strings.TrimSpace(req.EvidenceRef),
		IncidentPass: req.IncidentPass, AlertRoleKey: strings.TrimSpace(req.AlertRoleKey),
		ExpiresAt: req.ExpiresAt, Notes: strings.TrimSpace(req.Notes),
	}
}

// RevokeRequest takes a listing off the list. The reason is required because a ban that ends
// without a sentence leaves the next desk guessing, and the row is never deleted.
type RevokeRequest struct {
	Reason string `json:"reason" validate:"required,max=512"`
}

// ResolveRequest closes a hit with what somebody concluded about it.
type ResolveRequest struct {
	Resolution string `json:"resolution" validate:"required,max=1024"`
}

// EntryView is one listing as the register reads it. is_open is computed from the status and
// the entry's own expiry, so a screen never has to guess whether a listing can still bite.
type EntryView struct {
	ID             int64      `json:"id"`
	ListType       string     `json:"list_type"`
	EntityType     string     `json:"entity_type"`
	MatchType      string     `json:"match_type"`
	MatchValue     string     `json:"match_value"`
	DisplayName    *string    `json:"display_name,omitempty"`
	Organisation   *string    `json:"organisation,omitempty"`
	PhotoPath      *string    `json:"photo_path,omitempty"`
	Reason         string     `json:"reason"`
	Severity       string     `json:"severity"`
	Source         *string    `json:"source,omitempty"`
	EvidenceRef    *string    `json:"evidence_ref,omitempty"`
	IncidentPassID *int64     `json:"incident_pass_id,omitempty"`
	AlertRoleKey   *string    `json:"alert_supervisor_role_key,omitempty"`
	HitCount       int        `json:"hit_count"`
	LastHitAt      *time.Time `json:"last_hit_at,omitempty"`
	Status         string     `json:"status"`
	AddedBy        *int64     `json:"added_by_user_id,omitempty"`
	AddedAt        time.Time  `json:"added_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	RevokeReason   *string    `json:"revoke_reason,omitempty"`
	Notes          *string    `json:"notes,omitempty"`
	Expired        bool       `json:"is_expired"`
	Open           bool       `json:"is_open"`
}

func viewOf(e Entry) EntryView {
	now := time.Now().UTC()
	expired := e.ExpiredAt(now)
	return EntryView{
		ID: e.ID, ListType: e.ListType, EntityType: e.EntityType, MatchType: e.MatchType,
		MatchValue: e.MatchValue, DisplayName: e.DisplayName, Organisation: e.Organisation,
		PhotoPath: e.PhotoPath, Reason: e.Reason, Severity: e.Severity, Source: e.Source,
		EvidenceRef: e.EvidenceRef, IncidentPassID: e.IncidentPass, AlertRoleKey: e.AlertRoleKey,
		HitCount: e.HitCount, LastHitAt: e.LastHitAt, Status: e.Status, AddedBy: e.AddedBy,
		AddedAt: e.AddedAt, ExpiresAt: e.ExpiresAt, RevokedAt: e.RevokedAt,
		RevokeReason: e.RevokeReason, Notes: e.Notes,
		Expired: expired, Open: e.Open() && !expired,
	}
}

func viewsOf(entries []Entry) []EntryView {
	views := make([]EntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, viewOf(entry))
	}
	return views
}

// HitView is one match with the listing, the pass and the gate it happened at. The joined
// names are not here: the listing's own id is the link, and the register shows the rest.
type HitView struct {
	ID             int64      `json:"id"`
	EntryID        int64      `json:"entry_id"`
	MatchedOn      string     `json:"matched_on"`
	MatchedValue   *string    `json:"matched_value,omitempty"`
	PassID         *int64     `json:"pass_id,omitempty"`
	PassNo         *string    `json:"pass_no,omitempty"`
	MovementID     *int64     `json:"movement_id,omitempty"`
	GateID         *int64     `json:"gate_id,omitempty"`
	GateName       *string    `json:"gate_name,omitempty"`
	GateSessionID  *int64     `json:"gate_session_id,omitempty"`
	ActionTaken    string     `json:"action_taken"`
	OccurredAt     time.Time  `json:"occurred_at"`
	ListType       string     `json:"list_type"`
	Severity       string     `json:"severity"`
	EntryReason    string     `json:"entry_reason"`
	DisplayName    *string    `json:"display_name,omitempty"`
	EntryMatchType string     `json:"entry_match_type"`
	EntryMatchVal  string     `json:"entry_match_value"`
	ResolvedBy     *int64     `json:"resolved_by_user_id,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	Resolution     *string    `json:"resolution,omitempty"`
	Open           bool       `json:"is_open"`
}

func hitViewOf(h Hit) HitView {
	return HitView{
		ID: h.ID, EntryID: h.EntryID, MatchedOn: h.MatchedOn, MatchedValue: h.MatchedValue,
		PassID: h.PassID, PassNo: h.PassNo, MovementID: h.MovementID, GateID: h.GateID,
		GateName: h.GateName, GateSessionID: h.GateSession, ActionTaken: h.ActionTaken,
		OccurredAt: h.OccurredAt, ListType: h.EntryListType, Severity: h.EntrySeverity,
		EntryReason: h.EntryReason, DisplayName: h.DisplayName,
		EntryMatchType: h.MatchType, EntryMatchVal: h.MatchValue,
		ResolvedBy: h.ResolvedBy, ResolvedAt: h.ResolvedAt, Resolution: h.Resolution,
		Open: h.Open(),
	}
}

func hitViewsOf(hits []Hit) []HitView {
	views := make([]HitView, 0, len(hits))
	for _, hit := range hits {
		views = append(views, hitViewOf(hit))
	}
	return views
}

// ParseList reads the register filters: ?list_type=&severity=&status=&search=&include_stale=.
func ParseList(r *http.Request) (ListFilter, httpx.Page, error) {
	query := r.URL.Query()
	stale, err := optionalBool(query, "include_stale")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	filter := ListFilter{
		ListType: query.Get("list_type"), Severity: query.Get("severity"),
		Status: query.Get("status"), Search: strings.TrimSpace(query.Get("search")),
		IncludeStale: stale != nil && *stale,
	}
	if filter.ListType != "" && !ListType(filter.ListType).Valid() {
		return filter, httpx.Page{}, errs.BadField("list_type", "must be blocklist or watchlist")
	}
	if filter.Severity != "" {
		switch Severity(filter.Severity) {
		case SeverityLow, SeverityMedium, SeverityHigh:
		default:
			return filter, httpx.Page{}, errs.BadField("severity", "must be low, medium or high")
		}
	}
	if filter.Status != "" && !knownStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a listing status")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

// ParseHits reads the hit feed filters: ?entry_id=&list_type=&open=.
func ParseHits(r *http.Request) (HitFilter, httpx.Page, error) {
	query := r.URL.Query()
	entryID, err := optionalIDParam(query, "entry_id")
	if err != nil {
		return HitFilter{}, httpx.Page{}, err
	}
	open, err := optionalBool(query, "open")
	if err != nil {
		return HitFilter{}, httpx.Page{}, err
	}
	filter := HitFilter{
		EntryID: entryID, ListType: query.Get("list_type"),
		UnresolvedOnly: open != nil && *open,
	}
	if filter.ListType != "" && !ListType(filter.ListType).Valid() {
		return filter, httpx.Page{}, errs.BadField("list_type", "must be blocklist or watchlist")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func knownStatus(status string) bool {
	for _, candidate := range []Status{
		StatusActive, StatusExpired, StatusRevoked, StatusUnderReview,
	} {
		if string(candidate) == status {
			return true
		}
	}
	return false
}

func optionalIDParam(query url.Values, name string) (int64, error) {
	raw := query.Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errs.BadField(name, "expected a positive id")
	}
	return value, nil
}

// optionalBool refuses a value that is neither yes nor no: silently dropping ?open=maybe
// would hand back every hit and look like a filter that worked.
func optionalBool(query url.Values, name string) (*bool, error) {
	switch strings.ToLower(query.Get(name)) {
	case "":
		return nil, nil
	case "true", "1":
		yes := true
		return &yes, nil
	case "false", "0":
		no := false
		return &no, nil
	default:
		return nil, errs.BadField(name, "expected true or false")
	}
}
