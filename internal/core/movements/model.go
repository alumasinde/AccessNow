// Package movements is the append-only gate log. Nothing updates a movement row: an exit
// is a new row that points back at the entry it closes.
package movements

import "time"

type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

type Result string

const (
	ResultGranted  Result = "granted"
	ResultDenied   Result = "denied"
	ResultWarned   Result = "warned"
	ResultOverride Result = "override"
)

type Source string

const (
	SourceScan    Source = "scan"
	SourceManual  Source = "manual"
	SourceOffline Source = "offline_sync"
	SourceAPI     Source = "api"
	SourceCamera  Source = "camera"
	SourceSystem  Source = "system"
)

// Scan is one gate decision request: a guard, a gate from the session or the device, and
// the pass either by id or by the number printed on it.
type Scan struct {
	PassID         int64
	PassNo         string
	Direction      Direction
	Source         Source
	Reason         string
	IdempotencyKey string
	SubjectName    string
	Plate          string
	OccurredAt     time.Time
}

type Movement struct {
	ID                int64     `db:"id"`
	UUID              string    `db:"uuid"`
	TenantID          int64     `db:"tenant_id"`
	SiteID            int64     `db:"site_id"`
	GateID            int64     `db:"gate_id"`
	PassID            *int64    `db:"pass_id"`
	GateSessionID     *int64    `db:"gate_session_id"`
	GuardUserID       *int64    `db:"guard_user_id"`
	Direction         string    `db:"direction"`
	Result            string    `db:"result"`
	Source            string    `db:"source"`
	Reason            string    `db:"reason_note"` // read with COALESCE, the column is nullable
	OccurredAt        time.Time `db:"occurred_at"`
	SubjectName       *string   `db:"subject_name"`
	Plate             *string   `db:"plate"`
	DwellMinutes      *int      `db:"dwell_minutes"`
	CloseOfMovementID *int64    `db:"close_of_movement_id"`
	SyncBatchID       *int64    `db:"sync_batch_id"`
	IsLateSync        bool      `db:"is_late_sync"`
}

// Outcome is what the gate screen and the tablet need: one colour, one line, and the row
// that was written.
type Outcome struct {
	Movement Movement
	Result   Result
	Reason   string
	PassNo   string
	Status   string
}

func (o Outcome) Green() bool {
	return o.Result == ResultGranted || o.Result == ResultWarned || o.Result == ResultOverride
}

// Occupant is one row of the roll-call roster: still on site, with the minutes counted by
// the view so the server never re-derives presence.
type Occupant struct {
	MovementID    int64     `db:"id" json:"movement_id"`
	PassID        int64     `db:"pass_id" json:"pass_id"`
	GateID        int64     `db:"gate_id" json:"gate_id"`
	SubjectName   *string   `db:"subject_name" json:"subject_name,omitempty"`
	Plate         *string   `db:"plate" json:"plate,omitempty"`
	EnteredAt     time.Time `db:"occurred_at" json:"entered_at"`
	MinutesOnSite int       `db:"minutes_on_site" json:"minutes_on_site"`
}
