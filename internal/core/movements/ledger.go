package movements

import (
	"context"

	"accessnow/internal/platform/tenancy"
)

// Ledger is the seam an offline-sync ledger plugs into. Core replays the queue a tablet
// buffered while the link was down; the ledger is what keeps a book of that replay: which
// batch arrived, how much of it landed, and which rows disagreed with the server.
//
// It is a seam and not a piece of Sync because the two halves want opposite things. A device
// must get its movements on file whether or not anybody is counting them, and the counting
// belongs to a module a tenant may not have bought. An unbound ledger records nothing and the
// replay is exactly what it was before the module existed.
type Ledger interface {
	// Open files the batch before its rows land, so a movement can point at the batch that
	// carried it. Zero is the answer when nothing is listening, and the replay still runs.
	Open(ctx context.Context, scope tenancy.Scope, queue Queue) (int64, error)

	// Close is told what every item was answered with. It returns the word the batch is filed
	// under - completed, partial, failed - which is the ledger's to choose because it owns the
	// counts, and hands back whatever the device should be told to expect.
	Close(ctx context.Context, scope tenancy.Scope, batchID int64, queue Queue,
		results []Synced) (string, error)
}

// Queue is one device's buffered scans, named the way the device names them: BatchID is the
// uuid the tablet minted for this batch and is the replay guard, because a half-sent batch
// sent again is the same batch, not a second one.
type Queue struct {
	BatchID string
	Scans   []Scan
}

// LedgerGuard is the handle Core holds while the ledger it will point at is still being
// built. Like the screening and rulebook guards it starts empty, and an empty one says
// nothing.
type LedgerGuard struct{ ledger Ledger }

func NewLedgerGuard() *LedgerGuard { return &LedgerGuard{} }

// Bind hands the guard the ledger to write to.
func (g *LedgerGuard) Bind(ledger Ledger) { g.ledger = ledger }

// Open reports the batch, and stays silent when no module is keeping a ledger.
func (g *LedgerGuard) Open(ctx context.Context, scope tenancy.Scope, queue Queue) (int64, error) {
	if g.ledger == nil {
		return 0, nil
	}
	return g.ledger.Open(ctx, scope, queue)
}

// Close files the outcome, and gives back the batch word the ledger chose.
func (g *LedgerGuard) Close(ctx context.Context, scope tenancy.Scope, batchID int64,
	queue Queue, results []Synced) (string, error) {
	if g.ledger == nil {
		return "", nil
	}
	return g.ledger.Close(ctx, scope, batchID, queue, results)
}
