package reports

import (
	"context"
	"time"

	"accessnow/internal/platform/logging"
)

// Loop is the schedule's heartbeat. It is a loop rather than a job queue because a scheduled
// export is due at a wall-clock moment, and a tenant with four schedules does not need four
// timers: one pass every half minute is under a human's notice and cheap to run.
type Loop struct {
	svc   *Service
	every time.Duration
}

func NewLoop(svc *Service, every time.Duration) *Loop { return &Loop{svc: svc, every: every} }

func (l *Loop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ran, err := l.svc.RunDue(ctx)
			if err != nil {
				logging.From(ctx).ErrorContext(ctx, "report schedule sweep failed", "error", err)
				continue
			}
			if ran > 0 {
				logging.From(ctx).InfoContext(ctx, "report schedules ran", "count", ran)
			}
		}
	}
}
