package offline

import (
	"testing"
	"time"

	"accessnow/internal/core/gates"
	"accessnow/internal/core/movements"
	"accessnow/internal/platform/errs"
)

var (
	// The three clocks a replayed row can be judged by: when the batch was handed in, and two
	// of the device's own claims about when it walked somebody through the gate.
	handedIn   = time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	tenThirty  = handedIn.Add(-90 * time.Minute)
	tenFifteen = handedIn.Add(-105 * time.Minute)
)

func device() gates.Device { return gates.Device{ID: 7, TenantID: 3, Label: "TAB1"} }

// A queue that arrived whole and agreed with the server is the common case, and the one worth
// pinning down: every rule below has to leave it with nothing to show for itself.
func TestReconcileCountsACleanQueue(t *testing.T) {
	scans := []movements.Scan{
		{DeviceUUID: "a-1", PassNo: "VIS-01", OccurredAt: tenFifteen},
		{DeviceUUID: "a-2", PassNo: "VIS-02", OccurredAt: tenThirty},
	}
	results := []movements.Synced{
		{Index: 0, Result: movements.ResultGranted, MovementID: 101},
		{Index: 1, Result: movements.ResultWarned, MovementID: 102},
	}

	tally, conflicts := reconcile(device(), 55, scans, results, handedIn)
	if len(conflicts) != 0 {
		t.Fatalf("a clean queue filed %v, want no arguments", conflicts)
	}
	if tally.Status != StatusCompleted || tally.Accepted != 2 || tally.Conflicts != 0 {
		t.Errorf("tally = %+v, want completed with two accepted", tally)
	}
	if tally.FirstAt == nil || !tally.FirstAt.Equal(tenFifteen) {
		t.Errorf("first = %v, want the earliest device claim %v", tally.FirstAt, tenFifteen)
	}
	if tally.LastAt == nil || !tally.LastAt.Equal(tenThirty) {
		t.Errorf("last = %v, want the newest device claim %v", tally.LastAt, tenThirty)
	}
}

// One row, one argument, filed under the device's own name for it - which is the only name the
// tablet will repeat when it sends the queue again.
func TestReconcileFilesOneArgumentPerRow(t *testing.T) {
	cases := []struct {
		name       string
		scans      []movements.Scan
		results    []movements.Synced
		wantType   ConflictType
		wantRes    Resolution
		wantCounts Tally
	}{
		{
			name: "a row already on file",
			scans: []movements.Scan{
				{DeviceUUID: "b-1", PassNo: "VIS-01", OccurredAt: tenThirty},
			},
			results: []movements.Synced{
				{Index: 0, Result: movements.ResultGranted, MovementID: 201, Duplicate: true},
			},
			wantType: ConflictDuplicate, wantRes: ResolvedDropped,
			wantCounts: Tally{Duplicates: 1, Conflicts: 1, Records: 1},
		},
		{
			name: "a clock that ran into the future",
			scans: []movements.Scan{
				{DeviceUUID: "b-2", PassNo: "VIS-02", OccurredAt: handedIn.Add(10 * time.Minute)},
			},
			results: []movements.Synced{
				{Index: 0, Result: movements.ResultGranted, MovementID: 202, Duplicate: true},
			},
			// The clock is argued about first: a row whose time cannot be believed has nothing
			// else worth filing either, and one row gets one argument.
			wantType: ConflictClock, wantRes: ResolvedLate,
			wantCounts: Tally{Conflicts: 1, Records: 1},
		},
		{
			name: "a pass the server has never seen",
			scans: []movements.Scan{
				{DeviceUUID: "b-3", PassNo: "VIS-03", PassID: 88, OccurredAt: tenThirty},
			},
			results: []movements.Synced{
				{Index: 0, Err: errs.NotFound("pass"), MovementID: 0},
			},
			wantType: ConflictUnknown, wantRes: ResolvedReview,
			wantCounts: Tally{Rejected: 1, Conflicts: 1, Records: 1},
		},
		{
			name: "a queue whose own timestamps run backwards",
			scans: []movements.Scan{
				{DeviceUUID: "b-4", PassNo: "VIS-04", OccurredAt: tenThirty},
				{DeviceUUID: "b-5", PassNo: "VIS-05", OccurredAt: tenFifteen},
			},
			results: []movements.Synced{
				{Index: 0, Result: movements.ResultGranted, MovementID: 204},
				{Index: 1, Result: movements.ResultGranted, MovementID: 205},
			},
			// Both rows stay: the argument is about the order, not about whether they happened.
			wantType: ConflictOutOfOrder, wantRes: ResolvedBoth,
			wantCounts: Tally{Accepted: 2, Conflicts: 1, Records: 2},
		},
		{
			name: "a gate the device opened and the server would not",
			scans: []movements.Scan{
				{DeviceUUID: "b-6", PassNo: "VIS-06", PassID: 90, OccurredAt: tenThirty},
			},
			results: []movements.Synced{
				{Index: 0, Result: movements.ResultDenied, Reason: "not valid here", MovementID: 206},
			},
			wantType: ConflictStatus, wantRes: ResolvedKept,
			wantCounts: Tally{Accepted: 1, Conflicts: 1, Records: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tally, conflicts := reconcile(device(), 55, tc.scans, tc.results, handedIn)
			if len(conflicts) != 1 {
				t.Fatalf("filed %d arguments, want exactly one: %+v", len(conflicts), conflicts)
			}
			got := conflicts[0]
			if got.Type != tc.wantType {
				t.Errorf("type = %s, want %s", got.Type, tc.wantType)
			}
			if got.Resolution != tc.wantRes {
				t.Errorf("resolution = %s, want %s", got.Resolution, tc.wantRes)
			}
			if got.BatchID != 55 || got.DeviceID != 7 || got.TenantID != 3 {
				t.Errorf("filed under %+v, want tenant 3, device 7, batch 55", got)
			}
			if got.UUID != tc.scans[len(tc.scans)-1].DeviceUUID {
				t.Errorf("argued about %s, want the row's own device uuid", got.UUID)
			}
			if tally.Records != tc.wantCounts.Records || tally.Accepted != tc.wantCounts.Accepted ||
				tally.Duplicates != tc.wantCounts.Duplicates ||
				tally.Rejected != tc.wantCounts.Rejected ||
				tally.Conflicts != tc.wantCounts.Conflicts {
				t.Errorf("counts = %+v, want records %d, accepted %d, duplicates %d, rejected %d, conflicts %d",
					tally, tc.wantCounts.Records, tc.wantCounts.Accepted, tc.wantCounts.Duplicates,
					tc.wantCounts.Rejected, tc.wantCounts.Conflicts)
			}
		})
	}
}

