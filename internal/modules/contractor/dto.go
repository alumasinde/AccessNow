package contractor

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

// OrganizationRequest is one firm as the desk files it. Everything but the name is optional:
// the tenant's contract reference may live in an ERP, and a firm with no contract dates is a
// firm nobody has typed the dates for, not one that may not come on site.
type OrganizationRequest struct {
	Name           string     `json:"name" validate:"required,max=255"`
	ShortName      string     `json:"short_name" validate:"omitempty,max=64"`
	RegistrationNo string     `json:"registration_no" validate:"omitempty,max=64"`
	TaxID          string     `json:"tax_id" validate:"omitempty,max=64"`
	Trade          string     `json:"trade" validate:"omitempty,max=128"`
	ContactName    string     `json:"contact_name" validate:"omitempty,max=128"`
	ContactEmail   string     `json:"contact_email" validate:"omitempty,email,max=255"`
	ContactPhone   string     `json:"contact_phone" validate:"omitempty,max=32"`
	Address        string     `json:"address" validate:"omitempty,max=255"`
	ContractRef    string     `json:"contract_ref" validate:"omitempty,max=64"`
	ContractStart  *time.Time `json:"contract_start"`
	ContractEnd    *time.Time `json:"contract_end"`
	Status         string     `json:"status" validate:"omitempty,oneof=active pending suspended archived"`
	Notes          string     `json:"notes" validate:"omitempty,max=2000"`
}

func (req OrganizationRequest) toNew() NewOrganization {
	return NewOrganization{
		Name: req.Name, ShortName: req.ShortName, RegistrationNo: req.RegistrationNo,
		TaxID: req.TaxID, Trade: req.Trade, ContactName: req.ContactName,
		ContactEmail: req.ContactEmail, ContactPhone: req.ContactPhone, Address: req.Address,
		ContractRef: req.ContractRef, ContractStart: req.ContractStart,
		ContractEnd: req.ContractEnd, Status: req.Status, Notes: req.Notes,
	}
}

// ContractorRequest is one person on a firm's list.
type ContractorRequest struct {
	OrganizationID      int64  `json:"organization_id" validate:"omitempty,gt=0"`
	FirstName           string `json:"first_name" validate:"required,max=128"`
	LastName            string `json:"last_name" validate:"required,max=128"`
	Phone               string `json:"phone" validate:"omitempty,max=32"`
	Email               string `json:"email" validate:"omitempty,email,max=255"`
	Nationality         string `json:"nationality" validate:"omitempty,len=2"`
	IDType              string `json:"id_type" validate:"omitempty,oneof=national_id passport drivers_licence work_permit other"`
	IDNumber            string `json:"id_number" validate:"omitempty,max=64"`
	BadgeNo             string `json:"badge_no" validate:"omitempty,max=64"`
	AccessCardNo        string `json:"access_card_no" validate:"omitempty,max=64"`
	Trade               string `json:"trade" validate:"omitempty,max=128"`
	SponsorUserID       int64  `json:"sponsor_user_id" validate:"omitempty,gt=0"`
	SponsorDepartmentID int64  `json:"sponsor_department_id" validate:"omitempty,gt=0"`
	Status              string `json:"status" validate:"omitempty,oneof=pending active suspended expired archived"`
	Notes               string `json:"notes" validate:"omitempty,max=2000"`
	ExternalRef         string `json:"external_ref" validate:"omitempty,max=128"`
}

func (req ContractorRequest) toNew() NewContractor {
	return NewContractor{
		OrganizationID: req.OrganizationID, FirstName: req.FirstName, LastName: req.LastName,
		Phone: req.Phone, Email: req.Email, Nationality: req.Nationality,
		IDType: req.IDType, IDNumber: req.IDNumber, BadgeNo: req.BadgeNo,
		AccessCardNo: req.AccessCardNo, Trade: req.Trade, SponsorUserID: req.SponsorUserID,
		SponsorDepartmentID: req.SponsorDepartmentID, Status: req.Status, Notes: req.Notes,
		ExternalRef: req.ExternalRef,
	}
}

