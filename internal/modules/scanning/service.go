package scanning

import (
	"context"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/movements"
	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/security"
	"accessnow/internal/platform/tenancy"
)

// Service owns the tokens a device reads and the log of every read. It answers two callers:
// the desk, which issues and revokes badges, and Core's barrier, which asks what a scan meant.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	passes *passes.Repository
	access *access.Service
	chain  audit.Writer
	events events.Publisher
}

func NewService(pool *sqlx.DB, repo *Repository, passRepo *passes.Repository,
	accessSvc *access.Service, chain audit.Writer, bus events.Publisher) *Service {
	return &Service{pool: pool, repo: repo, passes: passRepo, access: accessSvc,
		chain: chain, events: bus}
}

func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// Issue mints one badge for a pass that can actually open a barrier. The secret is returned
// once and never stored: the row keeps a SHA-256 of it, so a database copy is a list of hashes
// rather than a stack of usable badges.
func (s *Service) Issue(ctx context.Context, scope tenancy.Scope, req IssueRequest) (Issued, error) {
	if err := req.validate(); err != nil {
		return Issued{}, err
	}
	pass, err := s.passes.ByID(ctx, scope.TenantID, req.PassID)
	if err != nil {
		return Issued{}, err
	}
	if !pass.Usable() {
		return Issued{}, errs.Conflict(pass.PassNo + " is " + pass.Status +
			"; a token can only be issued for a pass that may open a barrier")
	}

	now := time.Now().UTC()
	expires := req.ExpiresAt
	if expires == nil {
		// A badge that outlives its pass is a badge a visitor can show after their permit has
		// run out, so the pass's own end is the default. An open-ended pass gets an open token,
		// which is what the desk means when it files one.
		expires = pass.ValidUntil
	}
	if expires != nil && !expires.After(now) {
		return Issued{}, errs.BadField("expires_at", "a token that has already expired is not a token")
	}
	uses := req.MaxUses
	if req.SingleUse {
		uses = intPtr(1)
	}

	secret, err := NewSecret()
	if err != nil {
		return Issued{}, err
	}
	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		created, err := s.repo.Create(ctx, c, NewToken{
			TenantID: scope.TenantID, PassID: pass.ID,
			TokenHash: security.HashToken(secret), TokenPrefix: PrefixOf(secret),
			Format: string(formatOf(req.Format)), ExpiresAt: expires, MaxUses: uses,
			SingleUse: req.SingleUse, IssuedBy: scope.UserID, Delivery: req.Delivery,
		})
		if err != nil {
			return err
		}
		id = created
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "scan.token_issued",
			SubjectType: aggregateType, SubjectID: id, PassID: pass.ID, SiteID: pass.SiteID,
			After: map[string]any{"prefix": PrefixOf(secret), "format": formatOf(req.Format),
				"single_use": req.SingleUse, "pass_no": pass.PassNo},
		})
	})
	if err != nil {
		return Issued{}, err
	}
	token, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Issued{}, err
	}
	return Issued{Token: token, Secret: secret}, nil
}

// Revoke stops a badge that is still in somebody's hand. A revoked token keeps the reason it was
// revoked for, because the second supervisor to ask "why did this card stop working" deserves
// the first one's answer.
func (s *Service) Revoke(ctx context.Context, scope tenancy.Scope, id int64, reason string) (Token, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Token{}, errs.BadField("reason", "say why the token is being revoked")
	}
	if len(reason) > 255 {
		return Token{}, errs.BadField("reason", "too long")
	}
	current, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Token{}, err
	}
	if current.RevokedAt != nil {
		return Token{}, errs.Conflict("token " + current.TokenPrefix + " was already revoked")
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		if _, err := s.repo.Revoke(ctx, c, scope.TenantID, id, reason); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "scan.token_revoked",
			SubjectType: aggregateType, SubjectID: id, PassID: current.PassID,
			SiteID: scope.SiteID, Reason: reason,
			Before: map[string]any{"revoked": false}, After: map[string]any{"revoked": true},
		})
	})
	if err != nil {
		return Token{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Resolve is the barrier's question. A token that cannot be honoured is a refusal with a
// sentence the guard can read out, never an error: the guard is standing in front of somebody
// and needs a colour, not a stack trace.
func (s *Service) Resolve(ctx context.Context, q movements.TokenQuery) (movements.TokenVerdict, error) {
	verdict := movements.TokenVerdict{Input: string(inputOf(q.Input))}
	secret := strings.TrimSpace(q.Token)
	if secret == "" {
		verdict.Refusal = "nothing was read"
		return verdict, nil
	}
	on, err := s.enabled(ctx, q.TenantID)
	if err != nil {
		return verdict, err
	}
	if !on {
		verdict.Refusal = "QR scanning is not enabled at this site"
		return verdict, nil
	}
	// A QR code can carry a kilobyte of anything. A real secret is 32 characters, so a payload
	// long enough to be an attack is refused before it is hashed and looked up.
	if len(secret) > 256 {
		verdict.Refusal = "that token is not on file"
		return verdict, nil
	}
	token, err := s.repo.ByHash(ctx, q.TenantID, security.HashToken(secret))
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			verdict.Refusal = "that token is not on file"
			return verdict, nil
		}
		return verdict, err
	}
	verdict.PassID = token.PassID
	verdict.TokenID = token.ID
	verdict.Input = string(inputOf(q.Input))
	if refusal := token.Refusal(q.At); refusal != "" {
		verdict.Refusal = refusal
	}
	return verdict, nil
}

