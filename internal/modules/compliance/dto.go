package compliance

import (
	"net/http"
	"strings"
	"time"
)

// PolicyRequest sets one category's retention rule. An absent purge_action means null, which is
// the safest thing to do to a value that is no longer needed.
type PolicyRequest struct {
	DataCategory     string `json:"data_category" validate:"required,max=48"`
	RetentionDays    int    `json:"retention_days" validate:"required,min=1,max=3650"`
	PurgeAction      string `json:"purge_action" validate:"omitempty,oneof=null anonymise delete archive"`
	NotifyBeforeDays *int   `json:"notify_before_days" validate:"omitempty,min=0,max=90"`
	IsActive         *bool  `json:"is_active"`
}

// HoldRequest places a legal hold on one record.
type HoldRequest struct {
	EntityType string     `json:"entity_type" validate:"required,max=48"`
	EntityID   int64      `json:"entity_id" validate:"required,gt=0"`
	PassID     int64      `json:"pass_id" validate:"omitempty,gt=0"`
	CaseRef    string     `json:"case_ref" validate:"required,max=128"`
	Reason     string     `json:"reason" validate:"required,min=10,max=1024"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

// ReleaseRequest ends a hold. The reason is required because a hold that ends without a
// sentence leaves the next reviewer guessing what happened to the case.
type ReleaseRequest struct {
	Reason string `json:"reason" validate:"required,max=1024"`
}

// VerifyRequest walks the chain between two sequence numbers. With neither it walks everything
// the batch limit allows, from the beginning.
type VerifyRequest struct {
	FromSeq *uint64 `json:"from_seq"`
	ToSeq   *uint64 `json:"to_seq"`
	Note    *string `json:"note" validate:"omitempty,max=512"`
}

// PurgeRequest runs one category's policy. dry_run defaults to true: an endpoint whose default
// deletes personal data is an endpoint somebody runs by accident.
type PurgeRequest struct {
	DataCategory string `json:"data_category" validate:"required,max=48"`
	DryRun       *bool  `json:"dry_run"`
}

type PolicyView struct {
	ID               int64      `json:"id"`
	DataCategory     string     `json:"data_category"`
	RetentionDays    int        `json:"retention_days"`
	PurgeAction      string     `json:"purge_action"`
	NotifyBeforeDays *int       `json:"notify_before_days,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	IsActive         bool       `json:"is_active"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func policyView(p Policy) PolicyView {
	return PolicyView{ID: p.ID, DataCategory: p.DataCategory, RetentionDays: p.RetentionDays,
		PurgeAction: p.PurgeAction, NotifyBeforeDays: p.NotifyBeforeDays, LastRunAt: p.LastRunAt,
		IsActive: p.IsActive, UpdatedAt: p.UpdatedAt}
}

type HoldView struct {
	ID            int64      `json:"id"`
	EntityType    string     `json:"entity_type"`
	EntityID      int64      `json:"entity_id"`
	PassID        *int64     `json:"pass_id,omitempty"`
	CaseRef       string     `json:"case_ref"`
	Reason        string     `json:"reason"`
	Status        string     `json:"status"`
	PlacedBy      *int64     `json:"placed_by_user_id,omitempty"`
	PlacedAt      time.Time  `json:"placed_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	ReleasedBy    *int64     `json:"released_by_user_id,omitempty"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
	ReleaseReason *string    `json:"release_reason,omitempty"`
	Active        bool       `json:"is_active"`
}

func holdView(h Hold) HoldView {
	return HoldView{ID: h.ID, EntityType: h.EntityType, EntityID: h.EntityID, PassID: h.PassID,
		CaseRef: h.CaseRef, Reason: h.Reason, Status: h.Status, PlacedBy: h.PlacedBy,
		PlacedAt: h.PlacedAt, ExpiresAt: h.ExpiresAt, ReleasedBy: h.ReleasedBy,
		ReleasedAt: h.ReleasedAt, ReleaseReason: h.ReleaseReason, Active: h.Status == HoldActive}
}

type PurgeView struct {
	ID           int64      `json:"id"`
	DataCategory string     `json:"data_category"`
	PurgeAction  string     `json:"purge_action"`
	DryRun       bool       `json:"is_dry_run"`
	Status       string     `json:"status"`
	RowsScanned  int64      `json:"rows_scanned"`
	RowsPurged   int64      `json:"rows_purged"`
	RowsOnHold   int64      `json:"rows_skipped_on_hold"`
	CutoffAt     *time.Time `json:"cutoff_at,omitempty"`
	Error        *string    `json:"error_message,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func purgeView(r PurgeRun) PurgeView {
	return PurgeView{ID: r.ID, DataCategory: r.DataCategory, PurgeAction: r.PurgeAction,
		DryRun: r.IsDryRun, Status: r.Status, RowsScanned: r.RowsScanned,
		RowsPurged: r.RowsPurged, RowsOnHold: r.RowsOnHold, CutoffAt: r.CutoffAt,
		Error: r.ErrorMessage, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		CreatedAt: r.CreatedAt}
}

type VerificationView struct {
	ID               int64     `json:"id"`
	StreamKey        string    `json:"stream_key"`
	FromSeq          uint64    `json:"from_seq"`
	ToSeq            uint64    `json:"to_seq"`
	RowsChecked      int64     `json:"rows_checked"`
	Outcome          string    `json:"outcome"`
	FirstMismatchSeq *uint64   `json:"first_mismatch_seq,omitempty"`
	FirstMismatch    *string   `json:"first_mismatch_hash,omitempty"`
	TipHash          *string   `json:"tip_hash,omitempty"`
	DurationMS       *int      `json:"duration_ms,omitempty"`
	CheckedBy        *int64    `json:"checked_by_user_id,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
	Note             *string   `json:"note,omitempty"`
}

func verificationView(v Verification) VerificationView {
	return VerificationView{ID: v.ID, StreamKey: v.StreamKey, FromSeq: v.FromSeq,
		ToSeq: v.ToSeq, RowsChecked: v.RowsChecked, Outcome: v.Outcome,
		FirstMismatchSeq: v.FirstMismatchSeq, FirstMismatch: v.FirstMismatchHash,
		TipHash: v.TipHash, DurationMS: v.DurationMS, CheckedBy: v.CheckedBy,
		CheckedAt: v.CheckedAt, Note: v.Note}
}

func policyViews(policies []Policy) []PolicyView {
	out := make([]PolicyView, 0, len(policies))
	for _, p := range policies {
		out = append(out, policyView(p))
	}
	return out
}

func holdViews(holds []Hold) []HoldView {
	out := make([]HoldView, 0, len(holds))
	for _, h := range holds {
		out = append(out, holdView(h))
	}
	return out
}

func purgeViews(runs []PurgeRun) []PurgeView {
	out := make([]PurgeView, 0, len(runs))
	for _, run := range runs {
		out = append(out, purgeView(run))
	}
	return out
}

func verificationViews(rows []Verification) []VerificationView {
	out := make([]VerificationView, 0, len(rows))
	for _, row := range rows {
		out = append(out, verificationView(row))
	}
	return out
}

// ParseHoldStatus reads ?status= on the hold list. An unknown value is a typo worth naming, not
// a filter to ignore.
func ParseHoldStatus(r *http.Request) (string, error) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "", HoldActive, HoldReleased, HoldExpired:
		return status, nil
	default:
		return "", badHoldStatus(status)
	}
}

func badHoldStatus(status string) error {
	return errBadStatus(status)
}
