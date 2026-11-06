package offline

import (
	"context"
	"strconv"
	"time"

	"accessnow/internal/core/gates"
	"accessnow/internal/core/movements"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// The ledger is the half of this module that Core drives. It is a book of what a replayed
// queue came to, and nothing in it can stop a scan from landing: a tablet that has been
// holding a shift's worth of movements must be able to hand them in even on the afternoon the
// counting broke.

// Open files a batch under the name the device gave it, before any of its rows land, so the
// movements can point at the batch that carried them.
//
// A queue with no name on it cannot be filed, and that is reported as an error rather than
// quietly skipped, because the alternative is a device whose batches are never counted and
// nobody can tell it why. Core logs the error and replays anyway.
func (s *Service) Open(ctx context.Context, scope tenancy.Scope,
	queue movements.Queue) (int64, error) {
	if scope.DeviceID == 0 {
		return 0, errs.Forbidden("a batch is filed against a device, not a session")
	}
	if queue.BatchID == "" {
		return 0, errs.BadField("batch_id", "a replayed queue has to name its own batch")
	}
	on, err := s.enabled(ctx, scope.TenantID)
	if err != nil {
		return 0, err
	}
	if !on {
		return 0, nil
	}

	device, err := s.devices.DeviceByID(ctx, scope.TenantID, scope.DeviceID)
	if err != nil {
		return 0, err
	}
	batch, err := s.repo.FileBatch(ctx, NewBatch{
		TenantID: scope.TenantID, DeviceID: device.ID, GateID: nullableGate(device),
		ClientID: queue.BatchID, Kind: KindMovements, Direction: directionUpload,
		Status: StatusProcessing, CacheFrom: device.CacheVersion, RequestedBy: scope.UserID,
		At: time.Now().UTC(),
	})
	if err != nil {
		return 0, err
	}
	return batch.ID, nil
}

// Close writes what the queue came to and files one argument per row the two sides do not
// agree about.
//
// A batch that is already closed is a resend, and a resend changes no counts: the numbers on
// a closed batch say what landed the first time the device delivered it, and a retry that adds
// nothing but duplicates must not rewrite a shift that has already been reported.
func (s *Service) Close(ctx context.Context, scope tenancy.Scope, batchID int64,
	queue movements.Queue, results []movements.Synced) (string, error) {
	if batchID == 0 {
		return "", nil
	}
	on, err := s.enabled(ctx, scope.TenantID)
	if err != nil || !on {
		return "", err
	}
	batch, err := s.repo.BatchByID(ctx, scope.TenantID, batchID)
	if err != nil {
		return "", err
	}
	if batch.Closed() {
		if _, err := s.repo.RefreshConflicts(ctx, scope.TenantID, batchID); err != nil {
			return "", err
		}
		return batch.Status, nil
	}

	device, err := s.devices.DeviceByID(ctx, scope.TenantID, batch.DeviceID)
	if err != nil {
		return "", err
	}
	at := time.Now().UTC()
	tally, conflicts := reconcile(device, batchID, queue.Scans, results, at)

	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo, devices := s.repo.In(c), s.devices.In(c)
		for _, argument := range conflicts {
			if _, err := repo.FileConflict(ctx, argument); err != nil {
				return err
			}
		}
		// The count on the batch is the number of rows the desk can open, not the number of
		// arguments reconcile thought of: two queue rows can carry the device's name for one
		// movement, and the second of them joins the argument already filed rather than adding
		// one. A supervisor told there are six conflicts who finds five opens them all and
		// spends the shift looking for the sixth.
		filed, err := repo.RefreshConflicts(ctx, scope.TenantID, batchID)
		if err != nil {
			return err
		}
		tally.Conflicts = filed
		tally.Status = cameTo(tally)
		done, err := repo.CloseBatch(ctx, scope.TenantID, batchID, tally, at)
		if err != nil {
			return err
		}
		if !done {
			// Somebody else closed this batch between the read above and the write here, which
			// means they counted it and this attempt is the resend. Their numbers stand.
			return nil
		}
		if err := devices.StampSync(ctx, scope.TenantID, batch.DeviceID, batch.CacheFrom,
			syncStatus(tally), at); err != nil {
			return err
		}
		if tally.Conflicts == 0 {
			return nil
		}
		return s.events.Publish(ctx, c, events.Event{
			TenantID: scope.TenantID, EventKey: ConflictsFound,
			AggregateType: "device_sync_batch", AggregateID: batchID,
			SiteID: scope.SiteID, GateID: batch.GateID, ActorUserID: scope.UserID,
			DedupeKey: ConflictsFound + ":" + strconv.FormatInt(batchID, 10),
			Payload: map[string]any{
				"device_label": device.Label, "conflict_count": tally.Conflicts,
				"record_count": tally.Records, "client_batch_id": batch.ClientBatchID,
				"device_first_at": firstOrNull(tally.FirstAt),
			},
		})
	})
	if err != nil {
		return "", err
	}
	return string(tally.Status), nil
}

