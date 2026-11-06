package blocklist

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/movements"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// Service is the list itself: the gate asks it before a barrier opens, and the desk files,
// reads and retires entries through it. It never writes a pass or a movement - those are
// Core's rows - and it only ever reads the scan's own facts.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	access *access.Service
	events events.Publisher
	chain  audit.Writer
}

func NewService(pool *sqlx.DB, repo *Repository, accessSvc *access.Service,
	bus events.Publisher, chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, access: accessSvc, events: bus, chain: chain}
}

// enabled is the switch that keeps the module optional. With it off the gate gets an empty
// verdict and the desk surface is not mounted at all, so a site that screens some buildings
// and not others is a tenant flag rather than a deployment.
func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// identifier is one thing about the person or vehicle in front of a barrier that a listing
// can be keyed on.
type identifier struct {
	kind  MatchType
	value string
}

// identifiers is every column of the screening question that can be asked, ordered by what a
// guard is actually looking at: the plate on the windscreen, then the phone on the pass, then
// the name on the document. An empty column is not asked at all, because hashing "" would
// make every listing keyed on a blank value match every pass.
func identifiers(q movements.ScreenQuery) []identifier {
	candidates := []identifier{
		{MatchPlate, q.Plate},
		{MatchPhone, q.Phone},
		{MatchEmail, q.Email},
		{MatchNameHash, q.SubjectName},
	}
	out := make([]identifier, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.value) != "" {
			out = append(out, candidate)
		}
	}
	return out
}

// Screen answers the one question a barrier cannot answer for itself. A read that fails is
// an error rather than an empty verdict, because a list that could not be read is not a list
// with nothing on it: Core turns the error into a shut gate with a reason the guard can act
// on.
//
// The first blocklist match decides the colour. A watchlist match never does: it rides along
// in the verdict so Note can file it and raise the quiet alarm after the gate has opened.
func (s *Service) Screen(ctx context.Context, q movements.ScreenQuery) (movements.Verdict, error) {
	on, err := s.enabled(ctx, q.TenantID)
	if err != nil {
		return movements.Verdict{}, err
	}
	if !on {
		return movements.Verdict{}, nil
	}

	now := time.Now().UTC()
	matches := make([]movements.Match, 0, len(identifiers(q)))
	blocked := false
	reason := ""

	for _, id := range identifiers(q) {
		entry, err := s.repo.FindByHash(ctx, q.TenantID, id.kind, hashOf(normalize(id.kind, id.value)), now)
		if errs.CodeOf(err) == errs.CodeNotFound {
			continue
		}
		if err != nil {
			return movements.Verdict{}, err
		}
		matches = append(matches, movements.Match{
			EntryID: entry.ID, ListType: entry.ListType,
			MatchType: entry.MatchType, MatchValue: entry.MatchValue,
		})
		if entry.Blocked() && !blocked && q.Direction != movements.DirectionOut {
			// A listing refuses an entry; it does not refuse an exit. Somebody already on site
			// has to be able to leave, and a denied exit would strand them behind an open
			// movement row. The hit is filed either way, so the supervisor still sees the
			// banned person walk out of the gate.
			blocked = true
			reason = clip("on the blocklist: "+entry.Reason, reasonRoom)
		}
	}
	if len(matches) == 0 {
		return movements.Verdict{}, nil
	}
	return movements.Verdict{Blocked: blocked, Reason: reason, Matches: matches}, nil
}

// Note files what the gate finally did, once per listing that matched. It runs after the
// movement is committed, in its own transaction: the guard already has his colour, so a
// failure here is logged by Core and never turns a green light into an error page.
//
// The entry is re-read rather than trusted from the verdict, because the desk may have
// revoked it in the seconds since the scan and the hit row has to describe the listing that
// actually matched, including the reason and the supervisor it was meant to reach.
func (s *Service) Note(ctx context.Context, verdict movements.Verdict, ref movements.HitRef) error {
	if len(verdict.Matches) == 0 {
		return nil
	}
	on, err := s.enabled(ctx, ref.TenantID)
	if err != nil {
		return err
	}
	if !on {
		return nil
	}

	now := time.Now().UTC()
	action := actionOf(ref.Result)
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		for _, match := range verdict.Matches {
			entry, err := repo.ByID(ctx, ref.TenantID, match.EntryID)
			if err != nil {
				return err
			}
			hitID, err := repo.RecordHit(ctx, Hit{
				TenantID: ref.TenantID, EntryID: entry.ID, MatchedOn: entry.MatchType,
				MatchedValue: optionalText(entry.MatchValue),
				PassID:       optionalID(ref.PassID), MovementID: optionalID(ref.MovementID),
				GateID: optionalID(ref.GateID), GateSession: optionalID(ref.GateSessionID),
				ActionTaken: action,
			}, now)
			if err != nil {
				return err
			}
			if err := repo.Touch(ctx, ref.TenantID, entry.ID, now); err != nil {
				return err
			}
			if err := s.announce(ctx, c, ref, entry, hitID, action); err != nil {
				return err
			}
		}
		return nil
	})
}

