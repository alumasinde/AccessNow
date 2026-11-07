package contractor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

// In rebinds the repository to an open transaction, keeping one method set.
func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const organizationColumns = `id, tenant_id, name, short_name, registration_no, tax_id, trade,
	contact_name, contact_email, contact_phone, address, contract_ref, contract_start,
	contract_end, status, notes, created_at`

const contractorColumns = `c.id, c.tenant_id, c.organization_id, c.first_name, c.last_name,
	c.phone, c.email, c.nationality, c.id_type, c.id_number, c.id_number_purged_at,
	c.photo_path, c.photo_purged_at, c.badge_no, c.access_card_no, c.trade, c.sponsor_user_id,
	c.sponsor_department_id, c.status, c.documents_verified_at, c.notes, c.external_ref,
	c.created_at, COALESCE(o.name, '') AS organization_name`

const contractorFrom = `
	FROM   contractors c
	LEFT   JOIN contractor_organizations o ON o.id = c.organization_id`

const documentColumns = `d.id, d.tenant_id, d.contractor_id, d.document_type_key, d.document_no,
	d.issuing_authority, d.issued_at, d.valid_from, d.expires_on, d.attachment_path,
	d.status, d.verified_by_user_id, d.verified_at, d.rejection_reason, d.reminder_sent_at,
	d.notes, d.created_at, dt.name AS type_name, dt.is_mandatory,
	dt.reminder_days_before`

const documentFrom = `
	FROM   contractor_documents d
	JOIN   document_types dt ON dt.type_key = d.document_type_key`

// The register reads the pass with Core's own row and the person behind it, so one query
// answers "who holds this badge, on what number, and is the paperwork still good".
const passColumns = `cp.id, cp.tenant_id, cp.pass_id, cp.contractor_id, cp.organization_id,
	cp.site_id, cp.badge_no, cp.is_multi_entry, cp.valid_from, cp.valid_to, cp.work_order_ref,
	cp.induction_completed_at, cp.induction_due_on, cp.escort_required, cp.access_zone,
	cp.blocked_reason, cp.blocked_source, cp.blocked_at, cp.status, cp.notes, cp.created_at,
	p.pass_no, p.status AS pass_status, p.entry_count,
	CONCAT_WS(' ', c.first_name, c.last_name) AS contractor_name, c.phone AS contractor_phone,
	COALESCE(o.name, '') AS organization_name`

const passFrom = `
	FROM   contractor_passes cp
	JOIN   passes p      ON p.id = cp.pass_id AND p.tenant_id = cp.tenant_id
	JOIN   contractors c ON c.id = cp.contractor_id AND c.tenant_id = cp.tenant_id
	LEFT   JOIN contractor_organizations o ON o.id = cp.organization_id`

const windowColumns = `id, tenant_id, contractor_pass_id, gate_id, day_of_week, starts_at,
	closes_at, crosses_midnight`

const documentTypeColumns = `id, type_key, name, holder_type, applies_to_module, is_mandatory,
	validity_months, requires_verification, reminder_days_before, status`

// The firms.

func (r *Repository) CreateOrganization(ctx context.Context, in NewOrganization) (int64, error) {
	const query = `
		INSERT INTO contractor_organizations
			(tenant_id, name, short_name, registration_no, tax_id, trade, contact_name,
			 contact_email, contact_phone, address, contract_ref, contract_start, contract_end,
			 status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.Name, text(in.ShortName), text(in.RegistrationNo), text(in.TaxID),
		text(in.Trade), text(in.ContactName), text(in.ContactEmail), text(in.ContactPhone),
		text(in.Address), text(in.ContractRef), nullableTime(in.ContractStart),
		nullableTime(in.ContractEnd), in.Status, text(in.Notes))
	if err != nil {
		if db.IsDuplicate(err) {
			return 0, errs.Conflict("a firm called " + in.Name + " is already on file")
		}
		return 0, fmt.Errorf("create contractor organization: %w", err)
	}
	return lastID(result)
}

func (r *Repository) OrganizationByID(ctx context.Context, tenantID, id int64) (Organization, error) {
	const query = `SELECT ` + organizationColumns + `
		FROM contractor_organizations WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	var out Organization
	err := r.q.GetContext(ctx, &out, query, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, errs.NotFound("contractor organization")
	}
	if err != nil {
		return Organization{}, fmt.Errorf("read contractor organization: %w", err)
	}
	return out, nil
}

