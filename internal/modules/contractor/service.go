package contractor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/movements"
	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/clock"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// The outbox keys this module speaks. Only two of them are in the notification catalogue today
// - a document about to expire and a pass the paperwork has stopped - but the rest are
// published anyway, because an event is a fact about the system and a tenant who buys
// notifications later must be able to route one without the module changing.
const (
	eventRequested = "contractor.pass_requested"
	eventBlocked   = "contractor.pass_blocked"
	eventExpired   = "contractor.pass_expired"
	eventRevoked   = "contractor.pass_revoked"
	eventExpiring  = "contractor.document_expiring"
)

// Service owns the firms, the people, the paperwork and the pass around them. The barrier's
// own facts - whether somebody actually walked in - belong to Core; this module writes the
// permission and answers for it when the gate asks.
type Service struct {
	pool      *sqlx.DB
	repo      *Repository
	passes    *passes.Service
	passRepo  *passes.Repository
	publisher events.Publisher
	chain     audit.Writer
}

func NewService(pool *sqlx.DB, repo *Repository, passService *passes.Service,
	passRepo *passes.Repository, publisher events.Publisher, chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, passes: passService, passRepo: passRepo,
		publisher: publisher, chain: chain}
}

// The firms.

func (s *Service) CreateOrganization(ctx context.Context, scope tenancy.Scope,
	in NewOrganization) (Organization, error) {
	in.TenantID = scope.TenantID
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Organization{}, errs.BadField("name", "the firm needs a name")
	}
	if in.Status == "" {
		in.Status = FirmActive
	}
	if !validFirmStatus(in.Status) {
		return Organization{}, errs.BadField("status", in.Status+" is not a firm status")
	}
	if in.ContractStart != nil && in.ContractEnd != nil && in.ContractEnd.Before(*in.ContractStart) {
		return Organization{}, errs.BadField("contract_end", "the contract cannot end before it starts")
	}

	var created Organization
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		id, err := s.repo.In(c).CreateOrganization(ctx, in)
		if err != nil {
			return err
		}
		created, err = s.repo.In(c).OrganizationByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.organization_created",
			SubjectType: "contractor_organization", SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"name": in.Name, "status": in.Status},
		})
	})
	if err != nil {
		return Organization{}, err
	}
	return created, nil
}

func (s *Service) SetOrganizationStatus(ctx context.Context, scope tenancy.Scope, id int64,
	status string) (Organization, error) {
	if !validFirmStatus(status) {
		return Organization{}, errs.BadField("status", status+" is not a firm status")
	}
	current, err := s.repo.OrganizationByID(ctx, scope.TenantID, id)
	if err != nil {
		return Organization{}, err
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		changed, err := s.repo.In(c).SetOrganizationStatus(ctx, scope.TenantID, id, status)
		if err != nil {
			return err
		}
		if !changed {
			return errs.Conflict("firm " + current.Name + " changed while this request was in flight")
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.organization_status",
			SubjectType: "contractor_organization", SubjectID: id, SiteID: scope.SiteID,
			Before: map[string]any{"status": current.Status},
			After:  map[string]any{"status": status},
		})
	})
	if err != nil {
		return Organization{}, err
	}
	return s.repo.OrganizationByID(ctx, scope.TenantID, id)
}

// The people.

func (s *Service) CreateContractor(ctx context.Context, scope tenancy.Scope,
	in NewContractor) (Contractor, error) {
	in.TenantID = scope.TenantID
	in.CreatedBy = scope.UserID
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	if in.FirstName == "" || in.LastName == "" {
		return Contractor{}, errs.BadField("first_name", "a person needs a first and a last name")
	}
	// CHAR(2) nationality: the column holds an ISO code, so a hand-typed country name would be
	// truncated by the server rather than refused.
	in.Nationality = strings.ToUpper(strings.TrimSpace(in.Nationality))
	if in.Nationality != "" && len(in.Nationality) != 2 {
		return Contractor{}, errs.BadField("nationality", "expected a two letter country code")
	}
	if in.Status == "" {
		in.Status = PersonPending
	}
	if !validPersonStatus(in.Status) {
		return Contractor{}, errs.BadField("status", in.Status+" is not a contractor status")
	}
	if in.OrganizationID > 0 {
		found, err := s.repo.OrganizationBelongs(ctx, scope.TenantID, in.OrganizationID)
		if err != nil {
			return Contractor{}, err
		}
		if !found {
			return Contractor{}, errs.BadField("organization_id", "not a firm on this tenant")
		}
	}

	var created Contractor
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		id, err := s.repo.In(c).CreateContractor(ctx, in)
		if err != nil {
			return err
		}
		created, err = s.repo.In(c).ContractorByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.created",
			SubjectType: "contractor", SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"name": created.Name(), "status": in.Status,
				"organization_id": in.OrganizationID},
		})
	})
	if err != nil {
		return Contractor{}, err
	}
	return created, nil
}

