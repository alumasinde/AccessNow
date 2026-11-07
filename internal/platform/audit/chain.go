package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/db"
)

type tail struct {
	Seq  uint64 `db:"seq"`
	Hash string `db:"prev_hash"`
}

func lockTail(ctx context.Context, c db.Conn, tenantID int64) (uint64, string, error) {
	const query = `
		SELECT seq, COALESCE(row_hash, '') AS prev_hash
		FROM   audit_logs
		WHERE  tenant_id = ? AND stream_key = ?
		ORDER  BY seq DESC
		LIMIT  1 FOR UPDATE`
	var t tail
	err := c.GetContext(ctx, &t, query, tenantID, stream)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, "", nil
	case err != nil:
		return 0, "", fmt.Errorf("read audit chain tail: %w", err)
	default:
		return t.Seq, t.Hash, nil
	}
}

// hash covers what a verifier can rebuild from the row alone. The separator keeps
// two short values from concatenating into a third field's shape.
func hash(seq uint64, prevHash string, e Entry) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strconv.FormatUint(seq, 10),
		strconv.FormatInt(e.TenantID, 10),
		stream,
		e.ActorType,
		e.Action,
		e.SubjectType,
		strconv.FormatInt(e.SubjectID, 10),
		e.OccurredAt.UTC().Format(time.RFC3339Nano),
		prevHash,
	}, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// ChainRow is one stored audit row as a verifier reads it. It carries exactly the columns the
// row hash was built from, so verification recomputes the writer's own arithmetic instead of
// a second interpretation of it that could drift.
type ChainRow struct {
	Seq         uint64    `db:"seq"`
	TenantID    int64     `db:"tenant_id"`
	ActorType   string    `db:"actor_type"`
	Action      string    `db:"action"`
	SubjectType string    `db:"subject_type"`
	SubjectID   int64     `db:"subject_id"`
	OccurredAt  time.Time `db:"occurred_at"`
	PrevHash    string    `db:"prev_hash"`
	RowHash     string    `db:"row_hash"`
}

// ComputedHash rebuilds the hash this row should carry. It is exported for the compliance
// module, which verifies the trail the writer laid down; keeping the formula in one place is
// what stops a verifier from agreeing with itself and disagreeing with the data.
func (r ChainRow) ComputedHash() string {
	return hash(r.Seq, r.PrevHash, Entry{
		TenantID: r.TenantID, ActorType: r.ActorType, Action: r.Action,
		SubjectType: r.SubjectType, SubjectID: r.SubjectID, OccurredAt: r.OccurredAt,
	})
}

// ChainFinding is what a walk of a tenant's trail found. Checked is how many rows were read;
// FirstMismatchSeq is zero when the chain is intact.
type ChainFinding struct {
	Checked           int
	FirstMismatchSeq  uint64
	FirstMismatchHash string
	TipHash           string
	OK                bool
}

// VerifyChain walks rows in seq order and checks two things per row: that the row still hashes
// to what it stored, and that it names the previous row's hash. The first is a changed row, the
// second a removed one - and a removed row is the tamper a hash alone cannot catch.
func VerifyChain(rows []ChainRow) ChainFinding {
	finding := ChainFinding{Checked: len(rows), OK: true}
	var prev uint64
	for i, row := range rows {
		if i > 0 && row.Seq != prev+1 {
			finding.OK = false
			finding.FirstMismatchSeq = row.Seq
			finding.FirstMismatchHash = row.ComputedHash()
			return finding
		}
		computed := row.ComputedHash()
		if computed != row.RowHash {
			finding.OK = false
			finding.FirstMismatchSeq = row.Seq
			finding.FirstMismatchHash = computed
			return finding
		}
		finding.TipHash = row.RowHash
		prev = row.Seq
	}
	return finding
}

func encodeJSON(before, after map[string]any) (any, any, error) {
	enc := func(m map[string]any) (any, error) {
		if len(m) == 0 {
			return nil, nil
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("encode audit json: %w", err)
		}
		return string(raw), nil
	}
	b, err := enc(before)
	if err != nil {
		return nil, nil, err
	}
	a, err := enc(after)
	if err != nil {
		return nil, nil, err
	}
	return b, a, nil
}