type OrganizationFilter struct {
	TenantID int64
	Status   string
	Search   string
	Limit    int
	Before   *httpx.Cursor
}

func (r *Repository) Organizations(ctx context.Context, f OrganizationFilter) ([]Organization, error) {
	where := []string{"tenant_id = ?", "deleted_at IS NULL"}
	args := []any{f.TenantID}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Search != "" {
		where = append(where, "(name LIKE ? OR registration_no LIKE ? OR contact_name LIKE ?)")
		for range 3 {
			args = append(args, like(f.Search))
		}
	}
	if f.Before != nil {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	args = append(args, f.Limit)

	query := `SELECT ` + organizationColumns + ` FROM contractor_organizations WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY created_at DESC, id DESC LIMIT ?`
	rows := []Organization{}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list contractor organizations: %w", err)
	}
	return rows, nil
}

func (r *Repository) SetOrganizationStatus(ctx context.Context, tenantID, id int64, status string) (bool, error) {
	const query = `UPDATE contractor_organizations SET status = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, status, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("set contractor organization status: %w", err)
	}
	return changed(result)
}

// The people.

func (r *Repository) CreateContractor(ctx context.Context, in NewContractor) (int64, error) {
	const query = `
		INSERT INTO contractors
			(tenant_id, organization_id, first_name, last_name, phone, email, nationality,
			 id_type, id_number, badge_no, access_card_no, trade, sponsor_user_id,
			 sponsor_department_id, status, notes, external_ref, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, nullable(in.OrganizationID), in.FirstName, in.LastName, text(in.Phone),
		text(in.Email), text(in.Nationality), text(in.IDType), text(in.IDNumber),
		text(in.BadgeNo), text(in.AccessCardNo), text(in.Trade),
		nullable(in.SponsorUserID), nullable(in.SponsorDepartmentID), in.Status,
		text(in.Notes), text(in.ExternalRef), nullable(in.CreatedBy))
	if err != nil {
		if db.IsDuplicate(err) {
			return 0, errs.Conflict("that person is already on file for this tenant")
		}
		return 0, fmt.Errorf("create contractor: %w", err)
	}
	return lastID(result)
}

func (r *Repository) ContractorByID(ctx context.Context, tenantID, id int64) (Contractor, error) {
	query := `SELECT ` + contractorColumns + contractorFrom +
		` WHERE c.tenant_id = ? AND c.id = ? AND c.deleted_at IS NULL`
	var out Contractor
	err := r.q.GetContext(ctx, &out, query, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Contractor{}, errs.NotFound("contractor")
	}
	if err != nil {
		return Contractor{}, fmt.Errorf("read contractor: %w", err)
	}
	return out, nil
}

type ContractorFilter struct {
	TenantID       int64
	OrganizationID int64
	Status         string
	Search         string
	Limit          int
	Before         *httpx.Cursor
}

