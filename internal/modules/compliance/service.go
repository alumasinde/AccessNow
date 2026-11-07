package compliance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Service is the compliance desk: it configures what may be purged, records what was purged,
// checks the trail, and keeps a hold able to stop a purge. It never reads the PII it blanks.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	access *access.Service
	events events.Publisher
	chain  audit.Writer
	now    func() time.Time
}

func NewService(pool *sqlx.DB, repo *Repository, accessSvc *access.Service, bus events.Publisher,
	chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, access: accessSvc, events: bus, chain: chain,
		now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// Policies is the retention rulebook.
func (s *Service) Policies(ctx context.Context, tenantID int64) ([]Policy, error) {
	return s.repo.Policies(ctx, tenantID)
}

// SavePolicy creates or replaces one category's rule. Anonymise is refused for a category that
// carries no value to hash, which would otherwise store a rule that quietly behaves as null.
func (s *Service) SavePolicy(ctx context.Context, scope tenancy.Scope,
	req PolicyRequest) (Policy, error) {
	category := Category(strings.TrimSpace(req.DataCategory))
	if !category.Valid() {
		return Policy{}, errs.BadField("data_category", "is not a category this module can purge")
	}
	action := PurgeAction(strings.TrimSpace(req.PurgeAction))
	if action == "" {
		action = ActionNull
	}
	if !action.Valid() {
		return Policy{}, errs.BadField("purge_action",
			"must be null, anonymise, delete or archive")
	}
	if !category.Supports(action) {
		return Policy{}, errs.BadField("purge_action", actionRefusal(category, action))
	}
	if req.RetentionDays < 1 || req.RetentionDays > 3650 {
		return Policy{}, errs.BadField("retention_days", "must be between 1 and 3650")
	}
	if req.NotifyBeforeDays != nil && (*req.NotifyBeforeDays < 0 || *req.NotifyBeforeDays > 90) {
		return Policy{}, errs.BadField("notify_before_days", "must be between 0 and 90")
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}

	now := s.now()
	policy := Policy{
		TenantID: scope.TenantID, DataCategory: string(category),
		RetentionDays: req.RetentionDays, PurgeAction: string(action),
		NotifyBeforeDays: req.NotifyBeforeDays, IsActive: active, UpdatedBy: optionalID(scope.UserID),
	}
	var id int64
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		saved, err := s.repo.In(c).UpsertPolicy(ctx, policy, now)
		if err != nil {
			return err
		}
		id = saved
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "compliance.policy_saved",
			SubjectType: "retention_policy", SubjectID: saved, SiteID: scope.SiteID,
			After: map[string]any{"data_category": string(category),
				"retention_days": req.RetentionDays, "purge_action": string(action),
				"is_active": active},
		})
	})
	if err != nil {
		return Policy{}, err
	}
	return s.repo.Policy(ctx, scope.TenantID, id)
}

// Holds lists the cases protecting data, after closing the ones whose own expiry has passed.
func (s *Service) Holds(ctx context.Context, tenantID int64, status string) ([]Hold, error) {
	now := s.now()
	if _, err := s.repo.ExpireHolds(ctx, tenantID, now); err != nil {
		return nil, err
	}
	return s.repo.Holds(ctx, tenantID, status, 100)
}

// PlaceHold stops a purge from touching one record. The row a hold names does not have to
// exist yet - a case can be opened about a visit that is still being processed - but the
// reference has to be specific, or the hold protects nothing in particular.
func (s *Service) PlaceHold(ctx context.Context, scope tenancy.Scope, req HoldRequest) (Hold, error) {
	entityType := strings.TrimSpace(req.EntityType)
	if !knownEntityType(entityType) {
		return Hold{}, errs.BadField("entity_type", "is not a record type a hold can name")
	}
	if req.EntityID <= 0 {
		return Hold{}, errs.BadField("entity_id", "must be a positive id")
	}
	caseRef := strings.TrimSpace(req.CaseRef)
	if caseRef == "" {
		return Hold{}, errs.BadField("case_ref", "is required")
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) < 10 {
		return Hold{}, errs.BadField("reason", "must say why the record is being kept")
	}
	now := s.now()
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		return Hold{}, errs.BadField("expires_at", "must be in the future")
	}
	hold := Hold{
		TenantID: scope.TenantID, EntityType: entityType, EntityID: req.EntityID,
		PassID: optionalPtr(req.PassID), CaseRef: caseRef, Reason: reason,
		PlacedBy: optionalID(scope.UserID), ExpiresAt: req.ExpiresAt,
	}

	var id int64
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).InsertHold(ctx, hold, now)
		if err != nil {
			return err
		}
		id = filed
		if err := s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "compliance.hold_placed",
			SubjectType: "legal_hold", SubjectID: filed, PassID: valueOrZero(hold.PassID),
			SiteID: scope.SiteID, Reason: reason,
			After: map[string]any{"entity_type": entityType, "entity_id": req.EntityID,
				"case_ref": caseRef, "expires_at": req.ExpiresAt},
		}); err != nil {
			return err
		}
		return s.announce(ctx, c, scope.TenantID, filed, caseRef, entityType, req.EntityID)
	})
	if err != nil {
		return Hold{}, err
	}
	return s.repo.Hold(ctx, scope.TenantID, id)
}

