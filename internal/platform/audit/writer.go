// Package audit writes the tamper-evident trail. Core always writes it, so no module
// needs to be enabled for a scan or a status change to be recorded.
package audit

import (
	"context"
	"fmt"
	"time"

	"accessnow/internal/platform/db"
)

const stream = "audit"

type Entry struct {
	TenantID      int64
	ActorUserID   int64
	ActorType     string
	ActorDeviceID int64
	Action        string
	SubjectType   string
	SubjectID     int64
	PassID        int64
	SiteID        int64
	GateID        int64
	GateSessionID int64
	Reason        string
	RequestID     string
	IPAddress     string
	UserAgent     string
	Before        map[string]any
	After         map[string]any
	OccurredAt    time.Time
}

type Writer struct{}

// Write appends one row to the tenant's hash chain. Call it inside the transaction that
// changed the data: the chain is locked per tenant with SELECT ... FOR UPDATE, so
// two concurrent writers cannot both claim the same seq.
func (Writer) Write(ctx context.Context, c db.Conn, e Entry) error {
	if e.TenantID == 0 || e.Action == "" {
		return fmt.Errorf("audit: tenant_id and action are required")
	}
	if e.ActorType == "" {
		e.ActorType = "user"
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}

	prevSeq, prevHash, err := lockTail(ctx, c, e.TenantID)
	if err != nil {
		return err
	}
	rowHash := hash(prevSeq+1, prevHash, e)
	before, after, err := encodeJSON(e.Before, e.After)
	if err != nil {
		return err
	}

	const query = `
		INSERT INTO audit_logs
			(tenant_id, stream_key, seq, actor_user_id, actor_type, actor_device_id, action,
			 subject_type, subject_id, pass_id, site_id, gate_id, gate_session_id,
			 before_json, after_json, reason, ip_address, user_agent, request_id,
			 occurred_at, prev_hash, row_hash)
		VALUES (?, ?, ?, NULLIF(?, 0), ?, NULLIF(?, 0), ?, NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, 0),
		        NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0), ?, ?, NULLIF(?, ''), NULLIF(?, ''),
		        NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`
	_, err = c.ExecContext(ctx, query,
		e.TenantID, stream, prevSeq+1, e.ActorUserID, e.ActorType, e.ActorDeviceID, e.Action,
		e.SubjectType, e.SubjectID, e.PassID, e.SiteID, e.GateID, e.GateSessionID,
		before, after, e.Reason, e.IPAddress, e.UserAgent, e.RequestID,
		e.OccurredAt.UTC(), prevHash, rowHash)
	if err != nil {
		return fmt.Errorf("write audit row %s: %w", e.Action, err)
	}
	return nil
}