// DocumentRequest is one piece of paperwork. The attachment is a path the file store already
// owns; nothing here uploads bytes, because the API keeps the bytes and the retention job
// somewhere it can purge them.
type DocumentRequest struct {
	DocumentTypeKey  string     `json:"document_type_key" validate:"required,max=64"`
	DocumentNo       string     `json:"document_no" validate:"omitempty,max=128"`
	IssuingAuthority string     `json:"issuing_authority" validate:"omitempty,max=255"`
	IssuedAt         *time.Time `json:"issued_at"`
	ValidFrom        *time.Time `json:"valid_from"`
	ExpiresOn        *time.Time `json:"expires_on"`
	AttachmentPath   string     `json:"attachment_path" validate:"omitempty,max=512"`
	Notes            string     `json:"notes" validate:"omitempty,max=512"`
}

func (req DocumentRequest) toNew(contractorID int64) NewDocument {
	return NewDocument{
		ContractorID: contractorID, TypeKey: req.DocumentTypeKey, DocumentNo: req.DocumentNo,
		IssuingAuthority: req.IssuingAuthority, IssuedAt: req.IssuedAt,
		ValidFrom: req.ValidFrom, ExpiresOn: req.ExpiresOn,
		AttachmentPath: req.AttachmentPath, Notes: req.Notes,
	}
}

// DocumentDecisionRequest is the verify or refuse click.
type DocumentDecisionRequest struct {
	Action string `json:"action" validate:"required,oneof=verify reject"`
	Note   string `json:"note" validate:"omitempty,max=512"`
}

// WindowRequest is one weekly slot. day_of_week is ISO: 0 is every day, 1 Monday, 7 Sunday.
type WindowRequest struct {
	GateID    int64  `json:"gate_id" validate:"omitempty,gt=0"`
	DayOfWeek int    `json:"day_of_week" validate:"gte=0,lte=7"`
	StartsAt  string `json:"starts_at" validate:"required,max=8"`
	ClosesAt  string `json:"closes_at" validate:"required,max=8"`
}

// WindowInput is the window as the service validates it, after the transport's own checks.
type WindowInput struct {
	GateID    int64
	DayOfWeek int
	StartsAt  string
	ClosesAt  string
}

// ContractorPassRequest is one pass as the desk files it. valid_from and valid_to are calendar
// dates; the Core pass covers both days whole in the tenant's timezone.
type ContractorPassRequest struct {
	ContractorID   int64           `json:"contractor_id" validate:"required,gt=0"`
	OrganizationID int64           `json:"organization_id" validate:"omitempty,gt=0"`
	PassType       string          `json:"pass_type" validate:"required,oneof=contractor_recurring contractor_day service_visit"`
	ValidFrom      time.Time       `json:"valid_from"`
	ValidTo        time.Time       `json:"valid_to"`
	BadgeNo        string          `json:"badge_no" validate:"omitempty,max=64"`
	WorkOrderRef   string          `json:"work_order_ref" validate:"omitempty,max=64"`
	AccessZone     string          `json:"access_zone" validate:"omitempty,max=128"`
	EscortRequired bool            `json:"escort_required"`
	IsMultiEntry   *bool           `json:"is_multi_entry"`
	Windows        []WindowRequest `json:"windows" validate:"omitempty,max=20,dive"`
	Origin         string          `json:"origin" validate:"omitempty,oneof=walk_in pre_registered online api import renewal device"`
	Notes          string          `json:"notes" validate:"omitempty,max=2000"`
}

func (req ContractorPassRequest) toRequest() PassRequest {
	windows := make([]WindowInput, 0, len(req.Windows))
	for _, window := range req.Windows {
		windows = append(windows, WindowInput{
			GateID: window.GateID, DayOfWeek: window.DayOfWeek,
			StartsAt: window.StartsAt, ClosesAt: window.ClosesAt,
		})
	}
	return PassRequest{
		ContractorID: req.ContractorID, OrganizationID: req.OrganizationID,
		PassTypeKey: req.PassType, ValidFrom: req.ValidFrom, ValidTo: req.ValidTo,
		BadgeNo: req.BadgeNo, WorkOrderRef: req.WorkOrderRef, AccessZone: req.AccessZone,
		EscortRequired: req.EscortRequired, IsMultiEntry: req.IsMultiEntry,
		Windows: windows, Origin: req.Origin, Notes: req.Notes,
	}
}

// PassDecisionRequest is the approve or refuse click.
type PassDecisionRequest struct {
	Action string `json:"action" validate:"required,oneof=approve reject"`
	Note   string `json:"note" validate:"omitempty,max=512"`
}

// NoteRequest is the reason a revoke or a block has to carry.
type NoteRequest struct {
	Reason string `json:"reason" validate:"required,max=512"`
}

// StatusRequest is one status change on a firm or a person.
type StatusRequest struct {
	Status string `json:"status" validate:"required,max=16"`
}

