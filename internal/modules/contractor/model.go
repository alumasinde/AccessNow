// Package contractor is the contractor and recurring-access slice: the firms, the people they
// send, the documents that let them in, and the long pass that hangs off Core's spine.
//
// The pass itself is Core's - one row in passes, one number from the tenant's contractor
// series - and this module owns everything around it: the validity range, the weekly windows,
// the badge and the paperwork. A document that has expired is what makes a pass unusable, and
// that refusal is answered at the barrier through the eligibility seam rather than by rewriting
// the pass row: renewing the paperwork puts the same pass back to work without a second
// issuance, and the number the guard has already seen on a badge keeps meaning one thing.
package contractor

import (
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
)

// ModuleKey is the catalogue row this module switches on and off with.
const ModuleKey = "contractor_access"

// numberFormat is the tenant's contractor series, CTR-{YYYY}{SEQ} until the tenant edits it.
// aggregateType names this module's rows on the outbox, so a subscriber never has to guess
// whether an id points at a contractor pass or at Core's pass.
const (
	numberFormat  = "contractor"
	aggregateType = "contractor_pass"
)

// The pass types the catalogue seeds for this module. The desk may not issue another module's
// type through this surface: a visitor pass filed here would be a visit with no visit row.
const (
	PassTypeRecurring = "contractor_recurring"
	PassTypeDay       = "contractor_day"
	PassTypeService   = "service_visit"
)

func validPassType(key string) bool {
	switch key {
	case PassTypeRecurring, PassTypeDay, PassTypeService:
		return true
	}
	return false
}

// Status mirrors chk_contractor_passes_status.
//
// blocked is a pass the barrier must refuse while it stays on the books: an expired document
// stops the person coming in, but it does not throw away the pass, the badge or the number.
type Status string

const (
	StatusPending Status = "pending"
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusBlocked Status = "blocked"
	StatusRevoked Status = "revoked"
)

func (s Status) valid() bool {
	switch s {
	case StatusPending, StatusActive, StatusExpired, StatusBlocked, StatusRevoked:
		return true
	}
	return false
}

// BlockSource says who stopped a pass, which is the difference between a block the sweep may
// lift by itself and one a person has to. It is the column that keeps a renewed safety
// induction from silently un-suspending somebody a supervisor blocked on purpose.
type BlockSource string

const (
	BlockByDocument BlockSource = "document"
	BlockByDesk     BlockSource = "manual"
)

// PersonStatus mirrors chk_contractors_status, FirmStatus chk_contractor_org_status.
const (
	PersonPending   = "pending"
	PersonActive    = "active"
	PersonSuspended = "suspended"
	PersonExpired   = "expired"
	PersonArchived  = "archived"

	FirmPending   = "pending"
	FirmActive    = "active"
	FirmSuspended = "suspended"
	FirmArchived  = "archived"
)

func validPersonStatus(status string) bool {
	switch status {
	case PersonPending, PersonActive, PersonSuspended, PersonExpired, PersonArchived:
		return true
	}
	return false
}

func validFirmStatus(status string) bool {
	switch status {
	case FirmPending, FirmActive, FirmSuspended, FirmArchived:
		return true
	}
	return false
}

// DocumentStatus mirrors chk_contractor_documents_status.
type DocumentStatus string

const (
	DocPending   DocumentStatus = "pending"
	DocValid     DocumentStatus = "valid"
	DocExpiring  DocumentStatus = "expiring"
	DocExpired   DocumentStatus = "expired"
	DocRejected  DocumentStatus = "rejected"
	DocWithdrawn DocumentStatus = "withdrawn"
)

func (s DocumentStatus) valid() bool {
	switch s {
	case DocPending, DocValid, DocExpiring, DocExpired, DocRejected, DocWithdrawn:
		return true
	}
	return false
}

// Counts reports a document that is still on file for the purpose of expiry. A rejected or
// withdrawn document is paperwork the tenant has already refused, so its expiry date says
// nothing about the contractor.
func (s DocumentStatus) Counts() bool {
	switch s {
	case DocPending, DocValid, DocExpiring, DocExpired:
		return true
	}
	return false
}

// Organization is one firm the tenant buys work from.
type Organization struct {
	ID             int64      `db:"id"`
	TenantID       int64      `db:"tenant_id"`
	Name           string     `db:"name"`
	ShortName      *string    `db:"short_name"`
	RegistrationNo *string    `db:"registration_no"`
	TaxID          *string    `db:"tax_id"`
	Trade          *string    `db:"trade"`
	ContactName    *string    `db:"contact_name"`
	ContactEmail   *string    `db:"contact_email"`
	ContactPhone   *string    `db:"contact_phone"`
	Address        *string    `db:"address"`
	ContractRef    *string    `db:"contract_ref"`
	ContractStart  *time.Time `db:"contract_start"`
	ContractEnd    *time.Time `db:"contract_end"`
	Status         string     `db:"status"`
	Notes          *string    `db:"notes"`
	CreatedAt      time.Time  `db:"created_at"`
}

