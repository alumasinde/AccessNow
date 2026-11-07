package rules

import (
	"context"
	"time"

	"accessnow/internal/core/movements"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
)

// passFacts is the pass row reduced to the facts a rule can ask about. It is read as a
// snapshot because a request-stage rule judges the request that was actually filed, not the
// row after an approver has edited it.
type passFacts struct {
	ID              int64    `db:"id"`
	TenantID        int64    `db:"tenant_id"`
	SiteID          int64    `db:"site_id"`
	PassTypeID      int64    `db:"pass_type_id"`
	CategoryKey     string   `db:"category_key"`
	SourceModuleKey string   `db:"source_module_key"`
	PassNo          string   `db:"pass_no"`
	Status          string   `db:"status"`
	Origin          string   `db:"origin"`
	SubjectName     string   `db:"subject_name"`
	SubjectPhone    string   `db:"subject_phone"`
	SubjectEmail    string   `db:"subject_email"`
	Purpose         string   `db:"purpose"`
	Requester       int64    `db:"requester_user_id"`
	Host            int64    `db:"host_user_id"`
	Department      int64    `db:"department_id"`
	EntryCount      int      `db:"entry_count"`
	ValueAmount     *float64 `db:"value_amount"`
}

func (p passFacts) query(timezone string) movements.PolicyQuery {
	q := movements.PolicyQuery{
		ScreenQuery: movements.ScreenQuery{
			TenantID: p.TenantID, SiteID: p.SiteID, PassID: p.ID, PassNo: p.PassNo,
			CategoryKey: p.CategoryKey, SubjectName: p.SubjectName,
			Phone: p.SubjectPhone, Email: p.SubjectEmail,
		},
		PassTypeID: p.PassTypeID, SourceModuleKey: p.SourceModuleKey, Status: p.Status,
		Origin: p.Origin, Purpose: p.Purpose, DepartmentID: p.Department,
		RequesterUserID: p.Requester, HostUserID: p.Host, ValueAmount: p.ValueAmount,
		EntryCount: p.EntryCount, At: time.Now().UTC(), Timezone: timezone,
	}
	return q
}

type nightlyTenant struct {
	ID       int64  `db:"id"`
	Timezone string `db:"timezone"`
}