// The views.

type OrganizationView struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	ShortName      *string    `json:"short_name,omitempty"`
	RegistrationNo *string    `json:"registration_no,omitempty"`
	TaxID          *string    `json:"tax_id,omitempty"`
	Trade          *string    `json:"trade,omitempty"`
	ContactName    *string    `json:"contact_name,omitempty"`
	ContactEmail   *string    `json:"contact_email,omitempty"`
	ContactPhone   *string    `json:"contact_phone,omitempty"`
	Address        *string    `json:"address,omitempty"`
	ContractRef    *string    `json:"contract_ref,omitempty"`
	ContractStart  *time.Time `json:"contract_start,omitempty"`
	ContractEnd    *time.Time `json:"contract_end,omitempty"`
	Status         string     `json:"status"`
	Notes          *string    `json:"notes,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func viewOfOrganization(o Organization) OrganizationView {
	return OrganizationView{
		ID: o.ID, Name: o.Name, ShortName: o.ShortName, RegistrationNo: o.RegistrationNo,
		TaxID: o.TaxID, Trade: o.Trade, ContactName: o.ContactName,
		ContactEmail: o.ContactEmail, ContactPhone: o.ContactPhone, Address: o.Address,
		ContractRef: o.ContractRef, ContractStart: o.ContractStart, ContractEnd: o.ContractEnd,
		Status: o.Status, Notes: o.Notes, CreatedAt: o.CreatedAt,
	}
}

func viewsOfOrganizations(rows []Organization) []OrganizationView {
	views := make([]OrganizationView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfOrganization(row))
	}
	return views
}

// ContractorView is the person the desk works from: badge, employer and identity, which is what
// a guard reads off a card at the barrier. The photo and the ID number each carry a purged_at
// companion in the table, and this view simply shows whatever the retention job has left.
type ContractorView struct {
	ID                  int64      `json:"id"`
	OrganizationID      *int64     `json:"organization_id,omitempty"`
	OrganizationName    string     `json:"organization_name,omitempty"`
	FirstName           string     `json:"first_name"`
	LastName            string     `json:"last_name"`
	FullName            string     `json:"full_name"`
	Phone               *string    `json:"phone,omitempty"`
	Email               *string    `json:"email,omitempty"`
	Nationality         *string    `json:"nationality,omitempty"`
	IDType              *string    `json:"id_type,omitempty"`
	IDNumber            *string    `json:"id_number,omitempty"`
	BadgeNo             *string    `json:"badge_no,omitempty"`
	AccessCardNo        *string    `json:"access_card_no,omitempty"`
	Trade               *string    `json:"trade,omitempty"`
	SponsorUserID       *int64     `json:"sponsor_user_id,omitempty"`
	SponsorDepartmentID *int64     `json:"sponsor_department_id,omitempty"`
	Status              string     `json:"status"`
	DocumentsVerifiedAt *time.Time `json:"documents_verified_at,omitempty"`
	Notes               *string    `json:"notes,omitempty"`
	ExternalRef         *string    `json:"external_ref,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

func viewOfContractor(c Contractor) ContractorView {
	return ContractorView{
		ID: c.ID, OrganizationID: c.OrganizationID, OrganizationName: c.OrganizationName,
		FirstName: c.FirstName, LastName: c.LastName, FullName: c.Name(), Phone: c.Phone,
		Email: c.Email, Nationality: c.Nationality, IDType: c.IDType, IDNumber: c.IDNumber,
		BadgeNo: c.BadgeNo, AccessCardNo: c.AccessCardNo, Trade: c.Trade,
		SponsorUserID: c.SponsorUserID, SponsorDepartmentID: c.SponsorDepartmentID,
		Status: c.Status, DocumentsVerifiedAt: c.DocumentsVerifiedAt, Notes: c.Notes,
		ExternalRef: c.ExternalRef, CreatedAt: c.CreatedAt,
	}
}

func viewsOfContractors(rows []Contractor) []ContractorView {
	views := make([]ContractorView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfContractor(row))
	}
	return views
}

