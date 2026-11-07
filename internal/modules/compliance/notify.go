package compliance

import (
	"context"
	"fmt"
	"time"

	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
)

// NotifyDue publishes the warning a retention policy asked for. The notice lands before the
// data becomes eligible: the policy's clock starts at its last purge (or when it was created),
// and notify_before_days is how long before the next eligible date the owner hears about it.
func (s *Service) NotifyDue(ctx context.Context, tenantID int64) (int, error) {
	on, err := s.enabled(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if !on || s.events == nil {
		return 0, nil
	}
	policies, err := s.repo.Policies(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	now := s.now()
	sent := 0
	for _, policy := range policies {
		if !policy.IsActive || policy.NotifyBeforeDays == nil {
			continue
		}
		base := policy.CreatedAt
		if policy.LastRunAt != nil {
			base = *policy.LastRunAt
		}
		days := policy.RetentionDays - *policy.NotifyBeforeDays
		if days < 0 {
			days = 0
		}
		dueAt := base.AddDate(0, 0, days)
		if now.Before(dueAt) {
			continue
		}
		if policy.LastNotifiedAt != nil && !policy.LastNotifiedAt.Before(dueAt) {
			continue
		}
		if err := s.events.Publish(ctx, s.pool, events.Event{
			TenantID: tenantID, EventKey: "compliance.purge_due",
			AggregateType: aggregateType, AggregateID: policy.ID,
			DedupeKey: fmt.Sprintf("purge_due:%d:%s", policy.ID, dueAt.Format("2006-01-02")),
			Payload: map[string]any{
				"data_category": policy.DataCategory, "retention_days": policy.RetentionDays,
				"notify_before_days": *policy.NotifyBeforeDays,
				"due_at":             dueAt.UTC(), "purge_action": policy.PurgeAction,
			},
		}); err != nil {
			return sent, err
		}
		// Stamp only after the outbox row exists: a failed publish must be retried by the next
		// sweep rather than silently counted as warned.
		if err := s.repo.MarkNotified(ctx, tenantID, policy.ID, now); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// NotifyDueAll is the timer's call: every tenant with a policy that asked for a warning.
func (s *Service) NotifyDueAll(ctx context.Context) error {
	tenants, err := s.repo.PolicyTenants(ctx)
	if err != nil {
		return err
	}
	for _, tenantID := range tenants {
		sent, err := s.NotifyDue(ctx, tenantID)
		if err != nil {
			logging.From(ctx).ErrorContext(ctx, "pre-purge notice failed",
				"tenant_id", tenantID, "error", err)
			continue
		}
		if sent > 0 {
			logging.From(ctx).InfoContext(ctx, "pre-purge notices published",
				"tenant_id", tenantID, "count", sent)
		}
	}
	return nil
}

// Loop is the pre-purge timer. A policy's warning is about days, not seconds, so six hours is
// frequent enough to land it on the right morning and cheap enough to leave alone.
type Loop struct {
	svc   *Service
	every time.Duration
}

func NewLoop(svc *Service, every time.Duration) *Loop {
	if every <= 0 {
		every = 6 * time.Hour
	}
	return &Loop{svc: svc, every: every}
}

func (l *Loop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.svc.NotifyDueAll(ctx); err != nil && ctx.Err() == nil {
				logging.From(ctx).ErrorContext(ctx, "pre-purge sweep failed", "error", err)
			}
		}
	}
}
