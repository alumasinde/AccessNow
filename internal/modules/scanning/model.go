package scanning

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
)

// ModuleKey is the tenant's switch for this surface.
const ModuleKey = "qr_scanning"

// aggregateType names this module's rows on the outbox. Tokens are not announced today; the
// constant is here so the day one is, it does not have to be invented in a hurry.
const aggregateType = "pass_token"

// tokenBytes is 128 bits, which is the length the design asks for. A shorter secret is one a
// camera can guess; a longer one does not fit the printed badge.
const tokenBytes = 16

// prefixLength is how much of the secret a support agent may be read out over the phone. It is
// long enough to tell two badges apart in a stack and far too short to guess the rest.
const prefixLength = 8

// A token's format is where it is printed. It never changes what the barrier does - the token
// is the token - it changes what the desk hands the person holding it.
type Format string

const (
	FormatQRText  Format = "qr_text"
	FormatQRURL   Format = "qr_url"
	FormatNumeric Format = "numeric"
	FormatNFC     Format = "nfc_uid"
	FormatRFID    Format = "rfid_card"
	FormatBarcode Format = "barcode"
	FormatANPR    Format = "anpr_plate"
)

func (f Format) valid() bool {
	switch f {
	case FormatQRText, FormatQRURL, FormatNumeric, FormatNFC, FormatRFID, FormatBarcode,
		FormatANPR:
		return true
	}
	return false
}

// Input is what kind of reader spoke. It mirrors chk_scan_attempts_input.
type Input string

const (
	InputQR      Input = "qr"
	InputBarcode Input = "barcode"
	InputRFID    Input = "rfid"
	InputNFC     Input = "nfc"
	InputPlate   Input = "plate"
	InputManual  Input = "manual"
	InputFace    Input = "face"
	InputOther   Input = "other"
)

func (i Input) valid() bool {
	switch i {
	case InputQR, InputBarcode, InputRFID, InputNFC, InputPlate, InputManual, InputFace,
		InputOther:
		return true
	}
	return false
}

// inputOf keeps the scan log's vocabulary closed. An unknown kind is a device bug and is filed
// as "other" rather than as a value the CHECK constraint would refuse.
func inputOf(value string) Input {
	candidate := Input(strings.ToLower(strings.TrimSpace(value)))
	if candidate == "" {
		return InputQR
	}
	if !candidate.valid() {
		return InputOther
	}
	return candidate
}

// Token is one issued secret. The secret itself is not in this struct and never leaves the
// issue call: the row holds a SHA-256 of it, the first characters for a support agent, and the
// bookkeeping that says whether it may still be used.
type Token struct {
	ID              int64      `db:"id"`
	TenantID        int64      `db:"tenant_id"`
	PassID          int64      `db:"pass_id"`
	TokenHash       string     `db:"token_hash"`
	TokenPrefix     string     `db:"token_prefix"`
	Format          string     `db:"format"`
	IssuedAt        time.Time  `db:"issued_at"`
	ExpiresAt       *time.Time `db:"expires_at"`
	RevokedAt       *time.Time `db:"revoked_at"`
	RevokeReason    *string    `db:"revoke_reason"`
	MaxUses         *int       `db:"max_uses"`
	UseCount        int        `db:"use_count"`
	LastUsedAt      *time.Time `db:"last_used_at"`
	LastGateID      *int64     `db:"last_gate_id"`
	IsSingleUse     bool       `db:"is_single_use"`
	IssuedByUserID  *int64     `db:"issued_by_user_id"`
	DeliveryChannel *string    `db:"delivery_channel"`

	// The pass is joined in because a token row on its own is a hash, and a desk screen showing
	// a hash is a desk screen nobody can use.
	PassNo      string  `db:"pass_no"`
	PassStatus  string  `db:"pass_status"`
	SubjectName *string `db:"subject_name"`
}

// Live reports a token that may still open a barrier at this instant.
func (t Token) Live(now time.Time) bool {
	return t.Refusal(now) == ""
}

// Refusal is the one sentence the gate gives for a token that cannot be honoured, or an empty
// string when it can. The order matters: a revoked badge is revoked whether or not it has also
// run out, because "ask the desk" is the answer either way.
func (t Token) Refusal(now time.Time) string {
	if t.RevokedAt != nil {
		if t.RevokeReason != nil && *t.RevokeReason != "" {
			return "the token was revoked: " + *t.RevokeReason
		}
		return "the token was revoked"
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return "the token has expired"
	}
	if t.MaxUses != nil && t.UseCount >= *t.MaxUses {
		return "the token has already been used"
	}
	return ""
}

// Attempt is one row of the raw scan log: what a reader said, what the gate answered, and which
// pass it was about when it was about one at all.
type Attempt struct {
	ID              int64     `db:"id"`
	TenantID        int64     `db:"tenant_id"`
	SiteID          *int64    `db:"site_id"`
	GateID          int64     `db:"gate_id"`
	DeviceID        *int64    `db:"device_id"`
	GateSessionID   *int64    `db:"gate_session_id"`
	InputKind       string    `db:"input_kind"`
	TokenHash       *string   `db:"token_hash"`
	TokenPrefix     *string   `db:"token_prefix"`
	PlateRead       *string   `db:"plate_read"`
	Outcome         string    `db:"outcome"`
	ResponseCode    *string   `db:"response_code"`
	ResponseMessage *string   `db:"response_message"`
	MatchedPassID   *int64    `db:"matched_pass_id"`
	MatchedTokenID  *int64    `db:"matched_token_id"`
	MatchedMovement *int64    `db:"matched_movement_id"`
	Direction       *string   `db:"direction_requested"`
	LatencyMS       *int      `db:"latency_ms"`
	OccurredAt      time.Time `db:"occurred_at"`
	IdempotencyKey  *string   `db:"idempotency_key"`
	CreatedAt       time.Time `db:"created_at"`

	GateName    string  `db:"gate_name"`
	PassNo      string  `db:"pass_no"`
	SubjectName *string `db:"subject_name"`
}

// Issued pairs the row with the one copy of the secret the caller will ever see.
type Issued struct {
	Token  Token
	Secret string
}

// IssueRequest is one token as the desk or an approval asks for it.
type IssueRequest struct {
	PassID    int64
	Format    Format
	Delivery  string
	ExpiresAt *time.Time
	MaxUses   *int
	SingleUse bool
}

func (in IssueRequest) validate() error {
	if in.Format != "" && !in.Format.valid() {
		return errs.BadField("format", string(in.Format)+" is not a token format")
	}
	if in.Delivery != "" {
		switch in.Delivery {
		case "print", "sms", "email", "app", "badge", "device", "api":
		default:
			return errs.BadField("delivery_channel", in.Delivery+" is not a delivery channel")
		}
	}
	if in.MaxUses != nil && *in.MaxUses < 1 {
		return errs.BadField("max_uses", "a token that may be used zero times is not a token")
	}
	return nil
}

// formatOf is the format the desk asked for, or a plain QR code when it did not care.
func formatOf(format Format) Format {
	if format == "" {
		return FormatQRText
	}
	return format
}

// NewSecret returns a 128-bit token and its printable form. base64url would be shorter, but a
// badge is read by cheap scanners that disagree about case and about '+', so the secret is hex.
func NewSecret() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate scan token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// PrefixOf is the first characters of the secret, which is all a support agent ever needs to
// confirm they are looking at the right badge.
func PrefixOf(secret string) string {
	if len(secret) <= prefixLength {
		return secret
	}
	return secret[:prefixLength]
}