// DocumentView is one piece of paperwork. expired is computed against today on the site's
// clock, so the desk sees the same answer the barrier would give.
type DocumentView struct {
	ID               int64      `json:"id"`
	ContractorID     int64      `json:"contractor_id"`
	DocumentTypeKey  string     `json:"document_type_key"`
	TypeName         string     `json:"type_name"`
	Mandatory        bool       `json:"is_mandatory"`
	DocumentNo       *string    `json:"document_no,omitempty"`
	IssuingAuthority *string    `json:"issuing_authority,omitempty"`
	IssuedAt         *time.Time `json:"issued_at,omitempty"`
	ValidFrom        *time.Time `json:"valid_from,omitempty"`
	ExpiresOn        *time.Time `json:"expires_on,omitempty"`
	AttachmentPath   *string    `json:"attachment_path,omitempty"`
	Status           string     `json:"status"`
	VerifiedByUserID *int64     `json:"verified_by_user_id,omitempty"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	RejectionReason  *string    `json:"rejection_reason,omitempty"`
	ReminderSentAt   *time.Time `json:"reminder_sent_at,omitempty"`
	Notes            *string    `json:"notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	Expired          bool       `json:"is_expired"`
	DaysLeft         *int       `json:"days_left,omitempty"`
}

func viewOfDocument(d Document, today time.Time) DocumentView {
	view := DocumentView{
		ID: d.ID, ContractorID: d.ContractorID, DocumentTypeKey: d.TypeKey,
		TypeName: d.TypeName, Mandatory: d.Mandatory, DocumentNo: d.DocumentNo,
		IssuingAuthority: d.IssuingAuthority, IssuedAt: d.IssuedAt, ValidFrom: d.ValidFrom,
		ExpiresOn: d.ExpiresOn, AttachmentPath: d.AttachmentPath, Status: d.Status,
		VerifiedByUserID: d.VerifiedByUserID, VerifiedAt: d.VerifiedAt,
		RejectionReason: d.RejectionReason, ReminderSentAt: d.ReminderSentAt,
		Notes: d.Notes, CreatedAt: d.CreatedAt, Expired: d.Expired(today),
	}
	if d.ExpiresOn != nil {
		days := int(day(*d.ExpiresOn).Sub(today).Hours() / 24)
		view.DaysLeft = &days
	}
	return view
}

func viewsOfDocuments(rows []Document, today time.Time) []DocumentView {
	views := make([]DocumentView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfDocument(row, today))
	}
	return views
}

// DocumentTypeView is the catalogue row the upload form draws its dropdown from.
type DocumentTypeView struct {
	TypeKey            string `json:"type_key"`
	Name               string `json:"name"`
	HolderType         string `json:"holder_type"`
	IsMandatory        bool   `json:"is_mandatory"`
	ValidityMonths     *int   `json:"validity_months,omitempty"`
	RequiresValidation bool   `json:"requires_verification"`
	ReminderDaysBefore int    `json:"reminder_days_before"`
}

func viewsOfDocumentTypes(rows []DocumentType) []DocumentTypeView {
	views := make([]DocumentTypeView, 0, len(rows))
	for _, row := range rows {
		views = append(views, DocumentTypeView{
			TypeKey: row.TypeKey, Name: row.Name, HolderType: row.HolderType,
			IsMandatory: row.IsMandatory, ValidityMonths: row.ValidityMonths,
			RequiresValidation: row.RequiresValidation,
			ReminderDaysBefore: row.ReminderDaysBefore,
		})
	}
	return views
}

// WindowView is one slot of the rota, handed back so the desk shows what it filed rather than
// re-deriving it from a pattern nobody can read.
type WindowView struct {
	GateID          *int64 `json:"gate_id,omitempty"`
	DayOfWeek       int    `json:"day_of_week"`
	StartsAt        string `json:"starts_at"`
	ClosesAt        string `json:"closes_at"`
	CrossesMidnight bool   `json:"crosses_midnight"`
}

func viewOfWindow(w Window) WindowView {
	return WindowView{
		GateID: w.GateID, DayOfWeek: w.DayOfWeek, StartsAt: w.StartsAt,
		ClosesAt: w.ClosesAt, CrossesMidnight: w.CrossesMidnight,
	}
}

func viewsOfWindows(rows Schedule) []WindowView {
	views := make([]WindowView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfWindow(row))
	}
	return views
}

