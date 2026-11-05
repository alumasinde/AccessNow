package movements

import (
	"context"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// Synced is one replayed scan. Every item gets an answer: a batch is accepted even when
// one row inside it is refused, because a tablet cannot retry a queue one row at a time.
type Synced struct {
	Index      int
	Result     Result
	Reason     string
	MovementID int64
	Err        error
}

// Sync replays an offline queue. Each row goes through the same Scan, so the decisions
// are made on the server's clock and the idempotency key stops a half-sent batch from
// doubling when the tablet tries again.
func (s *Service) Sync(ctx context.Context, scope tenancy.Scope, scans []Scan) []Synced {
	results := make([]Synced, 0, len(scans))
	for index, scan := range scans {
		scan.Source = SourceOffline
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
			MovementID: outcome.Movement.ID,
		})
	}
	return results
}