func (r *Repository) Contractors(ctx context.Context, f ContractorFilter) ([]Contractor, error) {
	where := []string{"c.tenant_id = ?", "c.deleted_at IS NULL"}
	args := []any{f.TenantID}
	if f.OrganizationID > 0 {
		where = append(where, "c.organization_id = ?")
		args = append(args, f.OrganizationID)
	}
	if f.Status != "" {
		where = append(where, "c.status = ?")
		args = append(args, f.Status)
	}
	if f.Search != "" {
		where = append(where, `(c.first_name LIKE ? OR c.last_name LIKE ? OR c.badge_no LIKE ?
			OR c.phone LIKE ? OR c.id_number LIKE ? OR o.name LIKE ?)`)
		for range 6 {
			args = append(args, like(f.Search))
		}
	}
	if f.Before != nil {
		where = append(where, "(c.created_at < ? OR (c.created_at = ? AND c.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	args = append(args, f.Limit)

	query := `SELECT ` + contractorColumns + contractorFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY c.created_at DESC, c.id DESC LIMIT ?`
	rows := []Contractor{}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list contractors: %w", err)
	}
	return rows, nil
}

func (r *Repository) SetContractorStatus(ctx context.Context, tenantID, id int64, status string) (bool, error) {
	const query = `UPDATE contractors SET status = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, status, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("set contractor status: %w", err)
	}
	return changed(result)
}

// The paperwork.

func (r *Repository) DocumentTypes(ctx context.Context) ([]DocumentType, error) {
	const query = `SELECT ` + documentTypeColumns + ` FROM document_types
		WHERE status = 'active' ORDER BY holder_type, name`
	rows := []DocumentType{}
	if err := r.q.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list document types: %w", err)
	}
	return rows, nil
}

func (r *Repository) DocumentType(ctx context.Context, key string) (DocumentType, error) {
	const query = `SELECT ` + documentTypeColumns + ` FROM document_types WHERE type_key = ?`
	var out DocumentType
	err := r.q.GetContext(ctx, &out, query, key)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentType{}, errs.NotFound("document type " + key)
	}
	if err != nil {
		return DocumentType{}, fmt.Errorf("read document type: %w", err)
	}
	return out, nil
}

func (r *Repository) CreateDocument(ctx context.Context, in NewDocument) (int64, error) {
	const query = `
		INSERT INTO contractor_documents
			(tenant_id, contractor_id, document_type_key, document_no, issuing_authority,
			 issued_at, valid_from, expires_on, attachment_path, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.ContractorID, in.TypeKey, text(in.DocumentNo),
		text(in.IssuingAuthority), nullableTime(in.IssuedAt), nullableTime(in.ValidFrom),
		nullableTime(in.ExpiresOn), text(in.AttachmentPath), string(in.Status),
		text(in.Notes))
	if err != nil {
		if db.IsDuplicate(err) {
			return 0, errs.Conflict("that document is already on file for this contractor")
		}
		return 0, fmt.Errorf("create contractor document: %w", err)
	}
	return lastID(result)
}

func (r *Repository) DocumentByID(ctx context.Context, tenantID, id int64) (Document, error) {
	query := `SELECT ` + documentColumns + documentFrom + ` WHERE d.tenant_id = ? AND d.id = ?`
	var out Document
	err := r.q.GetContext(ctx, &out, query, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, errs.NotFound("contractor document")
	}
	if err != nil {
		return Document{}, fmt.Errorf("read contractor document: %w", err)
	}
	return out, nil
}

func (r *Repository) Documents(ctx context.Context, tenantID, contractorID int64, status string) ([]Document, error) {
	where := []string{"d.tenant_id = ?", "d.contractor_id = ?"}
	args := []any{tenantID, contractorID}
	if status != "" {
		where = append(where, "d.status = ?")
		args = append(args, status)
	}
	query := `SELECT ` + documentColumns + documentFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY dt.is_mandatory DESC, d.expires_on, d.id`
	rows := []Document{}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list contractor documents: %w", err)
	}
	return rows, nil
}

// ExpiredMandatory is the set that stops a pass: the contractor's mandatory documents whose
// last valid day has passed. It is asked live at the barrier and by the sweep, so the gate and
// the register cannot disagree about whether a person is still cleared.
func (r *Repository) ExpiredMandatory(ctx context.Context, tenantID, contractorID int64,
	today time.Time) ([]Document, error) {
	query := `SELECT ` + documentColumns + documentFrom + `
		WHERE  d.tenant_id = ? AND d.contractor_id = ? AND dt.is_mandatory = 1
		  AND  dt.holder_type IN ('contractor', 'organization')
		  AND  d.status IN ('pending', 'valid', 'expiring', 'expired')
		  AND  d.expires_on IS NOT NULL AND d.expires_on < ?
		ORDER BY d.expires_on, d.id`
	rows := []Document{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, contractorID, today.UTC()); err != nil {
		return nil, fmt.Errorf("read expired contractor documents: %w", err)
	}
	return rows, nil
}