// Live reports a firm whose people may still be issued passes.
func (o Organization) Live() bool { return o.Status == FirmActive }

// Contractor is one person a firm sends. id_number and photo_path are this module's own
// columns and both carry a purged_at companion, so the retention job can blank them and still
// prove when it did.
type Contractor struct {
	ID                  int64      `db:"id"`
	TenantID            int64      `db:"tenant_id"`
	OrganizationID      *int64     `db:"organization_id"`
	FirstName           string     `db:"first_name"`
	LastName            string     `db:"last_name"`
	Phone               *string    `db:"phone"`
	Email               *string    `db:"email"`
	Nationality         *string    `db:"nationality"`
	IDType              *string    `db:"id_type"`
	IDNumber            *string    `db:"id_number"`
	IDNumberPurgedAt    *time.Time `db:"id_number_purged_at"`
	PhotoPath           *string    `db:"photo_path"`
	PhotoPurgedAt       *time.Time `db:"photo_purged_at"`
	BadgeNo             *string    `db:"badge_no"`
	AccessCardNo        *string    `db:"access_card_no"`
	Trade               *string    `db:"trade"`
	SponsorUserID       *int64     `db:"sponsor_user_id"`
	SponsorDepartmentID *int64     `db:"sponsor_department_id"`
	Status              string     `db:"status"`
	DocumentsVerifiedAt *time.Time `db:"documents_verified_at"`
	Notes               *string    `db:"notes"`
	ExternalRef         *string    `db:"external_ref"`
	CreatedAt           time.Time  `db:"created_at"`

	// OrganizationName is joined for the register, because a desk reads "Delta Works" and not
	// a foreign key.
	OrganizationName string `db:"organization_name"`
}

func (c Contractor) Name() string { return strings.TrimSpace(c.FirstName + " " + c.LastName) }

// Cleared reports somebody who may be issued a pass: active, or pending while the desk
// finishes their paperwork. Suspended, expired and archived people may not.
func (c Contractor) Cleared() bool {
	return c.Status == PersonActive || c.Status == PersonPending
}

// DocumentType is the shared catalogue row a document is filed under.
type DocumentType struct {
	ID                 int64   `db:"id"`
	TypeKey            string  `db:"type_key"`
	Name               string  `db:"name"`
	HolderType         string  `db:"holder_type"`
	AppliesToModule    *string `db:"applies_to_module"`
	IsMandatory        bool    `db:"is_mandatory"`
	ValidityMonths     *int    `db:"validity_months"`
	RequiresValidation bool    `db:"requires_verification"`
	ReminderDaysBefore int     `db:"reminder_days_before"`
	Status             string  `db:"status"`
}

// Document is one piece of paperwork behind one contractor. expires_on is what the barrier
// ultimately reads: a mandatory document past that date stops every pass its holder has.
type Document struct {
	ID               int64      `db:"id"`
	TenantID         int64      `db:"tenant_id"`
	ContractorID     int64      `db:"contractor_id"`
	TypeKey          string     `db:"document_type_key"`
	DocumentNo       *string    `db:"document_no"`
	IssuingAuthority *string    `db:"issuing_authority"`
	IssuedAt         *time.Time `db:"issued_at"`
	ValidFrom        *time.Time `db:"valid_from"`
	ExpiresOn        *time.Time `db:"expires_on"`
	AttachmentPath   *string    `db:"attachment_path"`
	Status           string     `db:"status"`
	VerifiedByUserID *int64     `db:"verified_by_user_id"`
	VerifiedAt       *time.Time `db:"verified_at"`
	RejectionReason  *string    `db:"rejection_reason"`
	ReminderSentAt   *time.Time `db:"reminder_sent_at"`
	Notes            *string    `db:"notes"`
	CreatedAt        time.Time  `db:"created_at"`

	TypeName    string `db:"type_name"`
	Mandatory   bool   `db:"is_mandatory"`
	ReminderDay int    `db:"reminder_days_before"`
}

// Expired reports a mandatory document whose last valid day has passed.
func (d Document) Expired(today time.Time) bool {
	if !d.Mandatory || d.ExpiresOn == nil || !DocumentStatus(d.Status).Counts() {
		return false
	}
	return day(*d.ExpiresOn).Before(today)
}

