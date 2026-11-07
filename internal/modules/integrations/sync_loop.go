package integrations

import (
	"context"
	"time"

	"accessnow/internal/platform/logging"
)

// SyncLoop is the HR/ERP worker's heartbeat. A minute is the tick: the shortest frequency a
// system may ask for is one minute, and the queue read behind a tick is one indexed range.
type SyncLoop struct {
	svc   *Service
	every time.Duration
}

func NewSyncLoop(svc *Service, every time.Duration) *SyncLoop {
	return &SyncLoop{svc: svc, every: every}
}

func (l *SyncLoop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ran, err := l.svc.RunSyncDue(ctx)
			if err != nil {
				logging.From(ctx).ErrorContext(ctx, "sync sweep failed", "error", err)
				continue
			}
			if ran > 0 {
				logging.From(ctx).InfoContext(ctx, "sync jobs run", "count", ran)
			}
		}
	}
}
