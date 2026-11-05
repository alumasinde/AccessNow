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

type Device struct {
	ID        int64      `db:"id"`
	TenantID  int64      `db:"tenant_id"`
	SiteID    int64      `db:"site_id"`
	GateID    *int64     `db:"gate_id"`
	Label     string     `db:"label"`
	IsShared  bool       `db:"is_shared"`
	IsActive  bool       `db:"is_active"`
	RevokedAt *time.Time `db:"revoked_at"`
}

// Usable is the whole test for "this tablet may work this gate right now".
func (d Device) Usable() bool {
	return d.IsActive && d.RevokedAt == nil && d.GateID != nil && *d.GateID > 0
}
