package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// deliveryBatch caps one sweep. The loop runs every few seconds, so a backlog of a thousand
// notices drains in a minute of passes rather than one request that holds the CPU while an
// operator waits on the desk.
const deliveryBatch = 40

// firstBackoff and worstBackoff frame the retry ladder: a gateway that is down for a minute
// is retried quickly, and one that is down for a day is not hammered every thirty seconds by
// five hundred rows.
const (
	firstBackoff = 30 * time.Second
	worstBackoff = 30 * time.Minute
)

// staleClaim is how long a row may sit in 'sending' before the next sweep assumes the worker
// that claimed it is gone. Every sender caps its own call at fifteen seconds, so a claim that
// outlives two minutes has no live owner, and a notice stranded in 'sending' is invisible to
// both the queue and the retry button.
const staleClaim = 2 * time.Minute

// Report is what one sweep did, in the few words an operator cares about.
type Report struct {
	Seen      int
	Sent      int
	Delivered int
	Retried   int
	Failed    int
	Reclaimed int
}

// DeliverDue is what the background loop calls: every tenant's due rows, one batch.
func (s *Service) DeliverDue(ctx context.Context) error {
	_, err := s.Deliver(ctx, 0, deliveryBatch)
	return err
}

// Deliver walks the due queue and hands each row to the tenant's provider. It returns as soon
// as the database cannot be read, because that is the one failure the caller cannot ignore;
// every per-message failure lands on its own row instead, which is the point of the queue.
func (s *Service) Deliver(ctx context.Context, tenantID int64, limit int) (Report, error) {
	var out Report
	if limit <= 0 || limit > deliveryBatch {
		limit = deliveryBatch
	}
	reclaimed, err := s.repo.ReclaimStale(ctx, tenantID, s.now(), s.now().Add(-staleClaim), limit)
	if err != nil {
		return out, err
	}
	out.Reclaimed = reclaimed

	due, err := s.repo.Due(ctx, tenantID, s.now(), limit)
	if err != nil {
		return out, err
	}
	out.Seen = len(due)
	for _, m := range due {
		status, err := s.sendOne(ctx, m)
		if err != nil {
			return out, err
		}
		switch status {
		case StatusDelivered:
			out.Delivered++
		case StatusSent:
			out.Sent++
		case StatusQueued:
			out.Retried++
		case StatusSending:
			// Another worker holds the row. Nothing happened here, so nothing is counted.
		default:
			out.Failed++
		}
	}
	if out.Seen > 0 || out.Reclaimed > 0 {
		logging.From(ctx).InfoContext(ctx, "notification sweep",
			"seen", out.Seen, "sent", out.Sent, "delivered", out.Delivered,
			"retried", out.Retried, "failed", out.Failed, "reclaimed", out.Reclaimed)
	}
	return out, nil
}

// sendOne takes exactly one row and returns the status it ended in. The claim is where two
// workers part: whoever loses it returns 'sending' as the status of a row somebody else is
// already carrying, and touches nothing.
func (s *Service) sendOne(ctx context.Context, m Message) (Status, error) {
	claimed, err := s.repo.Claim(ctx, m.ID)
	if err != nil {
		return "", err
	}
	if !claimed {
		return StatusSending, nil
	}

	try := m.Attempts + 1
	sender, provider, err := s.senderFor(ctx, m)
	if err != nil {
		if failure, ok := refusedHere(err); ok {
			return StatusFailed, s.settleFailure(ctx, m, failure.Code, failure.Reason)
		}
		return "", err
	}

	receipt, sendErr := sender.Send(ctx, outbound(m, provider))
	failure, isRefusal := refusedHere(sendErr)
	switch {
	case sendErr == nil:
		status := StatusSent
		if receipt.ProvedLanding {
			status = StatusDelivered
		}
		if err := s.repo.Sent(ctx, m.ID, provider.Provider, receipt.MessageID, receipt.ProvedLanding); err != nil {
			return "", err
		}
		return status, nil

	case isRefusal:
		if err := s.repo.Fail(ctx, m.ID, failure.Code, failure.Reason); err != nil {
			return "", err
		}
		return StatusFailed, nil

	case try >= m.MaxAttempts:
		// The budget is spent. The row keeps the last error rather than the first, because
		// the last one is what the operator will find still true when they fix it.
		if err := s.repo.Fail(ctx, m.ID, "attempts_spent", sendErr.Error()); err != nil {
			return "", err
		}
		return StatusFailed, nil

	default:
		if err := s.repo.Retry(ctx, m.ID, s.now().Add(backoffFor(try)), "send_failed", sendErr.Error()); err != nil {
			return "", err
		}
		return StatusQueued, nil
	}
}

