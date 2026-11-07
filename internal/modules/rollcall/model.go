package rollcall

import "time"

// ModuleKey is the tenant's switch for this surface. The tables exist either way; the roster is
// frozen from Core's open movements, so an incident never depends on another module being on.
const ModuleKey = "emergency_rollcall"

const aggregateType = "evacuation_incident"

// defaultPage is the roster page. A muster point is read in one go, so the default is larger
// than a desk register's.
const defaultPage = 100

// Kind mirrors chk_evacuation_incidents_kind.
type Kind string

const (
	KindFire          Kind = "fire"
	KindMedical       Kind = "medical"
	KindSecurity      Kind = "security"
	KindDrill         Kind = "evacuation_drill"
	KindNaturalHazard Kind = "natural_hazard"
	KindChemical      Kind = "chemical"
	KindOther         Kind = "other"
)

func (k Kind) valid() bool {
	switch k {
	case KindFire, KindMedical, KindSecurity, KindDrill, KindNaturalHazard, KindChemical,
		KindOther:
		return true
	}
	return false
}

// Status mirrors chk_evacuation_incidents_status.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusClosed     Status = "closed"
	StatusCancelled  Status = "cancelled"
)

// Live reports an incident a muster point may still be marked against.
func (s Status) Live() bool { return s == StatusOpen || s == StatusInProgress }

func (s Status) valid() bool {
	switch s {
	case StatusOpen, StatusInProgress, StatusClosed, StatusCancelled:
		return true
	}
	return false
}

// PersonStatus mirrors chk_evacuation_roster_status.
type PersonStatus string

const (
	PersonUnaccounted PersonStatus = "unaccounted"
	PersonAccounted   PersonStatus = "accounted"
	PersonEvacuated   PersonStatus = "evacuated"
	PersonInjured     PersonStatus = "injured"
	PersonMissing     PersonStatus = "missing"
	PersonNotOnSite   PersonStatus = "not_on_site"
	PersonDeceased    PersonStatus = "deceased"
)

func (p PersonStatus) valid() bool {
	switch p {
	case PersonUnaccounted, PersonAccounted, PersonEvacuated, PersonInjured, PersonMissing,
		PersonNotOnSite, PersonDeceased:
		return true
	}
	return false
}

// Seen reports a status where a person has been physically confirmed: the four states a warden
// can reach with their own eyes. Missing and deceased are the two that stay on the board, and
// unaccounted is the board's default.
func (p PersonStatus) Seen() bool {
	switch p {
	case PersonAccounted, PersonEvacuated, PersonInjured, PersonNotOnSite:
		return true
	}
	return false
}

// Source mirrors chk_evacuation_roster_source.
type Source string

const (
	SourceScan   Source = "scan"
	SourceManual Source = "manual"
	SourceImport Source = "import"
	SourceSystem Source = "system"
)

func (s Source) valid() bool {
	switch s {
	case SourceScan, SourceManual, SourceImport, SourceSystem:
		return true
	}
	return false
}

// Incident is one alarm: when it started, who raised it, and how many people the frozen roster
// holds against how many have been seen.
type Incident struct {
	ID             int64      `db:"id"`
	UUID           string     `db:"uuid"`
	TenantID       int64      `db:"tenant_id"`
	SiteID         int64      `db:"site_id"`
	SiteName       string     `db:"site_name"`
	GateID         *int64     `db:"gate_id"`
	Kind           string     `db:"kind"`
	Status         string     `db:"status"`
	StartedAt      time.Time  `db:"started_at"`
	StartedBy      *int64     `db:"started_by_user_id"`
	RosterFrozenAt *time.Time `db:"roster_frozen_at"`
	Expected       int        `db:"expected_person_count"`
	Accounted      int        `db:"accounted_count"`
	MusterPoint    *string    `db:"muster_point"`
	ClosedAt       *time.Time `db:"closed_at"`
	ClosedBy       *int64     `db:"closed_by_user_id"`
	Summary        *string    `db:"summary"`
	Notes          *string    `db:"notes"`
	CreatedAt      time.Time  `db:"created_at"`
}