func (s *Service) SetContractorStatus(ctx context.Context, scope tenancy.Scope, id int64,
	status string) (Contractor, error) {
	if !validPersonStatus(status) {
		return Contractor{}, errs.BadField("status", status+" is not a contractor status")
	}
	current, err := s.repo.ContractorByID(ctx, scope.TenantID, id)
	if err != nil {
		return Contractor{}, err
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		changed, err := s.repo.In(c).SetContractorStatus(ctx, scope.TenantID, id, status)
		if err != nil {
			return err
		}
		if !changed {
			return errs.Conflict("contractor " + current.Name() + " changed while this request was in flight")
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.status",
			SubjectType: "contractor", SubjectID: id, SiteID: scope.SiteID,
			Before: map[string]any{"status": current.Status},
			After:  map[string]any{"status": status},
		})
	})
	if err != nil {
		return Contractor{}, err
	}
	return s.repo.ContractorByID(ctx, scope.TenantID, id)
}

// The paperwork.

func (s *Service) AddDocument(ctx context.Context, scope tenancy.Scope,
	in NewDocument) (Document, error) {
	in.TenantID = scope.TenantID
	if _, err := s.repo.ContractorByID(ctx, scope.TenantID, in.ContractorID); err != nil {
		return Document{}, err
	}
	docType, err := s.repo.DocumentType(ctx, in.TypeKey)
	if err != nil {
		return Document{}, err
	}
	if docType.HolderType != "contractor" && docType.HolderType != "organization" {
		return Document{}, errs.BadField("document_type_key",
			docType.TypeKey+" is not contractor paperwork")
	}
	if in.ValidFrom != nil && in.ExpiresOn != nil && in.ExpiresOn.Before(*in.ValidFrom) {
		return Document{}, errs.BadField("expires_on", "a document cannot expire before it starts")
	}
	// A type nobody has to look at files itself as valid; everything else waits for the desk,
	// because a permit is not a permit until somebody has read it.
	if docType.RequiresValidation {
		in.Status = DocPending
	} else {
		in.Status = DocValid
	}

	var created Document
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		id, err := s.repo.In(c).CreateDocument(ctx, in)
		if err != nil {
			return err
		}
		created, err = s.repo.In(c).DocumentByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.document_uploaded",
			SubjectType: "contractor_document", SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"contractor_id": in.ContractorID, "type": in.TypeKey,
				"expires_on": expiryOf(in.ExpiresOn), "status": string(in.Status)},
		})
	})
	if err != nil {
		return Document{}, err
	}
	return created, nil
}

