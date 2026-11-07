package scanning

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 50

// IssueTokenRequest is one badge as the desk asks for it. Everything is optional: the pass
// decides the expiry, a plain QR code is the format, and the delivery is whatever the desk does
// with the secret once it has it.
type IssueTokenRequest struct {
	Format    string     `json:"format" validate:"omitempty,oneof=qr_text qr_url numeric nfc_uid rfid_card barcode anpr_plate"`
	Delivery  string     `json:"delivery_channel" validate:"omitempty,oneof=print sms email app badge device api"`
	ExpiresAt *time.Time `json:"expires_at"`
	MaxUses   *int       `json:"max_uses" validate:"omitempty,gte=1,lte=100000"`
	SingleUse bool       `json:"is_single_use"`
}

func (req IssueTokenRequest) toRequest(passID int64) IssueRequest {
	return IssueRequest{
		PassID: passID, Format: Format(req.Format), Delivery: req.Delivery,
		ExpiresAt: req.ExpiresAt, MaxUses: req.MaxUses, SingleUse: req.SingleUse,
	}
}

// RevokeTokenRequest is the reason a badge is being taken out of circulation.
type RevokeTokenRequest struct {
	Reason string `json:"reason" validate:"required,max=255"`
}

// TokenView is a badge as the desk sees it. It carries the prefix, never the secret: the secret
// exists in one response, at issue time, and nowhere afterwards.
type TokenView struct {
	ID           int64      `json:"id"`
	PassID       int64      `json:"pass_id"`
	PassNo       string     `json:"pass_no"`
	PassStatus   string     `json:"pass_status"`
	SubjectName  string     `json:"subject_name,omitempty"`
	Prefix       string     `json:"prefix"`
	Format       string     `json:"format"`
	Delivery     string     `json:"delivery_channel,omitempty"`
	IssuedAt     time.Time  `json:"issued_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
	MaxUses      *int       `json:"max_uses,omitempty"`
	UseCount     int        `json:"use_count"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	LastGateID   *int64     `json:"last_gate_id,omitempty"`
	SingleUse    bool       `json:"is_single_use"`
	Status       string     `json:"status"`
}

func viewOfToken(t Token, now time.Time) TokenView {
	view := TokenView{
		ID: t.ID, PassID: t.PassID, PassNo: t.PassNo, PassStatus: t.PassStatus,
		Prefix: t.TokenPrefix, Format: t.Format, IssuedAt: t.IssuedAt,
		ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, MaxUses: t.MaxUses,
		UseCount: t.UseCount, LastUsedAt: t.LastUsedAt, LastGateID: t.LastGateID,
		SingleUse: t.IsSingleUse, Status: statusOf(t, now),
	}
	if t.SubjectName != nil {
		view.SubjectName = *t.SubjectName
	}
	if t.RevokeReason != nil {
		view.RevokeReason = *t.RevokeReason
	}
	if t.DeliveryChannel != nil {
		view.Delivery = *t.DeliveryChannel
	}
	return view
}

func viewsOfTokens(rows []Token, now time.Time) []TokenView {
	views := make([]TokenView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfToken(row, now))
	}
	return views
}

// statusOf is the one word a desk screen colours a row by.
func statusOf(t Token, now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && !now.Before(*t.ExpiresAt):
		return "expired"
	case t.MaxUses != nil && t.UseCount >= *t.MaxUses:
		return "used"
	default:
		return "live"
	}
}

// IssuedTokenView is the one response that carries the secret. It is a TokenView plus a token
// field, so a client reads the same shape either way and the secret is simply absent later.
type IssuedTokenView struct {
	TokenView
	Secret string `json:"token"`
}

func viewOfIssued(issued Issued, now time.Time) IssuedTokenView {
	return IssuedTokenView{TokenView: viewOfToken(issued.Token, now), Secret: issued.Secret}
}

// AttemptView is one raw scan: what the reader said, what the barrier answered, and the pass it
// was about when it was about one.
type AttemptView struct {
	ID           int64     `json:"id"`
	GateID       int64     `json:"gate_id"`
	GateName     string    `json:"gate_name"`
	DeviceID     *int64    `json:"device_id,omitempty"`
	Input        string    `json:"input"`
	Prefix       string    `json:"token_prefix,omitempty"`
	Plate        string    `json:"plate_read,omitempty"`
	Outcome      string    `json:"outcome"`
	ResponseCode string    `json:"response_code,omitempty"`
	Message      string    `json:"response_message,omitempty"`
	PassID       *int64    `json:"pass_id,omitempty"`
	PassNo       string    `json:"pass_no,omitempty"`
	SubjectName  string    `json:"subject_name,omitempty"`
	MovementID   *int64    `json:"movement_id,omitempty"`
	Direction    string    `json:"direction,omitempty"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func viewOfAttempt(a Attempt) AttemptView {
	view := AttemptView{
		ID: a.ID, GateID: a.GateID, GateName: a.GateName, DeviceID: a.DeviceID,
		Input: a.InputKind, Outcome: a.Outcome, PassID: a.MatchedPassID, PassNo: a.PassNo,
		MovementID: a.MatchedMovement, OccurredAt: a.OccurredAt,
	}
	if a.TokenPrefix != nil {
		view.Prefix = *a.TokenPrefix
	}
	if a.PlateRead != nil {
		view.Plate = *a.PlateRead
	}
	if a.ResponseCode != nil {
		view.ResponseCode = *a.ResponseCode
	}
	if a.ResponseMessage != nil {
		view.Message = *a.ResponseMessage
	}
	if a.SubjectName != nil {
		view.SubjectName = *a.SubjectName
	}
	if a.Direction != nil {
		view.Direction = *a.Direction
	}
	return view
}

func viewsOfAttempts(rows []Attempt) []AttemptView {
	views := make([]AttemptView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfAttempt(row))
	}
	return views
}

// knownOutcomes is the schema's CHECK list. A filter with a word the column cannot hold would
// answer an empty page that looks like "nothing went wrong here".
var knownOutcomes = map[string]bool{
	"granted": true, "denied": true, "warned": true, "override": true,
	"unknown_token": true, "expired": true, "revoked": true, "wrong_gate": true,
	"out_of_window": true, "already_used": true, "blocklist_hit": true,
	"hardware_fault": true,
}

// ParseAttempts reads the scan log's query string.
func ParseAttempts(r *http.Request) (AttemptFilter, httpx.Page, error) {
	query := r.URL.Query()
	gateID, err := optionalID(query, "gate_id")
	if err != nil {
		return AttemptFilter{}, httpx.Page{}, err
	}
	passID, err := optionalID(query, "pass_id")
	if err != nil {
		return AttemptFilter{}, httpx.Page{}, err
	}
	filter := AttemptFilter{
		GateID: gateID, PassID: passID, Outcome: query.Get("outcome"),
		Input: query.Get("input"),
	}
	if filter.Outcome != "" && !knownOutcomes[filter.Outcome] {
		return filter, httpx.Page{}, errs.BadField("outcome", filter.Outcome+" is not a scan outcome")
	}
	if filter.Input != "" && !Input(filter.Input).valid() {
		return filter, httpx.Page{}, errs.BadField("input", filter.Input+" is not an input kind")
	}
	page, err := httpx.ParsePage(r, defaultPage, 200)
	return filter, page, err
}

func optionalID(query url.Values, name string) (int64, error) {
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
