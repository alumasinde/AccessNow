package audit

import (
	"testing"
	"time"
)

// TestVerifyChainAcceptsAnIntactTrail builds the chain the writer would build and checks the
// verifier agrees with it.
func TestVerifyChainAcceptsAnIntactTrail(t *testing.T) {
	at := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	rows := []ChainRow{
		{Seq: 1, TenantID: 7, ActorType: "user", Action: "pass.issued", SubjectType: "pass",
			SubjectID: 12, OccurredAt: at},
		{Seq: 2, TenantID: 7, ActorType: "user", Action: "gate.scan", SubjectType: "movement",
			SubjectID: 44, OccurredAt: at.Add(time.Minute)},
	}
	for i := range rows {
		if i > 0 {
			rows[i].PrevHash = rows[i-1].RowHash
		}
		rows[i].RowHash = rows[i].ComputedHash()
	}

	finding := VerifyChain(rows)
	if !finding.OK {
		t.Fatalf("intact chain reported as broken at seq %d", finding.FirstMismatchSeq)
	}
	if finding.Checked != 2 {
		t.Fatalf("checked %d rows, want 2", finding.Checked)
	}
	if finding.TipHash != rows[1].RowHash {
		t.Fatal("tip hash is not the last row's hash")
	}
}

// TestVerifyChainCatchesAnEditedRow is the point of the feature: a row whose content changed
// after it was written no longer hashes to what it stored.
func TestVerifyChainCatchesAnEditedRow(t *testing.T) {
	at := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	rows := []ChainRow{
		{Seq: 1, TenantID: 7, ActorType: "user", Action: "pass.issued", SubjectType: "pass",
			SubjectID: 12, OccurredAt: at},
		{Seq: 2, TenantID: 7, ActorType: "user", Action: "gate.scan", SubjectType: "movement",
			SubjectID: 44, OccurredAt: at.Add(time.Minute)},
	}
	for i := range rows {
		if i > 0 {
			rows[i].PrevHash = rows[i-1].RowHash
		}
		rows[i].RowHash = rows[i].ComputedHash()
	}
	rows[0].Action = "pass.cancelled"

	finding := VerifyChain(rows)
	if finding.OK {
		t.Fatal("an edited row must not verify")
	}
	if finding.FirstMismatchSeq != 1 {
		t.Fatalf("mismatch at seq %d, want 1", finding.FirstMismatchSeq)
	}
}

// TestVerifyChainCatchesARemovedRow is the tamper a per-row hash cannot see on its own: the
// remaining rows still hash correctly, but the sequence has a hole in it.
func TestVerifyChainCatchesARemovedRow(t *testing.T) {
	at := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	rows := []ChainRow{
		{Seq: 1, TenantID: 7, ActorType: "user", Action: "pass.issued", SubjectType: "pass",
			SubjectID: 12, OccurredAt: at},
		{Seq: 2, TenantID: 7, ActorType: "user", Action: "gate.scan", SubjectType: "movement",
			SubjectID: 44, OccurredAt: at.Add(time.Minute)},
		{Seq: 3, TenantID: 7, ActorType: "user", Action: "gate.scan", SubjectType: "movement",
			SubjectID: 45, OccurredAt: at.Add(2 * time.Minute)},
	}
	for i := range rows {
		if i > 0 {
			rows[i].PrevHash = rows[i-1].RowHash
		}
		rows[i].RowHash = rows[i].ComputedHash()
	}

	finding := VerifyChain([]ChainRow{rows[0], rows[2]})
	if finding.OK {
		t.Fatal("a chain with a missing row must not verify")
	}
	if finding.FirstMismatchSeq != 3 {
		t.Fatalf("mismatch at seq %d, want 3", finding.FirstMismatchSeq)
	}
}