// DecideDocument is the desk's answer on one piece of paperwork. Verifying a document that has
// already run out is refused rather than recorded: the row would otherwise say a reviewer
// approved something they can see is expired, and every later reader would have to guess.
func (s *Service) DecideDocument(ctx context.Context, scope tenancy.Scope, id int64,
	approve bool, note string) (Document, error) {
	current, err := s.repo.DocumentByID(ctx, scope.TenantID, id)
	if err != nil {
		return Document{}, err
	}
	if DocumentStatus(current.Status) != DocPending {
		return Document{}, errs.Conflict("that document is already " + current.Status)
	}
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	if approve && current.ExpiresOn != nil && day(*current.ExpiresOn).Before(today) {
		return Document{}, errs.Conflict("that document expired on " +
			current.ExpiresOn.Format("2006-01-02") + "; upload the renewal instead")
	}
	if !approve && strings.TrimSpace(note) == "" {
		return Document{}, errs.BadField("note", "say why the document was refused")
	}

	to := DocValid
	if approve && current.ExpiresOn != nil && !day(*current.ExpiresOn).After(today.AddDate(0, 0, 30)) {
		// Approved, but close enough to the edge that the desk should see it coming.
		to = DocExpiring
	}
	if !approve {
		to = DocRejected
	}

	var decided Document
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		moved, err := repo.DecideDocument(ctx, scope.TenantID, id, scope.UserID, to, note)
		if err != nil {
			return err
		}
		if !moved {
			return errs.Conflict("that document was decided while this request was in flight")
		}
		if to != DocRejected {
			if err := repo.MarkContractorVerified(ctx, scope.TenantID, current.ContractorID); err != nil {
				return err
			}
		}
		decided, err = repo.DocumentByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.document_decided",
			SubjectType: "contractor_document", SubjectID: id, SiteID: scope.SiteID,
			Reason: note, Before: map[string]any{"status": current.Status},
			After: map[string]any{"status": string(to)},
		})
	})
	if err != nil {
		return Document{}, err
	}
	// Renewing the paperwork is allowed to lift a block the paperwork itself caused, without a
	// second issuance and without waiting for the nightly run.
	if decided.Status == string(DocValid) || decided.Status == string(DocExpiring) {
		if err := s.reconsider(ctx, scope, decided.ContractorID); err != nil {
			return Document{}, err
		}
	}
	return s.repo.DocumentByID(ctx, scope.TenantID, id)
}

// The passes.

// PassRequest is one contractor pass as the desk files it. ValidFrom and ValidTo are calendar
// dates: the Core pass covers the whole of both days in the tenant's timezone.
type PassRequest struct {
	ContractorID   int64
	OrganizationID int64
	PassTypeKey    string
	ValidFrom      time.Time
	ValidTo        time.Time
	BadgeNo        string
	WorkOrderRef   string
	AccessZone     string
	EscortRequired bool
	IsMultiEntry   *bool
	Windows        []WindowInput
	Origin         string
	Notes          string
}

func (in PassRequest) validate(today time.Time) error {
	if !validPassType(in.PassTypeKey) {
		return errs.BadField("pass_type", "pick contractor_recurring, contractor_day or service_visit")
	}
	if in.ValidFrom.IsZero() || in.ValidTo.IsZero() {
		return errs.BadField("valid_from", "the pass needs a start and an end date")
	}
	from, to := day(in.ValidFrom), day(in.ValidTo)
	if to.Before(from) {
		return errs.BadField("valid_to", "the pass cannot end before it starts")
	}
	if to.Before(today) {
		return errs.BadField("valid_to", "the pass already ended")
	}
	return nil
}