// reconcile is the whole argument between a device and the server, worked out from what Core
// answered rather than from anything this module re-decides.
//
// One row gets one conflict, chosen in this order, because the key the argument is filed under
// is the device's own movement id and a second opinion about the same row would overwrite the
// first. The order is by what can still be learned from the row: a clock that cannot be
// believed makes every other fact about the row equally unreliable, a row already on file is
// settled before it is argued about, a pass the server has never seen has nothing else to say,
// and only then does the disagreement about whether the gate should have opened get filed.
func reconcile(device gates.Device, batchID int64, scans []movements.Scan,
	results []movements.Synced, at time.Time) (Tally, []NewConflict) {
	tally := Tally{Records: len(results), Status: StatusCompleted}
	conflicts := make([]NewConflict, 0, len(results))

	for i, got := range results {
		if i >= len(scans) {
			// Core answers one item per row submitted, so this cannot be reached by anything
			// that came through the gate surface. It is here because the alternative to
			// checking is indexing a slice by a number that came from a loop bound.
			break
		}
		scan := scans[i]
		tally.firstLast(scan)

		switch {
		case !scan.OccurredAt.IsZero() && scan.OccurredAt.After(at.Add(clockSkewTolerance)):
			tally.Conflicts++
			conflicts = append(conflicts, argument(device, batchID, scan, got,
				ConflictClock, ResolvedLate, at))
		case got.Duplicate:
			tally.Duplicates++
			tally.Conflicts++
			conflicts = append(conflicts, argument(device, batchID, scan, got,
				ConflictDuplicate, ResolvedDropped, at))
		case got.Err != nil:
			tally.Rejected++
			if errs.CodeOf(got.Err) == errs.CodeNotFound {
				tally.Conflicts++
				conflicts = append(conflicts, argument(device, batchID, scan, got,
					ConflictUnknown, ResolvedReview, at))
			}
		case i > 0 && outOfOrder(scans[i-1], scan):
			tally.Accepted++
			tally.Conflicts++
			conflicts = append(conflicts, argument(device, batchID, scan, got,
				ConflictOutOfOrder, ResolvedBoth, at))
		case got.Result == movements.ResultDenied:
			tally.Accepted++
			tally.Conflicts++
			conflicts = append(conflicts, argument(device, batchID, scan, got,
				ConflictStatus, ResolvedKept, at))
		default:
			tally.Accepted++
		}
	}
	tally.Status = cameTo(tally)
	return tally, conflicts
}

// cameTo is the one word for a whole queue. A batch whose every row failed is a batch that did
// not arrive; one that landed most of what it carried and argued about the rest is not a
// success the desk should be able to sort away.
func cameTo(t Tally) Status {
	switch {
	case t.Records == 0:
		return StatusFailed
	case t.Rejected == t.Records:
		return StatusFailed
	case t.Conflicts > 0 || t.Rejected > 0:
		return StatusPartial
	default:
		return StatusCompleted
	}
}

func syncStatus(t Tally) gates.SyncStatus {
	if t.Status == StatusCompleted {
		return gates.SyncSynced
	}
	return gates.SyncFault
}

// outOfOrder is the device disagreeing with itself: a queue whose own timestamps run backwards
// against the order it was sent in. It says nothing about the gate and everything about the
// queue, and it is the one conflict a tablet can be blamed for, because the two rows it is
// arguing about both arrived in the same batch.
func outOfOrder(previous, current movements.Scan) bool {
	if previous.OccurredAt.IsZero() || current.OccurredAt.IsZero() {
		return false
	}
	return current.OccurredAt.Before(previous.OccurredAt)
}

func argument(device gates.Device, batchID int64, scan movements.Scan, got movements.Synced,
	kind ConflictType, done Resolution, at time.Time) NewConflict {
	return NewConflict{
		TenantID: device.TenantID, BatchID: batchID, DeviceID: device.ID, Type: kind,
		UUID: scan.DeviceUUID, MovementID: got.MovementID,
		// The server's answer names the pass; the device's row only names what it read, which
		// for an unknown pass is a number nothing on this tenant holds.
		PassID: firstOfInt(got.PassID, scan.PassID),
		Detail: said(scan, got, at), Resolution: done, At: at,
	}
}

func firstOfInt(candidates ...int64) int64 {
	for _, c := range candidates {
		if c != 0 {
			return c
		}
	}
	return 0
}

// said is both sides of the disagreement in one row: what the tablet claims it did, and what
// the server answered. A conflict a supervisor cannot read is a conflict that gets resolved by
// guessing, and the guess would be made about a gate that is no longer open.
func said(scan movements.Scan, got movements.Synced, at time.Time) map[string]any {
	out := map[string]any{
		"server": map[string]any{"result": string(got.Result), "reason": got.Reason,
			"movement_id": got.MovementID, "answered_at": at},
		"device": map[string]any{"direction": string(scan.Direction),
			"pass_no": scan.PassNo, "pass_id": scan.PassID, "occurred_at": timeOrNull(scan.OccurredAt),
			"reason": scan.Reason, "source": string(scan.Source)},
	}
	if got.Err != nil {
		out["server"].(map[string]any)["error"] = got.Err.Error()
	}
	return out
}

// firstLast keeps the two device timestamps a batch is judged by: how long its queue reached
// back, and when its newest claim was made. They are the only times in the row that belong to
// the device rather than to the server.
func (t *Tally) firstLast(scan movements.Scan) {
	if scan.OccurredAt.IsZero() {
		return
	}
	if t.FirstAt == nil || scan.OccurredAt.Before(*t.FirstAt) {
		value := scan.OccurredAt.UTC()
		t.FirstAt = &value
	}
	if t.LastAt == nil || scan.OccurredAt.After(*t.LastAt) {
		value := scan.OccurredAt.UTC()
		t.LastAt = &value
	}
}

func nullableGate(device gates.Device) int64 {
	if device.GateID == nil {
		return 0
	}
	return *device.GateID
}

func timeOrNull(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func firstOrNull(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}
