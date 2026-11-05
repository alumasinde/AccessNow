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