// Pass files a contractor pass and asks Core for the number behind it.
func (s *Service) Pass(ctx context.Context, scope tenancy.Scope, in PassRequest) (Pass, error) {
	if scope.SiteID == 0 {
		return Pass{}, errs.Forbidden("no site on this session")
	}
	loc := clock.Location(scope.Timezone)
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	if err := in.validate(today); err != nil {
		return Pass{}, err
	}

	member, err := s.repo.ContractorByID(ctx, scope.TenantID, in.ContractorID)
	if err != nil {
		return Pass{}, err
	}
	if !member.Cleared() {
		return Pass{}, errs.Conflict(member.Name() + " is " + member.Status +
			" and cannot be issued a pass")
	}
	if in.OrganizationID > 0 {
		found, err := s.repo.OrganizationBelongs(ctx, scope.TenantID, in.OrganizationID)
		if err != nil {
			return Pass{}, err
		}
		if !found {
			return Pass{}, errs.BadField("organization_id", "not a firm on this tenant")
		}
	} else if member.OrganizationID != nil {
		in.OrganizationID = *member.OrganizationID
	}
	// A pass issued to somebody already blocked by their paperwork would turn red at the gate
	// on its first scan, which is a worse answer than the desk being told now.
	expired, err := s.repo.ExpiredMandatory(ctx, scope.TenantID, member.ID, today)
	if err != nil {
		return Pass{}, err
	}
	if len(expired) > 0 {
		return Pass{}, errs.Conflict(member.Name() + " has an expired " +
			expired[0].TypeKey + "; renew it before issuing a pass")
	}
	schedule, err := s.schedule(ctx, scope, in.Windows)
	if err != nil {
		return Pass{}, err
	}
	passType, err := s.passRepo.TypeByKey(ctx, in.PassTypeKey)
	if err != nil {
		return Pass{}, err
	}

	status, coreStatus := StatusPending, passes.StatusPending
	if !passType.RequiresApproval {
		status, coreStatus = StatusActive, passes.StatusApproved
	}
	multi := defaultMultiEntry(in.PassTypeKey)
	if in.IsMultiEntry != nil {
		multi = *in.IsMultiEntry
	}
	origin := in.Origin
	if origin == "" {
		origin = "walk_in"
	}

	var filed Pass
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		pass, err := s.passes.Issue(ctx, c, passes.Issue{
			New: passes.New{
				TenantID: scope.TenantID, SiteID: scope.SiteID, PassTypeID: passType.ID,
				CategoryKey: passType.CategoryKey, SourceModuleKey: ModuleKey,
				Status: coreStatus, Origin: origin, SubjectName: member.Name(),
				SubjectPhone: member.Phone, Purpose: strPtr(purposeOf(passType.TypeKey, member)),
				RequesterUserID: num(scope.UserID), DepartmentID: member.SponsorDepartmentID,
				ValidFrom:  StartOfDay(in.ValidFrom, loc),
				ValidUntil: timePtr(EndOfDay(in.ValidTo, loc)),
			},
			FormatKey: formatFor(in.PassTypeKey), Timezone: scope.Timezone,
			ActorUserID: scope.UserID,
		})
		if err != nil {
			return err
		}

		id, err := s.repo.In(c).CreatePass(ctx, NewPass{
			TenantID: scope.TenantID, PassID: pass.ID, ContractorID: member.ID,
			OrganizationID: in.OrganizationID, SiteID: scope.SiteID, BadgeNo: in.BadgeNo,
			IsMultiEntry: multi, ValidFrom: day(in.ValidFrom), ValidTo: day(in.ValidTo),
			WorkOrderRef: in.WorkOrderRef, EscortRequired: in.EscortRequired,
			AccessZone: in.AccessZone, Status: status,
			Notes: in.Notes,
		})
		if err != nil {
			return err
		}
		if err := s.repo.In(c).ReplaceWindows(ctx, scope.TenantID, id, schedule); err != nil {
			return err
		}
		if err := s.publisher.Publish(ctx, c, event(scope, eventRequested, id, pass.ID, map[string]any{
			"pass_no": pass.PassNo, "contractor_id": member.ID, "contractor_name": member.Name(),
			"organization_id": in.OrganizationID, "pass_type": in.PassTypeKey,
			"status": string(status), "valid_from": day(in.ValidFrom).Format("2006-01-02"),
			"valid_to": day(in.ValidTo).Format("2006-01-02"), "is_multi_entry": multi,
			"windows": len(schedule),
		})); err != nil {
			return err
		}
		filed, err = s.repo.In(c).PassByID(ctx, scope.TenantID, id)
		return err
	})
	if err != nil {
		return Pass{}, err
	}
	filed.Windows = schedule
	return filed, nil
}

// DecidePass answers a request. Approving turns the pass active; refusing revokes it, which is
// the only refusal the contractor status column has, and Core's own status follows either way
// so the register and the barrier agree.
func (s *Service) DecidePass(ctx context.Context, scope tenancy.Scope, id int64, approve bool,
	note string) (Pass, error) {
	filed, err := s.repo.PassByID(ctx, scope.TenantID, id)
	if err != nil {
		return Pass{}, err
	}
	if filed.Decided() {
		return Pass{}, errs.Conflict("contractor pass " + filed.PassNo + " is already " + filed.Status)
	}

	to, core := StatusActive, passes.StatusApproved
	if !approve {
		to, core = StatusRevoked, passes.StatusRejected
	}
	if !approve && strings.TrimSpace(note) == "" {
		return Pass{}, errs.BadField("note", "say why the pass was refused")
	}
	if approve {
		today := LocalDay(time.Now().UTC(), scope.Timezone)
		expired, err := s.repo.ExpiredMandatory(ctx, scope.TenantID, filed.ContractorID, today)
		if err != nil {
			return Pass{}, err
		}
		if len(expired) > 0 {
			return Pass{}, errs.Conflict(filed.ContractorName + " has an expired " +
				expired[0].TypeKey + "; renew it before approving")
		}
	}
	if _, err := s.passes.Decide(ctx, scope, filed.PassID, core, note); err != nil {
		return Pass{}, err
	}
	if _, err := s.repo.MarkDecided(ctx, scope.TenantID, id, to); err != nil {
		return Pass{}, err
	}
	return s.repo.PassByID(ctx, scope.TenantID, id)
}

