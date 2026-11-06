package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const requestColumns = `id, tenant_id, pass_id, approval_flow_id, flow_snapshot, status,
	current_step_no, total_steps, value_amount, currency, started_at, deadline_at,
	completed_at, decided_by_user_id, decision_note, escalation_count, created_at, updated_at`

const requestStepColumns = `id, tenant_id, approval_request_id, step_no, name, assignee_key,
	assignee_user_id, approver_role_key, assignee_name_snapshot, status, mode, due_at,
	assigned_at, notified_at, viewed_at, acted_at, acted_by_user_id, comment,
	delegated_from_user_id, delegation_id, escalated_from_step_no, created_at, updated_at`

// NewRequest is a chain as it starts. Snapshot is the step list at the moment the pass was
// issued, kept because a tenant editing a flow must not rewrite history.
type NewRequest struct {
	TenantID    int64
	PassID      int64
	FlowID      int64
	Snapshot    string
	Status      RequestStatus
	TotalSteps  int
	ValueAmount *float64
	Currency    string
	DeadlineAt  *time.Time
}

// CreateRequest starts a chain and is safe to run twice for one pass: open_pass_key is
// unique, so a redelivered outbox event lands on the row that already exists and reports
// that it did not create it.
func (r *Repository) CreateRequest(ctx context.Context, in NewRequest) (Request, bool, error) {
	const query = `
		INSERT INTO approval_requests
			(tenant_id, pass_id, approval_flow_id, flow_snapshot, status, current_step_no,
			 total_steps, value_amount, currency, started_at, deadline_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, UTC_TIMESTAMP(6), ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	result, err := r.q.ExecContext(ctx, query, in.TenantID, in.PassID, nullable(in.FlowID),
		text(in.Snapshot), string(in.Status), in.TotalSteps, in.ValueAmount, text(in.Currency),
		nullableTime(in.DeadlineAt))
	if err != nil {
		return Request{}, false, fmt.Errorf("start approval chain: %w", err)
	}
	// One row affected is a fresh chain, zero is the duplicate, so the caller can tell a
	// first pass through from an outbox redelivery without a second query.
	affected, err := result.RowsAffected()
	if err != nil {
		return Request{}, false, fmt.Errorf("read approval chain result: %w", err)
	}
	created := affected == 1
	id, err := result.LastInsertId()
	if err != nil {
		return Request{}, false, fmt.Errorf("read approval request id: %w", err)
	}
	request, err := r.RequestByID(ctx, in.TenantID, id)
	return request, created, err
}

func (r *Repository) RequestByID(ctx context.Context, tenantID, id int64) (Request, error) {
	const query = `SELECT ` + requestColumns + ` FROM approval_requests
		WHERE id = ? AND tenant_id = ?`
	var out Request
	if err := r.q.GetContext(ctx, &out, query, id, tenantID); err != nil {
		return Request{}, requestReadErr(err)
	}
	return out, nil
}

// OpenByPass is the chain a pass is working through, if any.
func (r *Repository) OpenByPass(ctx context.Context, tenantID, passID int64) (Request, error) {
	const query = `SELECT ` + requestColumns + ` FROM approval_requests
		WHERE tenant_id = ? AND pass_id = ? AND completed_at IS NULL ORDER BY id DESC LIMIT 1`
	var out Request
	if err := r.q.GetContext(ctx, &out, query, tenantID, passID); err != nil {
		return Request{}, requestReadErr(err)
	}
	return out, nil
}

// Holds answers Core's question: is a chain of approvers still deciding this pass?
func (r *Repository) Holds(ctx context.Context, tenantID, passID int64) (bool, error) {
	const query = `SELECT 1 FROM approval_requests
		WHERE  tenant_id = ? AND pass_id = ? AND completed_at IS NULL
		  AND  status IN ('pending', 'in_progress', 'escalated') LIMIT 1`
	var one int
	err := r.q.GetContext(ctx, &one, query, tenantID, passID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check for an open chain: %w", err)
	}
	return true, nil
}

// MoveRequest advances the chain. The status guard is the concurrency answer: an approver
// who lost the race is told the chain moved, instead of silently moving it again.
func (r *Repository) MoveRequest(ctx context.Context, tenantID, id int64, to RequestStatus,
	stepNo int, deadline *time.Time) (bool, error) {
	const query = `
		UPDATE approval_requests
		SET    status = ?, current_step_no = ?, deadline_at = COALESCE(?, deadline_at)
		WHERE  tenant_id = ? AND id = ? AND completed_at IS NULL
		  AND  status IN ('pending', 'in_progress', 'escalated')`
	result, err := r.q.ExecContext(ctx, query, string(to), stepNo, nullableTime(deadline), tenantID, id)
	if err != nil {
		return false, fmt.Errorf("move approval chain: %w", err)
	}
	return changed(result)
}

// CountEscalation records that a step was taken up the chain, which is the number an
// approver queue sorts by when everything is overdue.
func (r *Repository) CountEscalation(ctx context.Context, tenantID, id int64) error {
	const query = `UPDATE approval_requests SET escalation_count = escalation_count + 1
		WHERE id = ? AND tenant_id = ?`
	_, err := r.q.ExecContext(ctx, query, id, tenantID)
	return wrap(err, "count escalation")
}

// FinishRequest closes a chain and frees the pass's open slot for the next one.
func (r *Repository) FinishRequest(ctx context.Context, tenantID, id int64, to RequestStatus,
	decidedBy int64, note string) (bool, error) {
	const query = `
		UPDATE approval_requests
		SET    status = ?, completed_at = UTC_TIMESTAMP(6), decided_by_user_id = ?,
		       decision_note = NULLIF(?, '')
		WHERE  tenant_id = ? AND id = ? AND completed_at IS NULL
		  AND  status IN ('pending', 'in_progress', 'escalated')`
	result, err := r.q.ExecContext(ctx, query, string(to), nullable(decidedBy), note, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("finish approval chain: %w", err)
	}
	return changed(result)
}

// NewStepRow is one assignee being asked.
type NewStepRow struct {
	TenantID          int64
	RequestID         int64
	StepNo            int
	Name              string
	AssigneeKey       string
	AssigneeUserID    int64
	ApproverRoleKey   string
	AssigneeName      string
	Mode              string
	DueAt             *time.Time
	DelegatedFromUser int64
	DelegationID      int64
	EscalatedFrom     int
}

// InsertStep asks one person or one role. The duplicate branch is a no-op on purpose: a
// relay that runs the same activation twice must not reset an answer already given.
func (r *Repository) InsertStep(ctx context.Context, in NewStepRow) error {
	const query = `
		INSERT INTO approval_request_steps
			(tenant_id, approval_request_id, step_no, name, assignee_key, assignee_user_id,
			 approver_role_key, assignee_name_snapshot, status, mode, due_at, assigned_at,
			 delegated_from_user_id, delegation_id, escalated_from_step_no)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, UTC_TIMESTAMP(6), ?, ?, ?)
		ON DUPLICATE KEY UPDATE status = status`
	_, err := r.q.ExecContext(ctx, query, in.TenantID, in.RequestID, in.StepNo, text(in.Name),
		in.AssigneeKey, nullable(in.AssigneeUserID), text(in.ApproverRoleKey), text(in.AssigneeName),
		in.Mode, nullableTime(in.DueAt), nullable(in.DelegatedFromUser), nullable(in.DelegationID),
		nullable(int64(in.EscalatedFrom)))
	return wrap(err, "assign approval step")
}

func (r *Repository) StepsForRequest(ctx context.Context, tenantID, requestID int64) ([]RequestStep, error) {
	const query = `SELECT ` + requestStepColumns + ` FROM approval_request_steps
		WHERE tenant_id = ? AND approval_request_id = ? ORDER BY step_no, id`
	var out []RequestStep
	if err := r.q.SelectContext(ctx, &out, query, tenantID, requestID); err != nil {
		return nil, fmt.Errorf("read chain steps: %w", err)
	}
	return out, nil
}

func (r *Repository) StepByID(ctx context.Context, tenantID, id int64) (RequestStep, error) {
	const query = `SELECT ` + requestStepColumns + ` FROM approval_request_steps
		WHERE id = ? AND tenant_id = ?`
	var s RequestStep
	if err := r.q.GetContext(ctx, &s, query, id, tenantID); err != nil {
		return RequestStep{}, stepReadErr(err)
	}
	return s, nil
}

// WaitingAtStep reads the asks still open on one step, which is how a parallel step knows
// whether it has everyone yet.
func (r *Repository) WaitingAtStep(ctx context.Context, tenantID, requestID int64, stepNo int) ([]RequestStep, error) {
	const query = `SELECT ` + requestStepColumns + ` FROM approval_request_steps
		WHERE tenant_id = ? AND approval_request_id = ? AND step_no = ? AND status = 'pending'
		ORDER BY id`
	var out []RequestStep
	if err := r.q.SelectContext(ctx, &out, query, tenantID, requestID, stepNo); err != nil {
		return nil, fmt.Errorf("read waiting asks: %w", err)
	}
	return out, nil
}

// Answer records one approver's decision on one ask. The pending guard is the whole story:
// two holders of the same role clicking approve together cannot both answer for the step,
// and the loser is told somebody got there first.
func (r *Repository) Answer(ctx context.Context, tenantID, stepID int64, to StepStatus,
	byUserID int64, comment string) (bool, error) {
	const query = `
		UPDATE approval_request_steps
		SET    status = ?, acted_at = UTC_TIMESTAMP(6), acted_by_user_id = ?,
		       comment = NULLIF(?, '')
		WHERE  id = ? AND tenant_id = ? AND status = 'pending'`
	result, err := r.q.ExecContext(ctx, query, string(to), byUserID, comment, stepID, tenantID)
	if err != nil {
		return false, fmt.Errorf("answer approval step: %w", err)
	}
	return changed(result)
}

// CloseSiblings is the any-one step settling: once one assignee answers, the rest of the
// rows are history rather than a task.
func (r *Repository) CloseSiblings(ctx context.Context, tenantID, requestID int64, stepNo int,
	answeredID int64) (int64, error) {
	const query = `
		UPDATE approval_request_steps SET status = 'skipped', acted_at = UTC_TIMESTAMP(6)
		WHERE  tenant_id = ? AND approval_request_id = ? AND step_no = ? AND status = 'pending'
		  AND  id <> ?`
	result, err := r.q.ExecContext(ctx, query, tenantID, requestID, stepNo, answeredID)
	if err != nil {
		return 0, fmt.Errorf("close the rest of the step: %w", err)
	}
	return result.RowsAffected()
}

// CancelAsks is what a chain that is over owes everybody who was still being asked. Without
// it a rejected pass sits in four inboxes forever, and an approver who arrives at their desk
// the next morning finds a task that was decided without them.
func (r *Repository) CancelAsks(ctx context.Context, tenantID, requestID int64) (int64, error) {
	const query = `
		UPDATE approval_request_steps SET status = 'recalled', acted_at = UTC_TIMESTAMP(6)
		WHERE  tenant_id = ? AND approval_request_id = ? AND status = 'pending'`
	result, err := r.q.ExecContext(ctx, query, tenantID, requestID)
	if err != nil {
		return 0, fmt.Errorf("clear the asks of a closed chain: %w", err)
	}
	return result.RowsAffected()
}

// OpenAskAudience is the people a chain that stops without an answer has to tell: whoever
// held a pending ask, whether it named them or named a role they hold. The catalogue addresses
// approval.cancelled to "the approver", and no directory has a role by that word, so an event
// without this list reaches nobody - including the supervisor who was about to answer.
func (r *Repository) OpenAskAudience(ctx context.Context, tenantID, requestID, siteID int64) ([]int64, error) {
	steps, err := r.StepsForRequest(ctx, tenantID, requestID)
	if err != nil {
		return nil, err
	}
	var (
		out  []int64
		seen = make(map[int64]bool)
	)
	for _, step := range steps {
		if !step.Waiting() {
			continue
		}
		if step.AssigneeUserID != nil {
			if id := *step.AssigneeUserID; !seen[id] {
				seen[id], out = true, append(out, id)
			}
			continue
		}
		if step.ApproverRoleKey == nil {
			continue
		}
		people, err := r.RoleHolders(ctx, tenantID, *step.ApproverRoleKey, siteID)
		if err != nil {
			return nil, err
		}
		for _, id := range personIDs(people) {
			if !seen[id] {
				seen[id], out = true, append(out, id)
			}
		}
	}
	return out, nil
}

// Recall takes an unanswered ask off its assignee, which is what an escalation does before
// it asks somebody higher.
func (r *Repository) Recall(ctx context.Context, tenantID, stepID int64) (bool, error) {
	const query = `
		UPDATE approval_request_steps SET status = 'recalled', acted_at = UTC_TIMESTAMP(6)
		WHERE  id = ? AND tenant_id = ? AND status = 'pending'`
	result, err := r.q.ExecContext(ctx, query, stepID, tenantID)
	if err != nil {
		return false, fmt.Errorf("recall approval ask: %w", err)
	}
	return changed(result)
}

// Overdue reads one unanswered ask whose clock has run out and which has not already been
// taken up the chain, so a sweep that runs twice escalates once.
func (r *Repository) Overdue(ctx context.Context, tenantID int64, at time.Time, limit int) ([]RequestStep, error) {
	const query = `
		SELECT ` + requestStepColumns + ` FROM approval_request_steps s
		WHERE  s.tenant_id = ? AND s.status = 'pending' AND s.due_at IS NOT NULL AND s.due_at <= ?
		  AND  NOT EXISTS (
		           SELECT 1 FROM approval_request_steps e
		           WHERE  e.approval_request_id = s.approval_request_id
		             AND  e.step_no = s.step_no AND e.escalated_from_step_no IS NOT NULL)
		  AND  EXISTS (
		           SELECT 1 FROM approval_requests ar
		           WHERE  ar.id = s.approval_request_id AND ar.completed_at IS NULL
		             AND  ar.status IN ('pending', 'in_progress', 'escalated'))
		ORDER  BY s.due_at, s.id LIMIT ?`
	var out []RequestStep
	if err := r.q.SelectContext(ctx, &out, query, tenantID, at.UTC(), limit); err != nil {
		return nil, fmt.Errorf("read overdue approval asks: %w", err)
	}
	return out, nil
}

// InboxRow is one ask waiting on somebody: enough about the pass for the approver to decide
// it without opening the module that raised it.
type InboxRow struct {
	RequestStepID   int64      `db:"request_step_id" json:"request_step_id"`
	StepNo          int        `db:"step_no" json:"step_no"`
	StepName        *string    `db:"step_name" json:"step_name,omitempty"`
	Mode            string     `db:"mode" json:"mode"`
	DueAt           *time.Time `db:"due_at" json:"due_at,omitempty"`
	AssignedAt      time.Time  `db:"assigned_at" json:"assigned_at"`
	RequestID       int64      `db:"approval_request_id" json:"approval_request_id"`
	RequestStatus   string     `db:"request_status" json:"request_status"`
	CurrentStepNo   int        `db:"current_step_no" json:"current_step_no"`
	TotalSteps      int        `db:"total_steps" json:"total_steps"`
	Escalated       bool       `db:"escalated" json:"escalated"`
	PassID          int64      `db:"pass_id" json:"pass_id"`
	PassNo          string     `db:"pass_no" json:"pass_no"`
	SubjectName     *string    `db:"subject_name" json:"subject_name,omitempty"`
	SourceModuleKey string     `db:"source_module_key" json:"source_module_key"`
	PassTypeKey     string     `db:"pass_type_key" json:"pass_type_key"`
	ValueAmount     *float64   `db:"value_amount" json:"value_amount,omitempty"`
	RequesterName   *string    `db:"requester_name" json:"requester_name,omitempty"`
	RequesterUserID *int64     `db:"requester_user_id" json:"requester_user_id,omitempty"`
}

// Inbox reads what is waiting on one person: asks addressed to them by name, and asks
// addressed to a role they hold. The role half is why this is not v_approval_inbox - the
// view can only name a user, and a step sent to "whoever is site manager this week" has no
// user id until somebody claims it.
func (r *Repository) Inbox(ctx context.Context, tenantID, userID int64, roleKeys []string, limit int) ([]InboxRow, error) {
	const query = `
		SELECT ars.id AS request_step_id, ars.step_no, ars.name AS step_name, ars.mode,
		       ars.due_at, ars.assigned_at, ar.id AS approval_request_id,
		       ar.status AS request_status, ar.current_step_no, ar.total_steps,
		       ar.escalation_count > 0 AS escalated, p.id AS pass_id, p.pass_no,
		       p.subject_name, p.source_module_key, pt.type_key AS pass_type_key,
		       p.value_amount, CONCAT_WS(' ', req.first_name, req.last_name) AS requester_name,
		       p.requester_user_id
		FROM   approval_request_steps ars
		JOIN   approval_requests ar ON ar.id = ars.approval_request_id AND ar.tenant_id = ars.tenant_id
		JOIN   passes p              ON p.id = ar.pass_id AND p.tenant_id = ars.tenant_id
		JOIN   pass_types pt         ON pt.id = p.pass_type_id
		LEFT   JOIN users req        ON req.id = p.requester_user_id
		WHERE  ars.tenant_id = ? AND ars.status = 'pending'
		  AND  ar.status IN ('pending', 'in_progress', 'escalated')
		  AND  (ars.assignee_user_id = ? OR (ars.assignee_user_id IS NULL AND ars.approver_role_key IN (?)))
		ORDER  BY ars.due_at IS NULL, ars.due_at, ars.id
		LIMIT  ?`
	expanded, args, err := sqlx.In(query, tenantID, userID, orNothing(roleKeys), limit)
	if err != nil {
		return nil, fmt.Errorf("build inbox query: %w", err)
	}
	var out []InboxRow
	// No rebind: sqlx.In only needs to widen the IN list, and the pool already speaks "?".
	if err := r.q.SelectContext(ctx, &out, expanded, args...); err != nil {
		return nil, fmt.Errorf("read approval inbox: %w", err)
	}
	return out, nil
}

// ListFilter is the register of chains. OpenOnly is what the queue screen asks for; the rest
// are the narrowing an operations manager wants over a week of decisions.
type ListFilter struct {
	TenantID  int64
	Status    string
	ModuleKey string
	FlowID    int64
	Assignee  int64
	OpenOnly  bool
	Limit     int
	Before    *httpx.Cursor
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]Request, error) {
	query := `SELECT ` + requestColumns + ` FROM approval_requests WHERE tenant_id = ?`
	args := []any{f.TenantID}

	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.OpenOnly {
		query += ` AND completed_at IS NULL AND status IN ('pending', 'in_progress', 'escalated')`
	}
	if f.FlowID != 0 {
		query += ` AND approval_flow_id = ?`
		args = append(args, f.FlowID)
	}
	if f.ModuleKey != "" {
		query += ` AND EXISTS (SELECT 1 FROM passes p WHERE p.id = approval_requests.pass_id
		           AND p.source_module_key = ?)`
		args = append(args, f.ModuleKey)
	}
	if f.Assignee != 0 {
		query += ` AND EXISTS (SELECT 1 FROM approval_request_steps s
		           WHERE s.approval_request_id = approval_requests.id AND s.status = 'pending'
		             AND  s.assignee_user_id = ?)`
		args = append(args, f.Assignee)
	}
	if f.Before != nil {
		query += ` AND (started_at < ? OR (started_at = ? AND id < ?))`
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	query += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)

	var out []Request
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("list approval requests: %w", err)
	}
	return out, nil
}

// orNothing keeps sqlx.In honest: an empty IN list is a syntax error, and a person with no
// roles simply sees no role-addressed asks.
func orNothing(keys []string) []string {
	if len(keys) == 0 {
		return []string{""}
	}
	return keys
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func requestReadErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errs.NotFound("approval request")
	}
	return fmt.Errorf("read approval request: %w", err)
}

func stepReadErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errs.NotFound("approval step")
	}
	return fmt.Errorf("read approval step: %w", err)
}