// MarkExpiredDocuments is the sweep's bulk step. It runs before the pass check, so a document
// whose date passed overnight is already `expired` when the block is filed, and the reason on
// the pass names the paper rather than a status the desk has to go and look up.
func (r *Repository) MarkExpiredDocuments(ctx context.Context, tenantID int64, today time.Time) (int, error) {
	const query = `UPDATE contractor_documents SET status = 'expired'
		WHERE tenant_id = ? AND status IN ('pending', 'valid', 'expiring')
		  AND expires_on IS NOT NULL AND expires_on < ?`
	result, err := r.q.ExecContext(ctx, query, tenantID, today.UTC())
	if err != nil {
		return 0, fmt.Errorf("expire contractor documents: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count expired contractor documents: %w", err)
	}
	return int(affected), nil
}

// DocumentsDueForReminder is the "about to run out" list: still valid, inside the catalogue's
// own warning window for its type, and not already warned about.
func (r *Repository) DocumentsDueForReminder(ctx context.Context, tenantID int64, today time.Time,
	limit int) ([]Document, error) {
	query := `SELECT ` + documentColumns + documentFrom + `
		WHERE  d.tenant_id = ? AND d.status = 'valid' AND d.expires_on IS NOT NULL
		  AND  d.expires_on >= ? AND d.reminder_sent_at IS NULL
		  AND  d.expires_on <= DATE_ADD(?, INTERVAL dt.reminder_days_before DAY)
		ORDER BY d.expires_on, d.id
		LIMIT ?`
	rows := []Document{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, today.UTC(), today.UTC(), limit); err != nil {
		return nil, fmt.Errorf("read documents due for a reminder: %w", err)
	}
	return rows, nil
}

// ExpiringDocuments is the desk's chase list: still good today, but due before the window
// closes. Expired rows are not here - they are a different phone call.
func (r *Repository) ExpiringDocuments(ctx context.Context, tenantID int64, today, until time.Time,
	limit int) ([]Document, error) {
	query := `SELECT ` + documentColumns + documentFrom + `
		WHERE  d.tenant_id = ? AND d.status IN ('valid', 'expiring')
		  AND  d.expires_on IS NOT NULL AND d.expires_on >= ? AND d.expires_on <= ?
		ORDER BY d.expires_on, d.id
		LIMIT ?`
	rows := []Document{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, today.UTC(), until.UTC(), limit); err != nil {
		return nil, fmt.Errorf("read expiring contractor documents: %w", err)
	}
	return rows, nil
}