// Revoke takes a live pass away for good: a firm lost the contract, a badge was lost, a person
// was replaced. The row stays, because the movements it produced are still history.
func (s *Service) Revoke(ctx context.Context, scope tenancy.Scope, id int64, reason string) (Pass, error) {
	if strings.TrimSpace(reason) == "" {
		return Pass{}, errs.BadField("reason", "say why the pass is being revoked")
	}
	filed, err := s.repo.PassByID(ctx, scope.TenantID, id)
	if err != nil {
		return Pass{}, err
	}
	if !filed.Open() {
		return Pass{}, errs.Conflict("contractor pass " + filed.PassNo + " is " + filed.Status +
			", and only a live pass can be revoked")
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MoveStatus(ctx, scope.TenantID, id,
			Status(filed.Status), StatusRevoked, reason, BlockByDesk)
		if err != nil || !moved {
			return err
		}
		return s.publisher.Publish(ctx, c, event(scope, eventRevoked, id, filed.PassID,
			map[string]any{"pass_no": filed.PassNo, "contractor_id": filed.ContractorID,
				"reason": reason}))
	})
	if err != nil {
		return Pass{}, err
	}
	// A revoked pass is cancelled at Core too, which is what stops a number the guard already
	// has in hand from opening a barrier on its own.
	if err := s.settle(ctx, scope, filed.PassID, passes.StatusCancelled, reason); err != nil {
		return Pass{}, err
	}
	return s.repo.PassByID(ctx, scope.TenantID, id)
}

// Block stops a pass without destroying it. It is the supervisor's answer to a safety
// observation, and it is left alone by the document sweep: only a person lifts this one.
func (s *Service) Block(ctx context.Context, scope tenancy.Scope, id int64, reason string) (Pass, error) {
	if strings.TrimSpace(reason) == "" {
		return Pass{}, errs.BadField("reason", "say why the pass is being blocked")
	}
	filed, err := s.repo.PassByID(ctx, scope.TenantID, id)
	if err != nil {
		return Pass{}, err
	}
	if filed.Status != string(StatusActive) {
		return Pass{}, errs.Conflict("contractor pass " + filed.PassNo + " is " + filed.Status +
			", and only an active pass can be blocked")
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MoveStatus(ctx, scope.TenantID, id,
			StatusActive, StatusBlocked, reason, BlockByDesk)
		if err != nil || !moved {
			return err
		}
		return s.publisher.Publish(ctx, c, event(scope, eventBlocked, id, filed.PassID,
			map[string]any{"pass_no": filed.PassNo, "contractor_id": filed.ContractorID,
				"reason": reason, "source": string(BlockByDesk)}))
	})
	if err != nil {
		return Pass{}, err
	}
	return s.repo.PassByID(ctx, scope.TenantID, id)
}

