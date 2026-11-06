// Package blocklist is the screening list every barrier consults before it opens: people and
// vehicles a tenant has said no to, and the ones they want flagged rather than refused.
package blocklist

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
)

// ModuleKey is the switch this module answers to. Switched off, the guard at the barrier
// gets no verdict at all and the gate decides on Core's own rules, which is the whole point
// of a site that screens some buildings and not others.
const ModuleKey = "blocklist_watchlist"

const aggregateType = "blocklist_entry"

type ListType string

const (
	// ListBlock refuses the scan. ListWatch lets it through and tells somebody quietly,
	// because a person you only want to see is not a person you want turned away.
	ListBlock ListType = "blocklist"
	ListWatch ListType = "watchlist"
)

type EntityType string

const (
	EntityPerson  EntityType = "person"
	EntityVehicle EntityType = "vehicle"
	EntityCompany EntityType = "company"
	EntityDevice  EntityType = "device"
)

type MatchType string

// The identifier a listing is keyed on. These are the values chk_blocklist_match allows, and
// every one of them is normalised the same way before it is hashed, so a phone typed with a
// plus and a space matches the same number dialled as digits.
const (
	MatchIDNumber  MatchType = "id_number"
	MatchPhone     MatchType = "phone"
	MatchPlate     MatchType = "plate"
	MatchEmail     MatchType = "email"
	MatchNameHash  MatchType = "name_hash"
	MatchBadgeNo   MatchType = "badge_no"
	MatchCardNo    MatchType = "card_no"
	MatchPhotoHash MatchType = "photo_hash"
)

type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

type Status string

const (
	StatusActive      Status = "active"
	StatusExpired     Status = "expired"
	StatusRevoked     Status = "revoked"
	StatusUnderReview Status = "under_review"
)

// Open reports whether the entry can still be hit: it has to be live and still inside its
// own expiry. under_review counts as live because a listing somebody is arguing about is a
// listing the gate should still obey until it is formally taken off.
func (e Entry) Open() bool {
	return e.Status == string(StatusActive) || e.Status == string(StatusUnderReview)
}

func (e Entry) ExpiredAt(now time.Time) bool {
	return e.ExpiresAt != nil && !e.ExpiresAt.After(now)
}

func (e Entry) Blocked() bool { return ListType(e.ListType) == ListBlock }

func (t ListType) Valid() bool { return t == ListBlock || t == ListWatch }

func (m MatchType) Valid() bool {
	switch m {
	case MatchIDNumber, MatchPhone, MatchPlate, MatchEmail, MatchNameHash, MatchBadgeNo,
		MatchCardNo, MatchPhotoHash:
		return true
	default:
		return false
	}
}

func (e EntityType) Valid() bool {
	switch e {
	case EntityPerson, EntityVehicle, EntityCompany, EntityDevice:
		return true
	default:
		return false
	}
}