// Unaccounted is what a warden shouts across the car park: expected minus seen.
func (i Incident) Unaccounted() int {
	left := i.Expected - i.Accounted
	if left < 0 {
		return 0
	}
	return left
}

// Live reports an incident a mark may still be filed against.
func (i Incident) Live() bool { return Status(i.Status).Live() }

// Person is one row of the frozen roster. The snapshots are what the warden needs when the
// network is down and the pass has since been edited: a name, a phone, a plate.
type Person struct {
	ID            int64      `db:"id"`
	TenantID      int64      `db:"tenant_id"`
	IncidentID    int64      `db:"evacuation_incident_id"`
	PersonKey     string     `db:"person_key"`
	PersonType    string     `db:"person_type"`
	PassID        *int64     `db:"pass_id"`
	PassNo        string     `db:"pass_no"`
	MovementID    *int64     `db:"movement_id"`
	GateSessionID *int64     `db:"gate_session_id"`
	Name          string     `db:"name_snapshot"`
	Phone         *string    `db:"phone_snapshot"`
	Company       *string    `db:"company_snapshot"`
	Plate         *string    `db:"plate_snapshot"`
	LastSeenAt    *time.Time `db:"last_seen_at"`
	LastSeenGate  *int64     `db:"last_seen_gate_id"`
	DepartmentID  *int64     `db:"department_id"`
	Status        string     `db:"status"`
	MarkedBy      *int64     `db:"marked_by_user_id"`
	MarkedAt      *time.Time `db:"marked_at"`
	MarkedGate    *int64     `db:"marked_gate_id"`
	MusterPoint   *string    `db:"muster_point"`
	Source        string     `db:"source"`
	Note          *string    `db:"note"`
	CreatedAt     time.Time  `db:"created_at"`
}

// Mark is one transition of one person's status, kept so a closed incident can be reconstructed
// row by row rather than by trusting the last write.
type Mark struct {
	ID          int64     `db:"id"`
	TenantID    int64     `db:"tenant_id"`
	IncidentID  int64     `db:"evacuation_incident_id"`
	PersonID    int64     `db:"evacuation_roster_id"`
	FromStatus  *string   `db:"from_status"`
	ToStatus    string    `db:"to_status"`
	MarkedBy    *int64    `db:"marked_by_user_id"`
	GateID      *int64    `db:"gate_id"`
	GateSession *int64    `db:"gate_session_id"`
	MusterPoint *string   `db:"muster_point"`
	OccurredAt  time.Time `db:"occurred_at"`
	Note        *string   `db:"note"`
	PersonName  string    `db:"name_snapshot"`
}

// Counts is the two numbers an incident is drawn by.
type Counts struct {
	Expected  int `json:"expected"`
	Accounted int `json:"accounted"`
}

func (c Counts) Unaccounted() int {
	left := c.Expected - c.Accounted
	if left < 0 {
		return 0
	}
	return left
}

// personTypeOf maps the module that issued a pass onto the roster's vocabulary. A plate-bound
// vehicle pass is a driver, because that is who the warden is looking for.
func personTypeOf(moduleKey string) string {
	switch moduleKey {
	case "visitor_management":
		return "visitor"
	case "staff_outpass":
		return "staff"
	case "contractor_access":
		return "contractor"
	case "vehicle_tracking":
		return "driver"
	default:
		return "other"
	}
}

// closeRefusal keeps a supervisor from signing off an incident with people still on the board
// unless the summary says something about them. An empty summary is a closed file that answers
// no question the next inspection will ask.
func closeRefusal(counts Counts, summary string) error {
	if counts.Unaccounted() > 0 && summary == "" {
		return errsSummaryRequired
	}
	return nil
}