// ReleaseHold ends a hold with a reason. It is refused when the hold already ended: writing a
// second release reason over the first would rewrite why the case closed.
func (s *Service) ReleaseHold(ctx context.Context, scope tenancy.Scope, id int64,
	req ReleaseRequest) (Hold, error) {
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return Hold{}, errs.BadField("reason", "is required to release a hold")
	}
	now := s.now()
	var released bool
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).ReleaseHold(ctx, scope.TenantID, id, scope.UserID, reason, now)
		if err != nil {
			return err
		}
		released = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "compliance.hold_released",
			SubjectType: "legal_hold", SubjectID: id, SiteID: scope.SiteID, Reason: reason,
			After: map[string]any{"status": HoldReleased},
		})
	})
	if err != nil {
		return Hold{}, err
	}
	if !released {
		hold, err := s.repo.Hold(ctx, scope.TenantID, id)
		if err != nil {
			return Hold{}, err
		}
		return Hold{}, errs.Conflict("hold is already " + hold.Status)
	}
	return s.repo.Hold(ctx, scope.TenantID, id)
}

// Verify walks the tenant's audit chain and records the answer. It reads the rows through the
// same hash formula the writer used, so a row that was edited after the fact is a mismatch on
// the row that changed, not on every row after it.
func (s *Service) Verify(ctx context.Context, scope tenancy.Scope,
	req VerifyRequest) (Verification, error) {
	started := s.now()
	from := uint64(0)
	to := uint64(^uint64(0))
	if req.FromSeq != nil {
		from = *req.FromSeq
	}
	if req.ToSeq != nil {
		to = *req.ToSeq
	}
	if to < from {
		return Verification{}, errs.BadField("to_seq", "must not be before from_seq")
	}

	rows, err := s.repo.AuditRows(ctx, scope.TenantID, from, to, verifyBatch)
	if err != nil {
		return Verification{}, err
	}
	finding := audit.VerifyChain(rows)

	outcome := OutcomeVerified
	switch {
	case len(rows) == 0:
		outcome = OutcomeIncomplete
	case !finding.OK:
		outcome = OutcomeMismatch
	}

	now := s.now()
	duration := int(now.Sub(started).Milliseconds())
	verification := Verification{
		TenantID: scope.TenantID, StreamKey: "audit", FromSeq: from, ToSeq: to,
		RowsChecked: int64(len(rows)), Outcome: outcome, DurationMS: &duration,
		CheckedBy: optionalID(scope.UserID), Note: req.Note, CheckedAt: now,
	}
	if outcome == OutcomeMismatch {
		seq := finding.FirstMismatchSeq
		hash := finding.FirstMismatchHash
		verification.FirstMismatchSeq = &seq
		verification.FirstMismatchHash = &hash
	}
	if finding.TipHash != "" {
		tip := finding.TipHash
		verification.TipHash = &tip
	}

	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		saved, err := s.repo.In(c).InsertVerification(ctx, verification, now)
		if err != nil {
			return err
		}
		id = saved
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "compliance.audit_verified", SubjectType: "audit_verification",
			SubjectID: saved, SiteID: scope.SiteID, After: map[string]any{
				"outcome": outcome, "rows": len(rows), "from_seq": from, "to_seq": to,
			},
		})
	})
	if err != nil {
		return Verification{}, err
	}
	verification.ID = id
	verification.CreatedAt = now
	return verification, nil
}

func (s *Service) Verifications(ctx context.Context, tenantID int64) ([]Verification, error) {
	return s.repo.Verifications(ctx, tenantID, 50)
}