// A queue that is all refusals did not arrive; one that arrived and argued is not the same
// thing as a queue that arrived, and a desk that cannot tell them apart sorts by the wrong word.
func TestCameToNamesWhatABatchCameTo(t *testing.T) {
	cases := []struct {
		name  string
		tally Tally
		want  Status
	}{
		{"empty", Tally{Records: 0}, StatusFailed},
		{"nothing landed", Tally{Records: 4, Rejected: 4}, StatusFailed},
		{"one row argued about", Tally{Records: 4, Accepted: 3, Conflicts: 1}, StatusPartial},
		{"one row refused", Tally{Records: 4, Accepted: 3, Rejected: 1}, StatusPartial},
		{"a retried queue argues about itself", Tally{Records: 4, Accepted: 4, Duplicates: 2,
			Conflicts: 2}, StatusPartial},
		{"all of it landed", Tally{Records: 4, Accepted: 4}, StatusCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tally := tc.tally
			if got := cameTo(tally); got != tc.want {
				t.Errorf("cameTo(%+v) = %s, want %s", tally, got, tc.want)
			}
		})
	}
}

// A batch that only partly arrived leaves the tablet marked as a fault: the server has the rows
// it was sent, and the desk is the only thing that can notice the rest.
func TestSyncStatusFaultsAPartialBatch(t *testing.T) {
	if got := syncStatus(Tally{Status: StatusCompleted}); got != gates.SyncSynced {
		t.Errorf("a completed batch stamped %s, want synced", got)
	}
	for _, status := range []Status{StatusPartial, StatusFailed} {
		if got := syncStatus(Tally{Status: status}); got != gates.SyncFault {
			t.Errorf("a %s batch stamped %s, want the fault word", status, got)
		}
	}
}

// Both sides of the argument have to be in the row: a supervisor deciding ten minutes after a
// shift cannot remember what the guard at the barrier was told.
func TestConflictSaysWhatBothSidesSaid(t *testing.T) {
	scan := movements.Scan{DeviceUUID: "c-1", PassNo: "VIS-07", Direction: movements.DirectionIn,
		OccurredAt: tenThirty, Reason: "on site, left early"}
	got := movements.Synced{Index: 0, Result: movements.ResultDenied, Reason: "window closed",
		MovementID: 301}

	detail := said(scan, got, handedIn)
	server, ok := detail["server"].(map[string]any)
	if !ok || server["reason"] != "window closed" || server["result"] != "denied" {
		t.Fatalf("server side = %v, want the sentence the gate answered with", detail["server"])
	}
	device, ok := detail["device"].(map[string]any)
	if !ok || device["pass_no"] != "VIS-07" || device["reason"] != "on site, left early" {
		t.Fatalf("device side = %v, want what the tablet claimed it did", detail["device"])
	}
	if device["occurred_at"] != tenThirty {
		t.Errorf("device time = %v, want its own timestamp", device["occurred_at"])
	}
}

// A refused row that is not a missing pass is refused, not argued about: the device was told
// the same thing live, so there is nothing for a supervisor to sort out.
func TestRejectedRowWithoutAMissingPassIsNotAConflict(t *testing.T) {
	scans := []movements.Scan{{DeviceUUID: "d-1", PassNo: "VIS-08", OccurredAt: tenThirty}}
	results := []movements.Synced{{Index: 0, Err: errs.BadField("idempotency_key", "required")}}

	tally, conflicts := reconcile(device(), 55, scans, results, handedIn)
	if len(conflicts) != 0 {
		t.Fatalf("a refused row filed %v, want nothing argued about", conflicts)
	}
	if tally.Rejected != 1 || tally.Conflicts != 0 {
		t.Errorf("tally = %+v, want one rejection and no conflicts", tally)
	}
}
