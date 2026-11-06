// Package gates is Core's view of a physical checkpoint: which gates exist, which ones a
// guard may work, and which one they are standing at right now.
package gates

import "time"

type Gate struct {
	ID                       int64    `db:"id"`
	TenantID                 int64    `db:"tenant_id"`
	SiteID                   int64    `db:"site_id"`
	Code                     string   `db:"code"`
	Name                     string   `db:"name"`
	GateType                 string   `db:"gate_type"`
	Direction                string   `db:"direction"`
	Status                   string   `db:"status"`
	IsDefault                bool     `db:"is_default"`
	OfflineCapable           bool     `db:"offline_capable"`
	SameGateExitRequired     bool     `db:"same_gate_exit_required"`
	RequiresReasonOnOverride bool     `db:"requires_reason_on_override"`
	Categories               []string `json:"categories,omitempty"`
}

type Shift struct {
	ID          int64      `db:"id"`
	TenantID    int64      `db:"tenant_id"`
	SiteID      int64      `db:"site_id"`
	UserID      int64      `db:"user_id"`
	GateID      int64      `db:"gate_id"`
	DeviceID    *int64     `db:"device_id"`
	OpenedAt    time.Time  `db:"opened_at"`
	ClosedAt    *time.Time `db:"closed_at"`
	CloseReason *string    `db:"close_reason"`
}

func (s Shift) Open() bool { return s.ClosedAt == nil }

// SyncStatus is how the last exchange with a device ended, as the device row records it.
type SyncStatus string

const (
	// SyncPending is where an enrolled device starts: it has never been given a cache.
	SyncPending SyncStatus = "pending"
	// SyncSynced is the ordinary answer: the device holds what the server just told it to.
	SyncSynced SyncStatus = "synced"
	// SyncFault means the last time this device talked, the server had a problem with what it
	// said. It is a state a supervisor is meant to notice, not one a device reads.
	SyncFault SyncStatus = "error"
)

type Device struct {
	ID        int64      `db:"id"`
	TenantID  int64      `db:"tenant_id"`
	SiteID    int64      `db:"site_id"`
	GateID    *int64     `db:"gate_id"`
	Label     string     `db:"label"`
	IsShared  bool       `db:"is_shared"`
	IsActive  bool       `db:"is_active"`
	RevokedAt *time.Time `db:"revoked_at"`
	// SyncStatus, CacheVersion and LastSyncAt are the device's side of offline gate mode: what
	// it was last told to hold, which epoch of it it holds, and when it last said anything.
	// They are read here rather than by the module because this row is Core's, and a device
	// that has been told a stale cache is holding a pass it has no way of knowing it lost.
	SyncStatus   SyncStatus `db:"sync_status"`
	CacheVersion int64      `db:"cache_version"`
	LastSyncAt   *time.Time `db:"last_sync_at"`
	LastSeenAt   *time.Time `db:"last_seen_at"`
}

// Usable is the whole test for "this tablet may work this gate right now".
func (d Device) Usable() bool {
	return d.IsActive && d.RevokedAt == nil && d.GateID != nil && *d.GateID > 0
}