// Window is one weekly slot of a recurring pass. day_of_week is ISO 1..7 with 0 meaning every
// day, and gate_id empty means any gate the pass may use.
type Window struct {
	ID              int64  `db:"id"`
	TenantID        int64  `db:"tenant_id"`
	ContractorPass  int64  `db:"contractor_pass_id"`
	GateID          *int64 `db:"gate_id"`
	DayOfWeek       int    `db:"day_of_week"`
	StartsAt        string `db:"starts_at"`
	ClosesAt        string `db:"closes_at"`
	CrossesMidnight bool   `db:"crosses_midnight"`
}

// Covers reports whether this window includes the instant and, when the pass names one, the
// gate the guard is standing at. The time handed in must already be the tenant's wall clock:
// a window that says "22:00" means ten at night where the site is.
//
// A window that closes at or before it opens crosses midnight, whether or not the flag was
// set by the desk: 22:00 to 06:00 is one shift, and reading it as a negative window would make
// every night contractor late.
func (w Window) Covers(at time.Time, gateID int64) bool {
	if w.GateID != nil && *w.GateID > 0 && gateID > 0 && *w.GateID != gateID {
		return false
	}
	opens, openOK := Clock(w.StartsAt)
	closes, closeOK := Clock(w.ClosesAt)
	if !openOK || !closeOK {
		return false
	}
	minute := at.Hour()*60 + at.Minute()
	today := ISOWeekday(at.Weekday())

	if w.CrossesMidnight || closes <= opens {
		if minute >= opens {
			return w.names(today)
		}
		return minute < closes && w.names(previousDay(today))
	}
	return w.names(today) && minute >= opens && minute < closes
}

// names reports whether this window is on duty for a weekday. Zero is every day, which is the
// column's own meaning.
func (w Window) names(weekday int) bool {
	return w.DayOfWeek == 0 || w.DayOfWeek == weekday
}

// Schedule is a pass's whole week. No rows at all means any hour of any day inside the
// validity range, which is what a contractor with no rota has always had.
type Schedule []Window

func (s Schedule) Permits(at time.Time, gateID int64) bool {
	if len(s) == 0 {
		return true
	}
	for _, window := range s {
		if window.Covers(at, gateID) {
			return true
		}
	}
	return false
}

// Pass is one contractor pass: the detail row, the Core pass it hangs off, and the person it
// belongs to. Nothing here duplicates Core's status; the two are shown side by side because a
// register that hides the disagreement is the register that cannot be reconciled.
type Pass struct {
	ID                 int64      `db:"id"`
	TenantID           int64      `db:"tenant_id"`
	PassID             int64      `db:"pass_id"`
	ContractorID       int64      `db:"contractor_id"`
	OrganizationID     *int64     `db:"organization_id"`
	SiteID             int64      `db:"site_id"`
	BadgeNo            *string    `db:"badge_no"`
	IsMultiEntry       bool       `db:"is_multi_entry"`
	ValidFrom          time.Time  `db:"valid_from"`
	ValidTo            time.Time  `db:"valid_to"`
	WorkOrderRef       *string    `db:"work_order_ref"`
	InductionCompleted *time.Time `db:"induction_completed_at"`
	InductionDueOn     *time.Time `db:"induction_due_on"`
	EscortRequired     bool       `db:"escort_required"`
	AccessZone         *string    `db:"access_zone"`
	BlockedReason      *string    `db:"blocked_reason"`
	BlockSource        *string    `db:"blocked_source"`
	BlockedAt          *time.Time `db:"blocked_at"`
	Status             string     `db:"status"`
	Notes              *string    `db:"notes"`
	CreatedAt          time.Time  `db:"created_at"`
	PassNo             string     `db:"pass_no"`
	PassStatus         string     `db:"pass_status"`
	EntryCount         int        `db:"entry_count"`
	ContractorName     string     `db:"contractor_name"`
	ContractorPhone    *string    `db:"contractor_phone"`
	OrganizationName   string     `db:"organization_name"`

	// Windows is loaded by the read that needs the rota and left empty elsewhere, so a list
	// of five hundred passes is not five hundred extra queries.
	Windows Schedule `db:"-"`
}

// Decided reports the one state an approval click belongs to.
func (p Pass) Decided() bool { return p.Status != string(StatusPending) }

// Open reports a pass that is still on the books, blocked or not: a blocked contractor is
// somebody a supervisor may still want to see in the register.
func (p Pass) Open() bool {
	return p.Status == string(StatusActive) || p.Status == string(StatusBlocked)
}