// Purge applies one category's retention policy. A dry run counts what would go without
// touching a row; a real run needs the module enabled for the tenant and refuses to touch a row
// a legal hold protects. Every run is on the ledger whether or not it changed anything.
func (s *Service) Purge(ctx context.Context, scope tenancy.Scope, req PurgeRequest) (PurgeRun, error) {
	on, err := s.enabled(ctx, scope.TenantID)
	if err != nil {
		return PurgeRun{}, err
	}
	if !on {
		return PurgeRun{}, errs.ModuleDisabled(ModuleKey)
	}
	category := Category(strings.TrimSpace(req.DataCategory))
	if !category.Valid() {
		return PurgeRun{}, errs.BadField("data_category", "is not a category this module can purge")
	}

	policy, err := s.repo.PolicyFor(ctx, scope.TenantID, string(category))
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return PurgeRun{}, errs.BadField("data_category",
				"has no retention policy; set one before purging")
		}
		return PurgeRun{}, err
	}
	if !policy.IsActive {
		return PurgeRun{}, errs.BadField("data_category", "its retention policy is switched off")
	}
	action := PurgeAction(policy.PurgeAction)
	if !action.Valid() || !category.Supports(action) {
		return PurgeRun{}, errs.BadField("data_category", "its policy names an action this module cannot take")
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}

	now := s.now()
	cutoff := now.AddDate(0, 0, -policy.RetentionDays)
	runID, err := s.repo.InsertPurgeRun(ctx, PurgeRun{
		TenantID: scope.TenantID, PolicyID: &policy.ID, DataCategory: string(category),
		PurgeAction: string(action), IsDryRun: dryRun, CutoffAt: &cutoff,
		RequestedBy: optionalID(scope.UserID),
	}, now)
	if err != nil {
		return PurgeRun{}, err
	}

	var scanned, purged, onHold int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		candidates, err := s.repo.Candidates(ctx, category, scope.TenantID, cutoff, purgeBatch)
		if err != nil {
			return err
		}
		scanned = int64(len(candidates))
		for _, candidate := range candidates {
			held, err := repo.OnHold(ctx, scope.TenantID, category.entityType(), candidate.EntityID, now)
			if err != nil {
				return err
			}
			if held {
				onHold++
				continue
			}
			if dryRun {
				continue
			}
			if action == ActionDelete {
				if err := repo.Delete(ctx, category, scope.TenantID, candidate.EntityID); err != nil {
					return err
				}
			} else {
				if err := repo.Apply(ctx, category, scope.TenantID, candidate.EntityID, action, now); err != nil {
					return err
				}
				if action == ActionArchive {
					if err := repo.InsertArchive(ctx, Archive{
						TenantID: scope.TenantID, PurgeRunID: &runID,
						DataCategory: string(category), EntityType: candidate.EntityType,
						EntityID: candidate.EntityID, ValueHash: archiveHash(candidate.Value),
						ArchivedAt: now,
					}, now); err != nil {
						return err
					}
				}
			}
			purged++
		}
		if !dryRun {
			if err := repo.TouchPolicy(ctx, scope.TenantID, policy.ID, now); err != nil {
				return err
			}
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "compliance.purge_run",
			SubjectType: "data_purge_run", SubjectID: runID, SiteID: scope.SiteID,
			After: map[string]any{
				"data_category": string(category), "purge_action": string(action),
				"dry_run": dryRun, "cutoff": cutoff, "scanned": scanned,
				"purged": purged, "on_hold": onHold,
			},
		})
	})
	if err != nil {
		message := err.Error()
		if failErr := s.repo.FinishPurgeRun(ctx, scope.TenantID, runID, scanned, purged, onHold,
			PurgeFailed, &message, s.now()); failErr != nil {
			logging.From(ctx).ErrorContext(ctx, "could not record failed purge run",
				"run_id", runID, "error", failErr)
		}
		return PurgeRun{}, err
	}

	if err := s.repo.FinishPurgeRun(ctx, scope.TenantID, runID, scanned, purged, onHold,
		PurgeSucceeded, nil, s.now()); err != nil {
		return PurgeRun{}, err
	}

	return PurgeRun{
		ID: runID, TenantID: scope.TenantID, PolicyID: &policy.ID,
		DataCategory: string(category), PurgeAction: string(action), IsDryRun: dryRun,
		Status: PurgeSucceeded, RowsScanned: scanned, RowsPurged: purged, RowsOnHold: onHold,
		CutoffAt: &cutoff, StartedAt: &now, RequestedBy: optionalID(scope.UserID),
	}, nil
}

func (s *Service) PurgeRuns(ctx context.Context, tenantID int64) ([]PurgeRun, error) {
	return s.repo.PurgeRuns(ctx, tenantID, 50)
}

// announce puts the hold on the outbox so the people who need to know a case is protecting a
// record hear about it without reading the compliance screen every morning.
func (s *Service) announce(ctx context.Context, c db.Conn, tenantID, holdID int64, caseRef,
	entityType string, entityID int64) error {
	if s.events == nil {
		return nil
	}
	return s.events.Publish(ctx, c, events.Event{
		TenantID: tenantID, EventKey: "compliance.hold_placed", AggregateType: aggregateType,
		AggregateID: holdID, DedupeKey: fmt.Sprintf("compliance-hold:%d", holdID),
		Payload: map[string]any{"case_ref": caseRef, "entity_type": entityType,
			"entity_id": entityID},
	})
}

func optionalID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func optionalPtr(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

func valueOrZero(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// actionRefusal says why a category cannot take an action, in the words a desk needs.
func actionRefusal(category Category, action PurgeAction) string {
	switch action {
	case ActionAnonymise:
		return string(category) + " has no value to anonymise; use null, delete or archive"
	case ActionDelete:
		return "delete removes the record, and only an attachment is a record whose whole point " +
			"is the personal data; use null or archive for " + string(category)
	}
	return string(category) + " cannot take the " + string(action) + " action"
}

// archiveHash is the receipt's proof of what was erased. It is a digest of the value, never the
// value: the archive is a record that erasure happened, not a second copy.
func archiveHash(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(value))
	digest := hex.EncodeToString(sum[:])
	return &digest
}

// knownEntityType closes the vocabulary of things a hold can protect.
func knownEntityType(value string) bool {
	switch value {
	case "passes", "visitors", "gate_movements", "notifications", "staff", "vehicles",
		"contractor_documents", "pass_attachments", "visits":
		return true
	}
	return false
}