// Entry is one listing. The comparison key is match_hash, never match_value: the raw value
// stays readable for the human who has to decide whether two lookalikes are the same person,
// and the hash is what an index can find without a range scan on somebody's phone number.
type Entry struct {
	ID           int64      `db:"id"`
	TenantID     int64      `db:"tenant_id"`
	ListType     string     `db:"list_type"`
	EntityType   string     `db:"entity_type"`
	MatchType    string     `db:"match_type"`
	MatchValue   string     `db:"match_value"`
	DisplayName  *string    `db:"display_name"`
	Organisation *string    `db:"organisation"`
	PhotoPath    *string    `db:"photo_path"`
	Reason       string     `db:"reason"`
	Severity     string     `db:"severity"`
	Source       *string    `db:"source"`
	EvidenceRef  *string    `db:"evidence_ref"`
	IncidentPass *int64     `db:"incident_pass_id"`
	AlertRoleKey *string    `db:"alert_supervisor_role_key"`
	HitCount     int        `db:"hit_count"`
	LastHitAt    *time.Time `db:"last_hit_at"`
	Status       string     `db:"status"`
	AddedBy      *int64     `db:"added_by_user_id"`
	AddedAt      time.Time  `db:"added_at"`
	ExpiresAt    *time.Time `db:"expires_at"`
	RevokedAt    *time.Time `db:"revoked_at"`
	RevokeReason *string    `db:"revoke_reason"`
	Notes        *string    `db:"notes"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

// Hit is one time the list was consulted and something on it matched.
type Hit struct {
	ID           int64      `db:"id"`
	TenantID     int64      `db:"tenant_id"`
	EntryID      int64      `db:"entry_id"`
	MatchedOn    string     `db:"matched_on"`
	MatchedValue *string    `db:"matched_value"`
	PassID       *int64     `db:"pass_id"`
	MovementID   *int64     `db:"movement_id"`
	GateID       *int64     `db:"gate_id"`
	GateSession  *int64     `db:"gate_session_id"`
	ActionTaken  string     `db:"action_taken"`
	OccurredAt   time.Time  `db:"occurred_at"`
	ResolvedBy   *int64     `db:"resolved_by_user_id"`
	ResolvedAt   *time.Time `db:"resolved_at"`
	Resolution   *string    `db:"resolution"`
	// Joined for the feed: a hit a supervisor cannot name is a number, not a lead.
	EntryListType string  `db:"entry_list_type"`
	EntrySeverity string  `db:"entry_severity"`
	EntryReason   string  `db:"entry_reason"`
	DisplayName   *string `db:"display_name"`
	MatchType     string  `db:"entry_match_type"`
	MatchValue    string  `db:"entry_match_value"`
	PassNo        *string `db:"pass_no"`
	GateName      *string `db:"gate_name"`
}

func (h Hit) Open() bool { return h.ResolvedAt == nil }

// New is one listing as the desk files it. The caller types the value the way a person
// would say it; normalisation and hashing are this module's business.
type New struct {
	TenantID     int64
	ListType     ListType
	EntityType   EntityType
	MatchType    MatchType
	MatchValue   string
	DisplayName  string
	Organisation string
	Reason       string
	Severity     Severity
	Source       string
	EvidenceRef  string
	IncidentPass int64
	AlertRoleKey string
	ExpiresAt    *time.Time
	AddedBy      int64
	Notes        string
}

// Normalised is the stored form of the match value, and the only form the hash is ever taken
// of. A listing typed twice with different punctuation is one listing, not two, and that is
// the difference between a guard seeing one ban and a desk seeing four.
func (in New) Normalised() string { return normalize(in.MatchType, in.MatchValue) }

func (in New) Hash() string { return hashOf(in.Normalised()) }

func normalize(kind MatchType, value string) string {
	switch kind {
	case MatchPhone, MatchIDNumber, MatchBadgeNo, MatchCardNo:
		return keep(value, unicode.IsDigit)
	case MatchPlate:
		return strings.ToUpper(keep(value, unicode.IsLetter, unicode.IsDigit))
	case MatchNameHash, MatchPhotoHash:
		return collapse(strings.ToUpper(keep(value, unicode.IsLetter, unicode.IsDigit,
			unicode.IsSpace)))
	default:
		return collapse(strings.ToUpper(strings.TrimSpace(value)))
	}
}

// keep drops everything the match type has no use for. A phone number with a plus, a space
// and a bracket is the same number as the digits alone, and a plate with a dash between the
// groups is the same plate.
func keep(value string, allowed ...func(rune) bool) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		for _, test := range allowed {
			if test(r) {
				out = append(out, r)
				break
			}
		}
	}
	return string(out)
}

// collapse squeezes runs of spaces into one and trims both ends, which is what lets
// "MARY  NKAISAI " and "Mary Nkaisai" hash to the same listing.
func collapse(value string) string {
	fields := strings.Fields(value)
	return strings.Join(fields, " ")
}

func hashOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