// PassView is one contractor pass: the module's row beside Core's, because a register that
// hides the disagreement between the two is the register nobody can reconcile.
type PassView struct {
	ID               int64        `json:"id"`
	PassID           int64        `json:"pass_id"`
	PassNo           string       `json:"pass_no"`
	PassStatus       string       `json:"pass_status"`
	EntryCount       int          `json:"entry_count"`
	ContractorID     int64        `json:"contractor_id"`
	ContractorName   string       `json:"contractor_name"`
	ContractorPhone  *string      `json:"contractor_phone,omitempty"`
	OrganizationID   *int64       `json:"organization_id,omitempty"`
	OrganizationName string       `json:"organization_name,omitempty"`
	SiteID           int64        `json:"site_id"`
	BadgeNo          *string      `json:"badge_no,omitempty"`
	IsMultiEntry     bool         `json:"is_multi_entry"`
	ValidFrom        time.Time    `json:"valid_from"`
	ValidTo          time.Time    `json:"valid_to"`
	WorkOrderRef     *string      `json:"work_order_ref,omitempty"`
	InductionDueOn   *time.Time   `json:"induction_due_on,omitempty"`
	EscortRequired   bool         `json:"escort_required"`
	AccessZone       *string      `json:"access_zone,omitempty"`
	BlockedReason    *string      `json:"blocked_reason,omitempty"`
	BlockSource      *string      `json:"blocked_source,omitempty"`
	BlockedAt        *time.Time   `json:"blocked_at,omitempty"`
	Status           string       `json:"status"`
	Notes            *string      `json:"notes,omitempty"`
	Windows          []WindowView `json:"windows"`
	ExpiresInDays    *int         `json:"expires_in_days,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
}

func viewOfPass(p Pass, today time.Time) PassView {
	view := PassView{
		ID: p.ID, PassID: p.PassID, PassNo: p.PassNo, PassStatus: p.PassStatus,
		EntryCount: p.EntryCount, ContractorID: p.ContractorID,
		ContractorName: p.ContractorName, ContractorPhone: p.ContractorPhone,
		OrganizationID: p.OrganizationID, OrganizationName: p.OrganizationName,
		SiteID: p.SiteID, BadgeNo: p.BadgeNo, IsMultiEntry: p.IsMultiEntry,
		ValidFrom: day(p.ValidFrom), ValidTo: day(p.ValidTo), WorkOrderRef: p.WorkOrderRef,
		InductionDueOn: p.InductionDueOn, EscortRequired: p.EscortRequired,
		AccessZone: p.AccessZone, BlockedReason: p.BlockedReason,
		BlockSource: p.BlockSource, BlockedAt: p.BlockedAt, Status: p.Status,
		Notes: p.Notes, Windows: viewsOfWindows(p.Windows), CreatedAt: p.CreatedAt,
	}
	if p.Open() {
		days := int(day(p.ValidTo).Sub(today).Hours() / 24)
		view.ExpiresInDays = &days
	}
	return view
}

func viewsOfPasses(rows []Pass, today time.Time) []PassView {
	views := make([]PassView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfPass(row, today))
	}
	return views
}

// The query strings.

func ParseOrganizations(r *http.Request) (OrganizationFilter, httpx.Page, error) {
	query := r.URL.Query()
	filter := OrganizationFilter{Status: query.Get("status"), Search: query.Get("search")}
	if filter.Status != "" && !validFirmStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a firm status")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func ParseContractors(r *http.Request) (ContractorFilter, httpx.Page, error) {
	query := r.URL.Query()
	organizationID, err := optionalID(query, "organization_id")
	if err != nil {
		return ContractorFilter{}, httpx.Page{}, err
	}
	filter := ContractorFilter{
		OrganizationID: organizationID, Status: query.Get("status"),
		Search: query.Get("search"),
	}
	if filter.Status != "" && !validPersonStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a contractor status")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func ParsePasses(r *http.Request) (PassFilter, httpx.Page, error) {
	query := r.URL.Query()
	contractorID, err := optionalID(query, "contractor_id")
	if err != nil {
		return PassFilter{}, httpx.Page{}, err
	}
	organizationID, err := optionalID(query, "organization_id")
	if err != nil {
		return PassFilter{}, httpx.Page{}, err
	}
	siteID, err := optionalID(query, "site_id")
	if err != nil {
		return PassFilter{}, httpx.Page{}, err
	}
	open, err := optionalBool(query, "open")
	if err != nil {
		return PassFilter{}, httpx.Page{}, err
	}
	filter := PassFilter{
		ContractorID: contractorID, OrganizationID: organizationID, SiteID: siteID,
		Status: query.Get("status"), Search: query.Get("search"),
		OpenOnly: open != nil && *open,
	}
	if filter.Status != "" && !Status(filter.Status).valid() {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a contractor pass status")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
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

// optionalBool refuses a value that is neither. Silently dropping ?open=maybe would list the
// whole register, which is the one answer nobody asked for.
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