// Unblock puts a pass back to work. It refuses when the range has closed or the paperwork is
// still bad, because an "unblocked" pass that turns red on the next scan is a lie.
func (s *Service) Unblock(ctx context.Context, scope tenancy.Scope, id int64) (Pass, error) {
	filed, err := s.repo.PassByID(ctx, scope.TenantID, id)
	if err != nil {
		return Pass{}, err
	}
	if filed.Status != string(StatusBlocked) {
		return Pass{}, errs.Conflict("contractor pass " + filed.PassNo + " is " + filed.Status +
			", and only a blocked pass can be released")
	}
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	if !filed.ValidOn(today) {
		return Pass{}, errs.Conflict("contractor pass " + filed.PassNo + " is outside its validity range")
	}
	expired, err := s.repo.ExpiredMandatory(ctx, scope.TenantID, filed.ContractorID, today)
	if err != nil {
		return Pass{}, err
	}
	if len(expired) > 0 {
		return Pass{}, errs.Conflict(filed.ContractorName + " still has an expired " +
			expired[0].TypeKey + "; renew it first")
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MoveStatus(ctx, scope.TenantID, id,
			StatusBlocked, StatusActive, "", BlockByDesk)
		if err != nil {
			return err
		}
		if !moved {
			return errs.Conflict("contractor pass " + filed.PassNo + " changed while this request was in flight")
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "contractor.pass_unblocked",
			SubjectType: "contractor_pass", SubjectID: id, PassID: filed.PassID,
			SiteID: filed.SiteID, Before: map[string]any{"status": filed.Status},
			After: map[string]any{"status": string(StatusActive)},
		})
	})
	if err != nil {
		return Pass{}, err
	}
	return s.repo.PassByID(ctx, scope.TenantID, id)
}

// Sweep is the nightly check: documents that have run out become expired, contractors with a
// mandatory expired document have their live passes blocked, renewals lift exactly the blocks
// the paperwork caused, and passes past their last day become expired.
//
// Every step is a compare-and-set on the status it read, so the sweep is safe to run from a
// cron and from a supervisor's button in the same minute.
func (s *Service) Sweep(ctx context.Context, scope tenancy.Scope) (SweepResult, error) {
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	var out SweepResult

	expiredDocs, err := s.repo.MarkExpiredDocuments(ctx, scope.TenantID, today)
	if err != nil {
		return out, err
	}
	out.DocumentsExpired = expiredDocs

	due, err := s.repo.DocumentsDueForReminder(ctx, scope.TenantID, today, sweepCeiling)
	if err != nil {
		return out, err
	}
	for _, document := range due {
		if err := s.warnDocument(ctx, scope, document); err != nil {
			return out, err
		}
		out.RemindersSent++
	}

	past, err := s.repo.PassesPastValidity(ctx, scope.TenantID, today)
	if err != nil {
		return out, err
	}
	for _, filed := range past {
		if expired, err := s.expirePass(ctx, scope, filed); err != nil {
			return out, err
		} else if expired {
			out.PassesExpired++
		}
	}

	held, err := s.repo.PassesBlockedByDocuments(ctx, scope.TenantID, today)
	if err != nil {
		return out, err
	}
	for _, filed := range held {
		if blocked, err := s.blockPass(ctx, scope, filed, today); err != nil {
			return out, err
		} else if blocked {
			out.PassesBlocked++
		}
	}

	released, err := s.release(ctx, scope, today)
	if err != nil {
		return out, err
	}
	out.PassesReleased = released
	return out, nil
}

// SweepResult is what one sweep came to. It is a count, not a page: a cron job and a desk
// button both only want to know whether anything moved.
type SweepResult struct {
	DocumentsExpired int `json:"documents_expired"`
	RemindersSent    int `json:"reminders_sent"`
	PassesExpired    int `json:"passes_expired"`
	PassesBlocked    int `json:"passes_blocked"`
	PassesReleased   int `json:"passes_released"`
}

// sweepCeiling bounds the reminder batch. A tenant with ten thousand documents about to expire
// is a tenant that should be told once, not ten thousand times in one tick.
const sweepCeiling = 200

func (s *Service) warnDocument(ctx context.Context, scope tenancy.Scope, document Document) error {
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkReminderSent(ctx, scope.TenantID, document.ID)
		if err != nil || !moved {
			return err
		}
		return s.publisher.Publish(ctx, c, event(scope, eventExpiring, document.ContractorID,
			document.ContractorID, map[string]any{
				"contractor_id": document.ContractorID, "document_id": document.ID,
				"document_type": document.TypeKey,
				"expires_on":    expiryOf(document.ExpiresOn),
			}))
	})
}