func (r *Repository) PassFacts(ctx context.Context, tenantID, passID int64) (passFacts, error) {
	const query = `SELECT id, tenant_id, site_id, pass_type_id, category_key, source_module_key,
		pass_no, status, origin, COALESCE(subject_name, '') AS subject_name,
		COALESCE(subject_phone, '') AS subject_phone, COALESCE(subject_email, '') AS subject_email,
		COALESCE(purpose, '') AS purpose, COALESCE(requester_user_id, 0) AS requester_user_id,
		COALESCE(host_user_id, 0) AS host_user_id, COALESCE(department_id, 0) AS department_id,
		entry_count, value_amount
		FROM passes WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	var out passFacts
	if err := r.q.GetContext(ctx, &out, query, tenantID, passID); err != nil {
		return passFacts{}, err
	}
	return out, nil
}

func (r *Repository) NightlyTenants(ctx context.Context) ([]nightlyTenant, error) {
	const query = `SELECT DISTINCT t.id, t.timezone
		FROM tenants t
		JOIN tenant_modules tm ON tm.tenant_id = t.id
		WHERE tm.module_key = ? AND tm.enabled = 1 AND t.status = 'active'
		ORDER BY t.id`
	var out []nightlyTenant
	if err := r.q.SelectContext(ctx, &out, query, ModuleKey); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) NightlyPasses(ctx context.Context, tenantID int64, now time.Time,
	limit int) ([]passFacts, error) {
	const query = `SELECT id, tenant_id, site_id, pass_type_id, category_key, source_module_key,
		pass_no, status, origin, COALESCE(subject_name, '') AS subject_name,
		COALESCE(subject_phone, '') AS subject_phone, COALESCE(subject_email, '') AS subject_email,
		COALESCE(purpose, '') AS purpose, COALESCE(requester_user_id, 0) AS requester_user_id,
		COALESCE(host_user_id, 0) AS host_user_id, COALESCE(department_id, 0) AS department_id,
		entry_count, value_amount
		FROM passes
		WHERE tenant_id = ? AND deleted_at IS NULL
		  AND status IN ('approved', 'active', 'on_site')
		  AND (valid_until IS NULL OR valid_until > ?)
		ORDER BY id LIMIT ?`
	var out []passFacts
	if err := r.q.SelectContext(ctx, &out, query, tenantID, now, limit); err != nil {
		return nil, err
	}
	return out, nil
}

// decideStage is the walk with a configurable stage, so request and nightly read the same
// predicate tree, the same allowances and the same waiver rules as the barrier does.
func (s *Service) decideStage(ctx context.Context, q movements.PolicyQuery,
	stages []string) (movements.Decision, error) {
	on, err := s.enabled(ctx, q.TenantID)
	if err != nil {
		return movements.Decision{}, err
	}
	if !on {
		return movements.Decision{}, nil
	}
	now := at(q)
	rows, err := s.repo.Candidates(ctx, matchFor(q, stages), now, candidateCeiling)
	if err != nil {
		return movements.Decision{}, err
	}
	candidates := trees(ctx, rows)
	if len(candidates) == 0 {
		return movements.Decision{}, nil
	}
	facts := factsOf(q, now, zone(q.Timezone))
	limits, err := s.allowances(ctx, q.TenantID, candidates, facts)
	if err != nil {
		return movements.Decision{}, err
	}
	return s.settle(ctx, evaluate(candidates, facts, limits), q), nil
}

// CheckRequest runs the request stage for one pass. It is called from the pass.issued reaction,
// and its answer is logged rather than applied: a request-stage refusal is a desk decision -
// the approver sees the evaluation and can refuse the pass - not something that silently
// rewrites Core's row.
func (s *Service) CheckRequest(ctx context.Context, tenantID, passID int64) error {
	if tenantID == 0 || passID == 0 {
		return nil
	}
	facts, err := s.repo.PassFacts(ctx, tenantID, passID)
	if err != nil {
		return err
	}
	q := facts.query("")
	decision, err := s.decideStage(ctx, q, []string{string(StageRequest), string(StageBoth)})
	if err != nil {
		return err
	}
	return s.logStage(ctx, q, decision, string(StageRequest))
}

func (s *Service) OnPassIssued(ctx context.Context, e events.Event) error {
	return s.CheckRequest(ctx, e.TenantID, e.PassID)
}

// logStage files one evaluation row per firing. It is deliberately the same table the scan
// writes to, so the desk filters one feed rather than four.
func (s *Service) logStage(ctx context.Context, q movements.PolicyQuery, d movements.Decision,
	stage string) error {
	if len(d.Fired) == 0 {
		return nil
	}
	now := at(q)
	evidence := evidenceOf(factsOf(q, now, zone(q.Timezone)))
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		for _, hit := range d.Fired {
			ruleID := hit.RuleID
			row := Evaluation{
				TenantID: q.TenantID, RuleID: &ruleID, Stage: stage, Outcome: hit.Outcome,
				Matched: true, PassID: optionalID(q.PassID), GateID: optionalID(q.GateID),
				EvaluatedAt: now, Context: evidence, Message: optionalText(clip(hit.Message)),
			}
			if err := repo.LogEvaluation(ctx, row); err != nil {
				return err
			}
		}
		return nil
	})
}

// RunNightly evaluates every open pass against the nightly book. It writes the same evaluation
// rows the barrier does; it does not change a pass, because a nightly rule that could revoke a
// pass while nobody is at the gate would be a rule with no human in the loop.
func (s *Service) RunNightly(ctx context.Context) error {
	tenants, err := s.repo.NightlyTenants(ctx)
	if err != nil {
		return err
	}
	for _, tenant := range tenants {
		now := time.Now().UTC()
		passes, err := s.repo.NightlyPasses(ctx, tenant.ID, now, 500)
		if err != nil {
			logging.From(ctx).ErrorContext(ctx, "read passes for the nightly rules",
				"tenant_id", tenant.ID, "error", err)
			continue
		}
		for _, pass := range passes {
			q := pass.query(tenant.Timezone)
			q.At = now
			decision, err := s.decideStage(ctx, q, []string{string(StageNightly)})
			if err != nil {
				logging.From(ctx).ErrorContext(ctx, "evaluate nightly rules",
					"tenant_id", tenant.ID, "pass_id", pass.ID, "error", err)
				continue
			}
			if err := s.logStage(ctx, q, decision, string(StageNightly)); err != nil {
				logging.From(ctx).ErrorContext(ctx, "log the nightly rule evaluation",
					"tenant_id", tenant.ID, "pass_id", pass.ID, "error", err)
			}
		}
	}
	return nil
}

// NightlyLoop is the timer the rulebook's nightly stage runs on. Six hours is often enough for
// a restriction about a day or a week, and cheap enough that a small tenant still gets it.
type NightlyLoop struct {
	svc   *Service
	every time.Duration
}

func NewNightlyLoop(svc *Service, every time.Duration) *NightlyLoop {
	if every <= 0 {
		every = 6 * time.Hour
	}
	return &NightlyLoop{svc: svc, every: every}
}

func (l *NightlyLoop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.svc.RunNightly(ctx); err != nil && ctx.Err() == nil {
				logging.From(ctx).ErrorContext(ctx, "nightly rules failed", "error", err)
			}
		}
	}
}