// settleFailure closes a row whose provider could not even be built. It is separate from the
// switch above only because the row was never really attempted; the status and the reason
// column end up the same either way.
func (s *Service) settleFailure(ctx context.Context, m Message, code, reason string) error {
	if err := s.repo.Fail(ctx, m.ID, code, reason); err != nil {
		return fmt.Errorf("record %s on notice %d: %w", code, m.ID, err)
	}
	return nil
}

// senderFor reads the tenant's provider rows for one channel and builds the sender. The
// catalogue order - primary first, then by id - is the whole selection rule: a tenant that
// lists a second gateway keeps it waiting until it says which one runs.
func (s *Service) senderFor(ctx context.Context, m Message) (Sender, Provider, error) {
	providers, err := s.repo.Providers(ctx, m.TenantID, Channel(m.Channel))
	if err != nil {
		return nil, Provider{}, err
	}
	for _, p := range providers {
		if !p.Sendable() {
			continue
		}
		sender, err := s.providers.build(p)
		if err != nil {
			return nil, p, err
		}
		return sender, p, nil
	}
	return nil, Provider{}, refused("provider_not_configured",
		"no active "+m.Channel+" provider is configured for this tenant")
}

// outbound is the queue row reduced to what a gateway is told. The subject travels with the
// body because an SMS has no header.
func outbound(m Message, p Provider) Outbound {
	from := derefString(p.SenderID)
	if from == "" {
		from = optionsOf(p.Options).str("from_address", "")
	}
	return Outbound{To: m.Address(), Subject: derefString(m.Subject),
		Body: derefString(m.Body), From: from}
}

// backoffFor is the ladder: 30s, 1m, 2m, 4m, capped. Exponential because a gateway that is
// down stays down, and linear retries on five hundred rows is how a tenant gets blamed for a
// denial of service on its own provider.
func backoffFor(try int) time.Duration {
	if try < 1 {
		try = 1
	}
	wait := firstBackoff << (try - 1)
	if wait > worstBackoff || wait <= 0 {
		// wait <= 0 is the shift running off the int; a spent budget is a long wait, not a
		// stampede.
		return worstBackoff
	}
	return wait
}

// Resend is the operator's answer to a row that failed for a reason they have just fixed: a
// provider they configured, a number they corrected. It re-opens the row and tries it once
// straight away, so the screen that asked for the resend can report what happened rather than
// "it will happen soon".
func (s *Service) Resend(ctx context.Context, scope tenancy.Scope, id int64) (Message, error) {
	m, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Message{}, err
	}
	if Channel(m.Channel) == ChannelInApp {
		return Message{}, errs.Conflict("an in-app notice was delivered the moment it was filed")
	}
	var reopened bool
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		reopened, err = s.repo.In(c).Reset(ctx, scope.TenantID, id, s.now())
		if err != nil {
			return err
		}
		if !reopened {
			return errs.Conflict("this notice is " + m.Status + "; only a failed or bounced one can be resent")
		}
		return s.record(ctx, c, scope, "notification.resend", "notification", id,
			"notice requeued by an operator", map[string]any{"from_status": m.Status})
	})
	if err != nil {
		return Message{}, err
	}

	m.Attempts = 0
	m.MaxAttempts = maxAttempts
	if _, err := s.sendOne(ctx, m); err != nil {
		return Message{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// DeliveryLoop is the queue's heartbeat, and the twin of the outbox backlog loop: the relay
// files notices when an event is drained, and this is what gets them out when nothing else is
// running.
type DeliveryLoop struct {
	sweeper interface {
		DeliverDue(ctx context.Context) error
	}
	every time.Duration
}

func NewDeliveryLoop(sweeper interface {
	DeliverDue(ctx context.Context) error
},
	every time.Duration) *DeliveryLoop {
	return &DeliveryLoop{sweeper: sweeper, every: every}
}

// Run sweeps until the context ends. A pass that fails is logged and retried on the next
// tick: the rows are the record, and a gateway outage must never take the API down with it.
func (l *DeliveryLoop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.sweeper.DeliverDue(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logging.From(ctx).ErrorContext(ctx, "notification sweep failed", "error", err)
			}
		}
	}
}