func (s *Service) expirePass(ctx context.Context, scope tenancy.Scope, filed Pass) (bool, error) {
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		from := Status(filed.Status)
		moved, err := s.repo.In(c).MoveStatus(ctx, scope.TenantID, filed.ID, from,
			StatusExpired, "", BlockByDocument)
		if err != nil || !moved {
			return err
		}
		return s.publisher.Publish(ctx, c, event(scope, eventExpired, filed.ID, filed.PassID,
			map[string]any{"pass_no": filed.PassNo, "contractor_id": filed.ContractorID,
				"valid_to": day(filed.ValidTo).Format("2006-01-02")}))
	})
	if err != nil {
		return false, err
	}
	// The Core pass is closed the same way the desk would close it, so a badge scanned at the
	// gate is refused by Core's own facts rather than by this module's opinion.
	if err := s.settle(ctx, scope, filed.PassID, passes.StatusExpired, "contractor pass expired"); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) blockPass(ctx context.Context, scope tenancy.Scope, filed Pass,
	today time.Time) (bool, error) {
	expired, err := s.repo.ExpiredMandatory(ctx, scope.TenantID, filed.ContractorID, today)
	if err != nil {
		return false, err
	}
	if len(expired) == 0 {
		return false, nil
	}
	reason := "document " + expired[0].TypeKey + " expired on " +
		day(*expired[0].ExpiresOn).Format("2006-01-02")
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MoveStatus(ctx, scope.TenantID, filed.ID,
			StatusActive, StatusBlocked, reason, BlockByDocument)
		if err != nil || !moved {
			return err
		}
		return s.publisher.Publish(ctx, c, event(scope, eventBlocked, filed.ID, filed.PassID,
			map[string]any{"pass_no": filed.PassNo, "contractor_id": filed.ContractorID,
				"contractor_name": filed.ContractorName, "reason": reason,
				"source": string(BlockByDocument)}))
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// release lifts the document blocks whose paperwork is now good again. It runs after the block
// step, so a document re-uploaded inside the same sweep is not un-blocked and re-blocked in one
// pass, and it never touches a block a person filed.
func (s *Service) release(ctx context.Context, scope tenancy.Scope, today time.Time) (int, error) {
	ready, err := s.repo.PassesReadyToUnblock(ctx, scope.TenantID, today)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, filed := range ready {
		moved, err := s.repo.MoveStatus(ctx, scope.TenantID, filed.ID,
			StatusBlocked, StatusActive, "", BlockByDocument)
		if err != nil {
			return released, err
		}
		if moved {
			released++
		}
	}
	return released, nil
}

// reconsider is the same release, narrowed to one contractor, and run when their paperwork is
// verified rather than on the next sweep.
func (s *Service) reconsider(ctx context.Context, scope tenancy.Scope, contractorID int64) error {
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	ready, err := s.repo.PassesReadyToUnblock(ctx, scope.TenantID, today)
	if err != nil {
		return err
	}
	for _, filed := range ready {
		if filed.ContractorID != contractorID {
			continue
		}
		if _, err := s.repo.MoveStatus(ctx, scope.TenantID, filed.ID,
			StatusBlocked, StatusActive, "", BlockByDocument); err != nil {
			return err
		}
	}
	return nil
}

// Check is this module's answer at the barrier, behind Core's eligibility seam. It is asked
// only for passes this module issued, and it says nothing about any other module's rows.
func (s *Service) Check(ctx context.Context, q movements.EligibilityQuery) (string, error) {
	if q.PassID == 0 || q.SourceModuleKey != ModuleKey {
		return "", nil
	}
	filed, err := s.repo.PassByCorePass(ctx, q.TenantID, q.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return "", nil
		}
		return "", err
	}
	loc := clock.Location(q.Timezone)
	at := q.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	today := LocalDay(at, q.Timezone)

	switch Status(filed.Status) {
	case StatusActive:
	case StatusBlocked:
		reason := "a document has expired"
		if filed.BlockedReason != nil {
			reason = *filed.BlockedReason
		}
		return "access blocked: " + reason, nil
	case StatusPending:
		return "the contractor pass is awaiting approval", nil
	case StatusExpired:
		return "the contractor pass has expired", nil
	case StatusRevoked:
		return "the contractor pass was revoked", nil
	default:
		return "the contractor pass is " + filed.Status, nil
	}
	if !filed.ValidOn(today) {
		return "outside the contractor pass's validity range", nil
	}
	windows, err := s.repo.Windows(ctx, q.TenantID, filed.ID)
	if err != nil {
		return "", err
	}
	if !windows.Permits(at.In(loc), q.GateID) {
		return "outside the contractor's allowed hours at this gate", nil
	}
	expired, err := s.repo.ExpiredMandatory(ctx, q.TenantID, filed.ContractorID, today)
	if err != nil {
		return "", err
	}
	if len(expired) > 0 {
		return "document " + expired[0].TypeKey + " expired on " +
			day(*expired[0].ExpiresOn).Format("2006-01-02"), nil
	}
	return "", nil
}