func (r *Repository) MarkReminderSent(ctx context.Context, tenantID, id int64) (bool, error) {
	const query = `UPDATE contractor_documents
		SET status = CASE WHEN status = 'valid' THEN 'expiring' ELSE status END,
		    reminder_sent_at = UTC_TIMESTAMP(6)
		WHERE tenant_id = ? AND id = ? AND reminder_sent_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("mark document reminder sent: %w", err)
	}
	return changed(result)
}

// DecideDocument files the desk's answer. Only a document nobody has decided yet can be
// decided, so a second click lands on a row that has already moved and changes nothing.
func (r *Repository) DecideDocument(ctx context.Context, tenantID, id, actorUserID int64,
	to DocumentStatus, note string) (bool, error) {
	query := `UPDATE contractor_documents SET status = ?`
	args := []any{string(to)}
	switch to {
	case DocValid, DocExpiring:
		query += `, verified_by_user_id = ?, verified_at = UTC_TIMESTAMP(6),
			rejection_reason = NULL`
		args = append(args, nullable(actorUserID))
	case DocRejected:
		query += `, rejection_reason = ?, verified_by_user_id = ?, verified_at = UTC_TIMESTAMP(6)`
		args = append(args, text(note), nullable(actorUserID))
	}
	query += ` WHERE tenant_id = ? AND id = ? AND status = ?`
	args = append(args, tenantID, id, string(DocPending))
	result, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("decide contractor document: %w", err)
	}
	return changed(result)
}

// MarkContractorVerified is the note the person row carries: the desk has seen the file, which
// is a different fact from any one document still being inside its date.
func (r *Repository) MarkContractorVerified(ctx context.Context, tenantID, id int64) error {
	const query = `UPDATE contractors SET documents_verified_at = UTC_TIMESTAMP(6)
		WHERE tenant_id = ? AND id = ?`
	if _, err := r.q.ExecContext(ctx, query, tenantID, id); err != nil {
		return fmt.Errorf("stamp contractor documents: %w", err)
	}
	return nil
}

// The passes.

func (r *Repository) CreatePass(ctx context.Context, in NewPass) (int64, error) {
	const query = `
		INSERT INTO contractor_passes
			(tenant_id, pass_id, contractor_id, organization_id, site_id, badge_no,
			 is_multi_entry, valid_from, valid_to, work_order_ref, induction_completed_at,
			 induction_due_on, escort_required, access_zone, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.PassID, in.ContractorID, nullable(in.OrganizationID), in.SiteID,
		text(in.BadgeNo), boolInt(in.IsMultiEntry), in.ValidFrom.UTC(), in.ValidTo.UTC(),
		text(in.WorkOrderRef), nullableTime(in.InductionCompleted),
		nullableTime(in.InductionDueOn), boolInt(in.EscortRequired), text(in.AccessZone),
		string(in.Status), text(in.Notes))
	if err != nil {
		return 0, fmt.Errorf("create contractor pass: %w", err)
	}
	return lastID(result)
}