// ValidOn reports whether a calendar day is inside the pass's range. Dates are compared as
// calendar days in the tenant's timezone, because a pass that says "valid to 30 November"
// includes the whole of the 30th wherever the site is.
func (p Pass) ValidOn(today time.Time) bool {
	return !today.Before(day(p.ValidFrom)) && !today.After(day(p.ValidTo))
}

// Permits is the whole rest of the question - range, then rota - asked with the tenant's clock
// in the caller's hand.
func (p Pass) Permits(at time.Time, gateID int64, today time.Time) bool {
	return p.ValidOn(today) && p.Windows.Permits(at, gateID)
}

// New is one firm as the desk files it.
type NewOrganization struct {
	TenantID       int64
	Name           string
	ShortName      string
	RegistrationNo string
	TaxID          string
	Trade          string
	ContactName    string
	ContactEmail   string
	ContactPhone   string
	Address        string
	ContractRef    string
	ContractStart  *time.Time
	ContractEnd    *time.Time
	Status         string
	Notes          string
}

// NewContractor is one person as the desk files them.
type NewContractor struct {
	TenantID            int64
	OrganizationID      int64
	FirstName           string
	LastName            string
	Phone               string
	Email               string
	Nationality         string
	IDType              string
	IDNumber            string
	BadgeNo             string
	AccessCardNo        string
	Trade               string
	SponsorUserID       int64
	SponsorDepartmentID int64
	Status              string
	Notes               string
	ExternalRef         string
	CreatedBy           int64
}

// NewDocument is one piece of paperwork as it is uploaded.
type NewDocument struct {
	TenantID         int64
	ContractorID     int64
	TypeKey          string
	DocumentNo       string
	IssuingAuthority string
	IssuedAt         *time.Time
	ValidFrom        *time.Time
	ExpiresOn        *time.Time
	AttachmentPath   string
	Status           DocumentStatus
	Notes            string
}

// NewPass is the detail row once Core has issued the pass behind it.
type NewPass struct {
	TenantID           int64
	PassID             int64
	ContractorID       int64
	OrganizationID     int64
	SiteID             int64
	BadgeNo            string
	IsMultiEntry       bool
	ValidFrom          time.Time
	ValidTo            time.Time
	WorkOrderRef       string
	InductionCompleted *time.Time
	InductionDueOn     *time.Time
	EscortRequired     bool
	AccessZone         string
	Status             Status
	Notes              string
}

// Clock reads a TIME column - "22:30:00" - into minutes past midnight. Both engines hand a
// TIME back as text, and a value nobody can parse is a window that must not open.
func Clock(value string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	if len(parts) == 3 {
		second, err := strconv.Atoi(parts[2])
		if err != nil || second < 0 || second > 59 {
			return 0, false
		}
	}
	return hour*60 + minute, true
}

// NormaliseClock is what the desk's "22:00" is stored as, and refuses anything a TIME column
// would silently round.
func NormaliseClock(field, value string) (string, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", errs.BadField(field, "expected HH:MM")
	}
	if _, ok := Clock(value); !ok {
		return "", errs.BadField(field, "not a time of day")
	}
	hour := strings.Split(value, ":")[0]
	minute := parts[1]
	for len(hour) < 2 {
		hour = "0" + hour
	}
	for len(minute) < 2 {
		minute = "0" + minute
	}
	return hour + ":" + minute + ":00", nil
}

// ISOWeekday numbers Monday as 1 and Sunday as 7, the way chk_contractor_pass_windows_day
// does. Go's own numbering starts on Sunday at zero, and a rota that is off by one is a
// contractor turned away on the wrong morning.
func ISOWeekday(day time.Weekday) int {
	if day == time.Sunday {
		return 7
	}
	return int(day)
}

func previousDay(weekday int) int {
	if weekday == 1 {
		return 7
	}
	return weekday - 1
}

// day is a calendar day held at UTC midnight, which is how a DATE column reads back. Every
// comparison in this package is between those, so "valid to the 30th" means the whole 30th.
func day(value time.Time) time.Time {
	year, month, date := value.Date()
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}

// StartOfDay and EndOfDay turn a calendar date into the instants a Core pass must cover. They
// take the tenant's location because a Nairobi site that says "from the 1st" means from
// midnight in Nairobi, not from midnight UTC.
func StartOfDay(value time.Time, loc *time.Location) time.Time {
	year, month, date := value.Date()
	return time.Date(year, month, date, 0, 0, 0, 0, loc).UTC()
}

func EndOfDay(value time.Time, loc *time.Location) time.Time {
	year, month, date := value.Date()
	return time.Date(year, month, date, 23, 59, 59, 999_000_000, loc).UTC()
}