// schedule validates the desk's rota and turns it into rows.
func (s *Service) schedule(ctx context.Context, scope tenancy.Scope,
	inputs []WindowInput) (Schedule, error) {
	out := make(Schedule, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for i, in := range inputs {
		field := fmt.Sprintf("windows[%d]", i)
		if in.DayOfWeek < 0 || in.DayOfWeek > 7 {
			return nil, errs.BadField(field+".day_of_week", "expected 1 (Monday) to 7 (Sunday), or 0 for every day")
		}
		starts, err := NormaliseClock(field+".starts_at", in.StartsAt)
		if err != nil {
			return nil, err
		}
		closes, err := NormaliseClock(field+".closes_at", in.ClosesAt)
		if err != nil {
			return nil, err
		}
		if in.GateID > 0 {
			found, err := s.repo.GateOnSite(ctx, scope.TenantID, scope.SiteID, in.GateID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, errs.BadField(field+".gate_id", "not a gate on this site")
			}
		}
		key := fmt.Sprintf("%d|%d|%s|%s", in.GateID, in.DayOfWeek, starts, closes)
		if seen[key] {
			continue
		}
		seen[key] = true

		opens, _ := Clock(starts)
		ends, _ := Clock(closes)
		window := Window{
			TenantID: scope.TenantID, DayOfWeek: in.DayOfWeek,
			StartsAt: starts, ClosesAt: closes, CrossesMidnight: ends <= opens,
		}
		if in.GateID > 0 {
			gate := in.GateID
			window.GateID = &gate
		}
		out = append(out, window)
	}
	return out, nil
}

// settle mirrors a module state onto Core's spine. A conflict means another writer already
// moved it, which is the outcome this call wanted anyway.
func (s *Service) settle(ctx context.Context, scope tenancy.Scope, passID int64, to passes.Status,
	note string) error {
	current, err := s.passRepo.ByID(ctx, scope.TenantID, passID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil
		}
		return err
	}
	if !current.Decidable() || current.Status == string(to) {
		return nil
	}
	if _, err := s.passes.Decide(ctx, scope, passID, to, note); err != nil {
		if errs.CodeOf(err) == errs.CodeConflict {
			return nil
		}
		return err
	}
	return nil
}

// LocalDay is today's calendar day on the site's wall clock, held at UTC midnight so it can be
// compared with the DATE columns as they read back.
func LocalDay(at time.Time, timezone string) time.Time {
	return day(at.In(clock.Location(timezone)))
}

func expiryOf(value *time.Time) any {
	if value == nil {
		return nil
	}
	return day(*value).Format("2006-01-02")
}

// defaultMultiEntry makes a long pass multi-entry and a day or service pass single, which is
// what the two catalogue rows are for. The desk can still overrule either.
func defaultMultiEntry(passType string) bool { return passType == PassTypeRecurring }

// formatFor keeps service engineers in the service series, so a tenant reading its register can
// tell a service attendance from a month-long site pass by its number.
func formatFor(passType string) string {
	if passType == PassTypeService {
		return "service"
	}
	return numberFormat
}

func purposeOf(passType string, member Contractor) string {
	name := "Contractor access"
	if passType == PassTypeService {
		name = "Service attendance"
	}
	if member.OrganizationName != "" {
		return name + " · " + member.OrganizationName
	}
	return name
}

func event(scope tenancy.Scope, key string, id, passID int64, payload map[string]any) events.Event {
	return events.Event{
		TenantID: scope.TenantID, EventKey: key, AggregateType: aggregateType,
		AggregateID: id, PassID: passID, SiteID: scope.SiteID, GateID: scope.GateID,
		ActorUserID: scope.UserID, Payload: payload,
	}
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func timePtr(value time.Time) *time.Time { return &value }

func num(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
