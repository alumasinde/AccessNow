package main

import (
	"context"

	"accessnow/internal/platform/db"
)

// ensureOfflineGate switches the seeded tenant on for offline gate mode.
//
// That is the whole of it, and deliberately so: the gate the seed builds is already marked
// offline_capable, and the tablet it enrolls is the one whose token gets printed at the end of
// the run. So a fresh tenant can pull a cache with that token, replay a queue at /gate/sync,
// and read what the ledger made of it - which is the only way this module can be tried without
// unplugging a real gate.
func ensureOfflineGate(ctx context.Context, c db.Conn, tenantID int64) error {
	if _, err := c.ExecContext(ctx,
		`UPDATE tenant_modules SET enabled = 1 WHERE tenant_id = ? AND module_key = 'offline_gate'`,
		tenantID); err != nil {
		return wrap(err, "enable offline_gate")
	}
	return nil
}