func (r *Repository) ReplaceWindows(ctx context.Context, tenantID, passID int64, windows []Window) error {
	const clear = `DELETE FROM contractor_pass_windows WHERE tenant_id = ? AND contractor_pass_id = ?`
	if _, err := r.q.ExecContext(ctx, clear, tenantID, passID); err != nil {
		return fmt.Errorf("clear contractor pass windows: %w", err)
	}
	if len(windows) == 0 {
		return nil
	}
	const insert = `
		INSERT INTO contractor_pass_windows
			(tenant_id, contractor_pass_id, gate_id, day_of_week, starts_at, closes_at,
			 crosses_midnight)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	for _, window := range windows {
		if _, err := r.q.ExecContext(ctx, insert, tenantID, passID, window.GateID,
			window.DayOfWeek, window.StartsAt, window.ClosesAt,
			boolInt(window.CrossesMidnight)); err != nil {
			return fmt.Errorf("insert contractor pass window: %w", err)
		}
	}
	return nil
}

func (r *Repository) Windows(ctx context.Context, tenantID, passID int64) (Schedule, error) {
	const query = `SELECT ` + windowColumns + ` FROM contractor_pass_windows
		WHERE tenant_id = ? AND contractor_pass_id = ?
		ORDER BY day_of_week, starts_at`
	rows := Schedule{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, passID); err != nil {
		return nil, fmt.Errorf("read contractor pass windows: %w", err)
	}
	return rows, nil
}

func (r *Repository) PassByID(ctx context.Context, tenantID, id int64) (Pass, error) {
	return r.readPass(ctx, ` WHERE cp.tenant_id = ? AND cp.id = ?`, tenantID, id)
}

// PassByCorePass is how the barrier finds the module row behind a pass it already holds: the
// scan knows Core's id, and only this module knows the rota and the paperwork.
func (r *Repository) PassByCorePass(ctx context.Context, tenantID, passID int64) (Pass, error) {
	return r.readPass(ctx, ` WHERE cp.tenant_id = ? AND cp.pass_id = ?`, tenantID, passID)
}

func (r *Repository) readPass(ctx context.Context, where string, args ...any) (Pass, error) {
	query := `SELECT ` + passColumns + passFrom + where
	var out Pass
	err := r.q.GetContext(ctx, &out, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Pass{}, errs.NotFound("contractor pass")
	}
	if err != nil {
		return Pass{}, fmt.Errorf("read contractor pass: %w", err)
	}
	return out, nil
}

type PassFilter struct {
	TenantID       int64
	ContractorID   int64
	OrganizationID int64
	SiteID         int64
	Status         string
	OpenOnly       bool
	Search         string
	Limit          int
	Before         *httpx.Cursor
}

func (r *Repository) Passes(ctx context.Context, f PassFilter) ([]Pass, error) {
	where := []string{"cp.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.ContractorID > 0 {
		where = append(where, "cp.contractor_id = ?")
		args = append(args, f.ContractorID)
	}
	if f.OrganizationID > 0 {
		where = append(where, "cp.organization_id = ?")
		args = append(args, f.OrganizationID)
	}
	if f.SiteID > 0 {
		where = append(where, "cp.site_id = ?")
		args = append(args, f.SiteID)
	}
	if f.Status != "" {
		where = append(where, "cp.status = ?")
		args = append(args, f.Status)
	}
	if f.OpenOnly {
		where = append(where, "cp.status IN ('active', 'blocked')")
	}
	if f.Search != "" {
		where = append(where, `(p.pass_no LIKE ? OR c.first_name LIKE ? OR c.last_name LIKE ?
			OR cp.badge_no LIKE ? OR cp.work_order_ref LIKE ? OR o.name LIKE ?)`)
		for range 6 {
			args = append(args, like(f.Search))
		}
	}
	if f.Before != nil {
		where = append(where, "(cp.created_at < ? OR (cp.created_at = ? AND cp.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	args = append(args, f.Limit)

	query := `SELECT ` + passColumns + passFrom + ` WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY cp.created_at DESC, cp.id DESC LIMIT ?`
	rows := []Pass{}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list contractor passes: %w", err)
	}
	return rows, nil
}

// MarkDecided mirrors an approval click onto the detail row. It is keyed on pending, so a
// second click lands on nothing.
func (r *Repository) MarkDecided(ctx context.Context, tenantID, id int64, to Status) (bool, error) {
	const query = `UPDATE contractor_passes SET status = ?
		WHERE tenant_id = ? AND id = ? AND status = ?`
	result, err := r.q.ExecContext(ctx, query, string(to), tenantID, id, string(StatusPending))
	if err != nil {
		return false, fmt.Errorf("decide contractor pass: %w", err)
	}
	return changed(result)
}

// MoveStatus is a compare-and-set on the status the caller read, which is what makes the sweep
// safe to run twice: a row another run already moved is not moved again.
func (r *Repository) MoveStatus(ctx context.Context, tenantID, id int64, from, to Status,
	reason string, source BlockSource) (bool, error) {
	query := `UPDATE contractor_passes SET status = ?`
	args := []any{string(to)}
	switch to {
	case StatusBlocked:
		query += `, blocked_reason = ?, blocked_source = ?, blocked_at = UTC_TIMESTAMP(6)`
		args = append(args, text(reason), string(source))
	case StatusActive:
		query += `, blocked_reason = NULL, blocked_source = NULL, blocked_at = NULL`
	}
	query += ` WHERE tenant_id = ? AND id = ? AND status = ?`
	args = append(args, tenantID, id, string(from))

	result, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("move contractor pass status: %w", err)
	}
	return changed(result)
}

// PassesPastValidity is the sweep's first list: still on the books, past the last day it was
// issued for. Blocked passes are included, because a block does not make a pass immortal.
func (r *Repository) PassesPastValidity(ctx context.Context, tenantID int64, today time.Time) ([]Pass, error) {
	query := `SELECT ` + passColumns + passFrom + `
		WHERE  cp.tenant_id = ? AND cp.status IN ('active', 'blocked') AND cp.valid_to < ?
		ORDER BY cp.valid_to, cp.id`
	rows := []Pass{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, today.UTC()); err != nil {
		return nil, fmt.Errorf("read contractor passes past validity: %w", err)
	}
	return rows, nil
}

// PassesBlockedByDocuments is the sweep's second list: active, still inside its range, and now
// held by somebody with an expired mandatory document.
func (r *Repository) PassesBlockedByDocuments(ctx context.Context, tenantID int64,
	today time.Time) ([]Pass, error) {
	query := `SELECT ` + passColumns + passFrom + `
		WHERE  cp.tenant_id = ? AND cp.status = 'active' AND cp.valid_to >= ?
		  AND  EXISTS (
		        SELECT 1
		        FROM   contractor_documents d
		        JOIN   document_types dt ON dt.type_key = d.document_type_key
		        WHERE  d.tenant_id = cp.tenant_id AND d.contractor_id = cp.contractor_id
		          AND  dt.is_mandatory = 1 AND dt.holder_type IN ('contractor', 'organization')
		          AND  d.status IN ('pending', 'valid', 'expiring', 'expired')
		          AND  d.expires_on IS NOT NULL AND d.expires_on < ?)
		ORDER BY cp.id`
	rows := []Pass{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, today.UTC(), today.UTC()); err != nil {
		return nil, fmt.Errorf("read contractor passes blocked by documents: %w", err)
	}
	return rows, nil
}

// PassesReadyToUnblock is the other side of the same question, and it only ever considers
// blocks the sweep itself filed. A pass a supervisor stopped stays stopped until a supervisor
// says otherwise, whatever the paperwork now says.
func (r *Repository) PassesReadyToUnblock(ctx context.Context, tenantID int64,
	today time.Time) ([]Pass, error) {
	query := `SELECT ` + passColumns + passFrom + `
		WHERE  cp.tenant_id = ? AND cp.status = 'blocked' AND cp.blocked_source = 'document'
		  AND  cp.valid_to >= ?
		  AND  NOT EXISTS (
		        SELECT 1
		        FROM   contractor_documents d
		        JOIN   document_types dt ON dt.type_key = d.document_type_key
		        WHERE  d.tenant_id = cp.tenant_id AND d.contractor_id = cp.contractor_id
		          AND  dt.is_mandatory = 1 AND dt.holder_type IN ('contractor', 'organization')
		          AND  d.status IN ('pending', 'valid', 'expiring', 'expired')
		          AND  d.expires_on IS NOT NULL AND d.expires_on < ?)
		ORDER BY cp.id`
	rows := []Pass{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, today.UTC(), today.UTC()); err != nil {
		return nil, fmt.Errorf("read contractor passes ready to unblock: %w", err)
	}
	return rows, nil
}

// GateOnSite answers whether a gate named in a rota is this tenant's and this site's. The
// foreign key alone would accept another tenant's gate id, which is exactly the mistake a
// window has no way of noticing later.
func (r *Repository) GateOnSite(ctx context.Context, tenantID, siteID, gateID int64) (bool, error) {
	const query = `SELECT COUNT(*) > 0 FROM gates
		WHERE id = ? AND tenant_id = ? AND site_id = ? AND deleted_at IS NULL`
	var found bool
	if err := r.q.GetContext(ctx, &found, query, gateID, tenantID, siteID); err != nil {
		return false, fmt.Errorf("read gate: %w", err)
	}
	return found, nil
}

// OrganizationBelongs is the same question for the firm a person is filed under.
func (r *Repository) OrganizationBelongs(ctx context.Context, tenantID, id int64) (bool, error) {
	const query = `SELECT COUNT(*) > 0 FROM contractor_organizations
		WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`
	var found bool
	if err := r.q.GetContext(ctx, &found, query, id, tenantID); err != nil {
		return false, fmt.Errorf("read contractor organization: %w", err)
	}
	return found, nil
}

func changed(result sql.Result) (bool, error) { return db.RowsChanged(result) }

func lastID(result sql.Result) (int64, error) {
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted id: %w", err)
	}
	return id, nil
}

func like(term string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
}

func text(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullable(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