// actionOf turns what the gate did into the word the hit row is filed under. The gate's own
// result wins for a refusal, a warning and a deliberate override; everything else is a
// listing that was let through with an alarm, which is the only remaining thing a hit can be.
func actionOf(result movements.Result) string {
	switch result {
	case movements.ResultDenied:
		return "denied"
	case movements.ResultWarned:
		return "warned"
	case movements.ResultOverride:
		return "override_granted"
	default:
		return "allowed_with_alert"
	}
}

// announce is the quiet alarm. It rides the outbox in the same transaction as the hit, so a
// supervisor is told even if this process dies before the notice goes out, and a tenant that
// has notifications switched off simply has an event key with no subscriber.
func (s *Service) announce(ctx context.Context, c db.Conn, ref movements.HitRef, entry Entry,
	hitID int64, action string) error {
	if s.events == nil {
		return nil
	}
	key := "blocklist.watched_hit"
	if action == "denied" {
		key = "blocklist.denied_entry"
	}
	payload := map[string]any{
		"hit_id": hitID, "entry_id": entry.ID, "list_type": entry.ListType,
		"severity": entry.Severity, "reason": entry.Reason, "match_type": entry.MatchType,
		"matched_value": entry.MatchValue, "action_taken": action,
		"pass_no": ref.PassNo, "subject_name": ref.SubjectName, "gate_reason": ref.Reason,
	}
	// A vehicle listing's value is the plate, and {{plate}} is the word the template needs:
	// "KCA123M was refused" reads differently from "a person was refused".
	if MatchType(entry.MatchType) == MatchPlate {
		payload["plate"] = entry.MatchValue
	}
	if entry.AlertRoleKey != nil && *entry.AlertRoleKey != "" {
		payload["alert_role_key"] = *entry.AlertRoleKey
	}
	return s.events.Publish(ctx, c, events.Event{
		TenantID: ref.TenantID, EventKey: key, AggregateType: aggregateType,
		AggregateID: entry.ID, PassID: ref.PassID, SiteID: ref.SiteID, GateID: ref.GateID,
		ActorUserID: ref.ActorUserID,
		DedupeKey:   "blocklist-hit:" + strconv.FormatInt(hitID, 10),
		Payload:     payload,
	})
}

// The desk.

// validate checks the listing and fills in the one default it owns. It takes a pointer
// because it writes: a value receiver would set severity to high on its own copy and hand
// the caller back an empty one, which the database then refuses as a constraint violation.
func (in *New) validate(now time.Time) error {
	in.ListType = ListType(strings.TrimSpace(string(in.ListType)))
	if !in.ListType.Valid() {
		return errs.BadField("list_type", "must be blocklist or watchlist")
	}
	if !in.EntityType.Valid() {
		return errs.BadField("entity_type", "must be person, vehicle, company or device")
	}
	if !in.MatchType.Valid() {
		return errs.BadField("match_type", "is not an identifier this list can match on")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return errs.BadField("reason", "is required")
	}
	if in.Normalised() == "" {
		return errs.BadField("match_value", "has nothing left to match on")
	}
	switch in.Severity {
	case "":
		in.Severity = SeverityHigh
	case SeverityLow, SeverityMedium, SeverityHigh:
	default:
		return errs.BadField("severity", "must be low, medium or high")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return errs.BadField("expires_at", "must be in the future")
	}
	return nil
}

// Add files one listing. The unique key is the tenant, the list, the identifier kind and the
// hash, so the same phone may be watched and refused at once but cannot be filed twice on the
// same list - a second copy is a conflict naming the value, not a duplicate row.
func (s *Service) Add(ctx context.Context, scope tenancy.Scope, in New) (Entry, error) {
	in.TenantID = scope.TenantID
	in.AddedBy = scope.UserID
	now := time.Now().UTC()
	if err := in.validate(now); err != nil {
		return Entry{}, err
	}

	var id int64
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).Insert(ctx, in, now)
		if err != nil {
			return err
		}
		id = filed
		return s.record(ctx, c, scope, "blocklist.entry_added", aggregateType, filed, in.Reason,
			map[string]any{
				"list_type": string(in.ListType), "entity_type": string(in.EntityType),
				"match_type": string(in.MatchType), "match_value": in.Normalised(),
				"severity": string(in.Severity), "display_name": in.DisplayName,
			})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return Entry{}, errs.Conflict("this " + string(in.MatchType) + " is already on the " +
				string(in.ListType))
		}
		return Entry{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Entry, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) Get(ctx context.Context, tenantID, id int64) (Entry, error) {
	return s.repo.ByID(ctx, tenantID, id)
}