// Record files the read. It is asked after the barrier already has its colour, so a failure is
// logged and never rewrites the answer the guard was given.
func (s *Service) Record(ctx context.Context, a movements.Attempt) error {
	if a.TenantID == 0 || a.GateID == 0 {
		return nil
	}
	outcome, code := classify(a)
	when := a.OccurredAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	in := NewAttempt{
		TenantID: a.TenantID, SiteID: a.SiteID, GateID: a.GateID, DeviceID: a.DeviceID,
		GateSessionID: a.GateSessionID, InputKind: string(inputOf(a.Input)),
		Plate: a.Plate, Outcome: outcome, ResponseCode: code, ResponseText: a.Reason,
		PassID: a.PassID, TokenID: a.TokenID, MovementID: a.MovementID,
		Direction: string(a.Direction), OccurredAt: when, IdemKey: a.IdempotencyKey,
	}
	if a.Token != "" {
		// The raw read never lands in the log. The hash matches it back to a badge, and the
		// prefix is what a supervisor can compare against the card in their hand.
		in.TokenHash = security.HashToken(strings.TrimSpace(a.Token))
		in.TokenPrefix = PrefixOf(strings.TrimSpace(a.Token))
	}
	if _, err := s.repo.InsertAttempt(ctx, s.pool, in); err != nil {
		return err
	}
	// Spend a use only when the barrier opened for the token. A refused read must not burn a
	// single-use badge, or a camera misread would cost somebody their badge.
	if a.TokenID != 0 && a.Result != movements.ResultDenied {
		claimed, err := s.repo.ClaimUse(ctx, s.pool, a.TenantID, a.TokenID, a.GateID, when)
		if err != nil {
			return err
		}
		if !claimed {
			logging.From(ctx).WarnContext(ctx, "token was no longer spendable after the movement",
				"token_id", a.TokenID, "pass_no", a.PassNo)
		}
	}
	return nil
}

// OnPassApproved puts a badge on a pass the moment it is approved, so the guard at the barrier
// has something to scan without the desk minting one first.
func (s *Service) OnPassApproved(ctx context.Context, e events.Event) error {
	return s.issueForPass(ctx, e)
}

// OnPassIssued is the same courtesy for passes that are born usable - an auto-approved service
// visit never goes through Decide, so it never publishes pass.approved at all.
func (s *Service) OnPassIssued(ctx context.Context, e events.Event) error {
	return s.issueForPass(ctx, e)
}

// SettledKeys are the ends of a pass. A badge that survives its pass is the one failure this
// module must never allow, so every terminal status is subscribed to.
func SettledKeys() []string {
	return []string{"pass.cancelled", "pass.completed", "pass.expired", "pass.rejected", "pass.void"}
}

// OnPassSettled takes every live badge off a pass that has ended.
func (s *Service) OnPassSettled(ctx context.Context, e events.Event) error {
	if e.TenantID == 0 || e.PassID == 0 {
		return nil
	}
	status := strings.TrimPrefix(e.EventKey, "pass.")
	changed, err := s.repo.RevokeForPass(ctx, s.pool, e.TenantID, e.PassID,
		"the pass is "+status)
	if err != nil {
		return err
	}
	if changed > 0 {
		logging.From(ctx).InfoContext(ctx, "revoked badges of a settled pass",
			"pass_id", e.PassID, "tokens", changed)
	}
	return nil
}

// issueForPass is the automatic half of Issue: idempotent because the relay delivers at least
// once, and quiet when the tenant has the module switched off or the pass cannot be used.
func (s *Service) issueForPass(ctx context.Context, e events.Event) error {
	if e.TenantID == 0 || e.PassID == 0 {
		return nil
	}
	on, err := s.enabled(ctx, e.TenantID)
	if err != nil {
		return err
	}
	if !on {
		return nil
	}
	pass, err := s.passes.ByID(ctx, e.TenantID, e.PassID)
	if err != nil {
		return err
	}
	if !pass.Usable() {
		return nil
	}
	live, err := s.repo.HasLive(ctx, e.TenantID, pass.ID, time.Now().UTC())
	if err != nil {
		return err
	}
	if live {
		return nil
	}
	scope := tenancy.Scope{TenantID: e.TenantID, UserID: e.ActorUserID, SiteID: e.SiteID,
		GateID: e.GateID}
	if scope.SiteID == 0 {
		scope.SiteID = pass.SiteID
	}
	_, err = s.Issue(ctx, scope, IssueRequest{PassID: pass.ID})
	return err
}

// classify is the log's vocabulary. The pass ids and the movement id say what happened; this
// says which of the schema's dozen outcomes to file it under, and the code the device shows.
func classify(a movements.Attempt) (outcome, code string) {
	switch a.Result {
	case movements.ResultGranted:
		return "granted", "GRANTED"
	case movements.ResultWarned:
		return "warned", "WARNED"
	case movements.ResultOverride:
		return "override", "OVERRIDE"
	}
	reason := strings.ToLower(a.Reason)
	switch {
	case strings.Contains(reason, "not on file"):
		return "unknown_token", "UNKNOWN_TOKEN"
	case strings.Contains(reason, "revoked"):
		return "revoked", "TOKEN_REVOKED"
	case strings.Contains(reason, "expired"):
		return "expired", "TOKEN_EXPIRED"
	case strings.Contains(reason, "already been used"):
		return "already_used", "TOKEN_USED"
	case strings.Contains(reason, "not valid at this gate"):
		return "wrong_gate", "WRONG_GATE"
	case strings.Contains(reason, "outside"):
		return "out_of_window", "OUT_OF_WINDOW"
	case strings.Contains(reason, "not enabled"):
		return "denied", "SCANNING_DISABLED"
	default:
		return "denied", "DENIED"
	}
}

func intPtr(value int) *int { return &value }
