package movements

import (
	"context"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Synced is one replayed scan. Every item gets an answer: a batch is accepted even when
// one row inside it is refused, because a tablet cannot retry a queue one row at a time.
//
// Duplicate says the row was already on file under the same idempotency key, so the answer
// handed back is the one the first attempt got. Nothing in Core reads it; the ledger that
// counts a batch does, because a device retrying a half-sent batch has done nothing wrong and
// that is the one conflict nobody has to be told off for.
type Synced struct {
	Index      int
	Result     Result
	Reason     string
	MovementID int64
	// PassID is which pass the server settled this row against. A device often names a pass
	// only by the number it read, and the ledger files an argument under the pass the server
	// resolved, because that is the one name both sides can agree on.
	PassID    int64
	Duplicate bool
	Err       error
}

// Summary is one batch as the device needs to hear it.
type Summary struct {
	BatchID int64
	Status  string
	Items   []Synced
}

// replayed is the word a batch lands under when no ledger counted it: the rows are on file,
// and nobody has said how many of them were new.
const replayed = "replayed"

// Sync replays an offline queue. Each row goes through the same Scan, so the decisions are
// made on the server's clock and the idempotency key stops a half-sent batch from doubling
// when the tablet tries again.
//
// The ledger is opened first so the movements can point at the batch that carried them, and
// closed last so it can say what the whole queue came to. Either half failing is logged and
// costs the device nothing: a tablet that cannot be filed is not a reason to lose the scans
// it has been holding since its shift ended.
func (s *Service) Sync(ctx context.Context, scope tenancy.Scope, queue Queue) Summary {
	batchID := s.openBatch(ctx, scope, queue)

	results := make([]Synced, 0, len(queue.Scans))
	for index, scan := range queue.Scans {
		scan.Source = SourceOffline
		scan.BatchID = batchID
		if scan.IdempotencyKey == "" {
			results = append(results, Synced{Index: index, Err: errs.BadField("idempotency_key", "required on an offline scan")})
			continue
		}
		outcome, err := s.Scan(ctx, scope, scan)
		if err != nil {
			results = append(results, Synced{Index: index, Err: err})
			continue
		}
		results = append(results, Synced{
			Index: index, Result: outcome.Result, Reason: outcome.Reason,
			MovementID: outcome.Movement.ID, PassID: derefInt64(outcome.Movement.PassID),
			Duplicate: outcome.Replayed,
		})
	}

	status := s.closeBatch(ctx, scope, batchID, queue, results)
	// Every Scan drained the outbox for itself, and the ledger only speaks once the whole queue
	// has landed - so what it said would still be pending when the tablet got its answer. This
	// drain is what makes a notice about a batch arrive with the batch instead of whenever the
	// next badge happens to come past a gate.
	if err := s.relay.Relay(ctx, scope.TenantID); err != nil {
		logging.From(ctx).ErrorContext(ctx, "relay the batch", "batch_id", batchID, "error", err)
	}
	return Summary{BatchID: batchID, Status: status, Items: results}
}

// openBatch files the queue and hands back the row its movements point at. Zero is the answer
// when there is no ledger to file with, which is the same answer as a tenant that has not
// bought this module - so the nil check belongs here rather than in every caller.
func (s *Service) openBatch(ctx context.Context, scope tenancy.Scope, queue Queue) int64 {
	if s.ledger == nil {
		return 0
	}
	batchID, err := s.ledger.Open(ctx, scope, queue)
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "open sync batch", "client_batch_id", queue.BatchID,
			"error", err)
		return 0
	}
	return batchID
}

// closeBatch is the other half, and says "replayed" whenever the ledger had nothing to say:
// the rows are on file either way, and the device is owed a word about them.
func (s *Service) closeBatch(ctx context.Context, scope tenancy.Scope, batchID int64,
	queue Queue, results []Synced) string {
	if s.ledger == nil || batchID == 0 {
		return replayed
	}
	status, err := s.ledger.Close(ctx, scope, batchID, queue, results)
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "close sync batch", "batch_id", batchID, "error", err)
	}
	if status == "" {
		return replayed
	}
	return status
}
