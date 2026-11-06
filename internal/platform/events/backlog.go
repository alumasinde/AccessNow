package events

import (
	"context"
	"time"

	"accessnow/internal/platform/logging"
)

// drainer is the half of the relay a background pass needs: drain whoever owes work.
type drainer interface {
	Drain(ctx context.Context) error
}

// Backlog is the outbox's safety net. The gate scan relays its own tenant inside the
// request; a desk decision or an escalation sweep has no such hook, so its events would sit
// pending until something else came along. This loop is what makes the outbox mean "delivered
// eventually" rather than "delivered when a badge happens to be scanned".
type Backlog struct {
	drainer drainer
	every   time.Duration
}

func NewBacklog(d drainer, every time.Duration) *Backlog {
	return &Backlog{drainer: d, every: every}
}

// Run drains until the context ends. A pass that fails is logged and retried on the next
// tick: the rows are the record, so the loop has nothing to lose by carrying on.
func (b *Backlog) Run(ctx context.Context) {
	ticker := time.NewTicker(b.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.drainer.Drain(ctx); err != nil {
				logging.From(ctx).ErrorContext(ctx, "outbox backlog pass failed", "error", err)
			}
		}
	}
}