// Revoke takes a listing off the list. Nothing is deleted: the hits behind it keep pointing
// at a real row, which is what makes a ban that was wrong for a week auditable as having
// been wrong.
func (s *Service) Revoke(ctx context.Context, scope tenancy.Scope, id int64,
	reason string) (Entry, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Entry{}, errs.BadField("reason", "is required to take a listing off the list")
	}
	var taken bool
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).Revoke(ctx, scope.TenantID, id, scope.UserID, reason,
			time.Now().UTC())
		if err != nil || !moved {
			taken = moved
			return err
		}
		taken = true
		return s.record(ctx, c, scope, "blocklist.entry_revoked", aggregateType, id, reason,
			map[string]any{"status": string(StatusRevoked)})
	})
	if err != nil {
		return Entry{}, err
	}
	if !taken {
		entry, err := s.repo.ByID(ctx, scope.TenantID, id)
		if err != nil {
			return Entry{}, err
		}
		return Entry{}, errs.Conflict("listing is already " + entry.Status)
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Summary is the two numbers the desk screen leads with, plus the hits nobody has explained.
type Summary struct {
	Blocklist      int `json:"blocklist"`
	Watchlist      int `json:"watchlist"`
	Total          int `json:"total"`
	UnresolvedHits int `json:"unresolved_hits"`
}

func (s *Service) Summary(ctx context.Context, tenantID int64) (Summary, error) {
	counts, err := s.repo.CountByType(ctx, tenantID)
	if err != nil {
		return Summary{}, err
	}
	open, err := s.repo.UnresolvedHits(ctx, tenantID)
	if err != nil {
		return Summary{}, err
	}
	blocked, watched := counts[string(ListBlock)], counts[string(ListWatch)]
	return Summary{Blocklist: blocked, Watchlist: watched, Total: blocked + watched,
		UnresolvedHits: open}, nil
}

func (s *Service) Hits(ctx context.Context, filter HitFilter) ([]Hit, error) {
	return s.repo.Hits(ctx, filter)
}

func (s *Service) Hit(ctx context.Context, tenantID, id int64) (Hit, error) {
	return s.repo.HitByID(ctx, tenantID, id)
}

// Resolve closes one hit with the sentence somebody wrote about it. The listing itself stays
// exactly where it was: a hit that turns out to be a different person with the same name is
// resolved, while the ban on the real person keeps doing its job.
func (s *Service) Resolve(ctx context.Context, scope tenancy.Scope, hitID int64,
	note string) (Hit, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return Hit{}, errs.BadField("resolution", "is required to close a hit")
	}
	var closed bool
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).Resolve(ctx, scope.TenantID, hitID, scope.UserID, note,
			time.Now().UTC())
		if err != nil || !moved {
			closed = moved
			return err
		}
		closed = true
		return s.record(ctx, c, scope, "blocklist.hit_resolved", "blocklist_hit", hitID, note,
			map[string]any{"resolved": true})
	})
	if err != nil {
		return Hit{}, err
	}
	if !closed {
		hit, err := s.repo.HitByID(ctx, scope.TenantID, hitID)
		if err != nil {
			return Hit{}, err
		}
		return Hit{}, errs.Conflict("hit " + strconv.FormatInt(hit.ID, 10) + " is already resolved")
	}
	return s.repo.HitByID(ctx, scope.TenantID, hitID)
}

// record is the audit trail of a desk decision. Who was put on a list, who took them off and
// who closed a hit are exactly the questions an incident review asks, and a listing row on its
// own cannot answer any of them.
func (s *Service) record(ctx context.Context, c db.Conn, scope tenancy.Scope, action,
	subjectType string, subjectID int64, reason string, after map[string]any) error {
	return s.chain.Write(ctx, c, audit.Entry{
		TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: action,
		SubjectType: subjectType, SubjectID: subjectID, SiteID: scope.SiteID, Reason: reason,
		After: after,
	})
}

// reasonRoom is how much of a listing's own words may travel into the gate's reason column.
// gate_movements.reason_note is VARCHAR(512) and the server runs strict, so a listing whose
// reason is written long would otherwise turn the overflow into a failed scan - an error page
// shown to a guard who is owed a red light.
const reasonRoom = 480

// clip shortens a value on runes, not bytes, because a reason typed with accents is not a
// reason that gets to break the gate.
func clip(value string, room int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= room {
		return string(runes)
	}
	return string(runes[:room-1]) + "…"
}

func optionalID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
