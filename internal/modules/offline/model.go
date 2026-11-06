// Package offline is the server half of offline gate mode. A tablet holds today's scannable
// passes and the screening list so a gate keeps working when the link drops, then sends what
// it buffered as one batch; this module assembles that cache, files the batch it comes back
// in, and keeps the arguments the two sides end up having.
//
// It owns three tables and no decisions: whether a replayed scan was allowed is Core's answer,
// made on the server's clock, and this module only records that the device disagreed.
package offline

import (
	"context"
	"time"
)

// ModuleKey is the tenant's switch for this whole surface.
const ModuleKey = "offline_gate"

// ConflictsFound is the outbox key a batch closes under when the two sides ended up with an
// argument on file. The catalogue row is seeded with the module, so the notice wording is a
// desk decision rather than one made here; publishing it regardless is what keeps the ledger
// honest for a tenant that buys notifications later.
const ConflictsFound = "sync.conflicts_found"

// cacheHorizon is how far ahead of now a device is given passes for. A contractor's pass that
// is valid for a month is handed over with its real expiry and stays in the cache until it
// drops out of the set on the next refresh; a pass booked for next week is not in the cache
// today because nobody can scan it today.
const cacheHorizon = 24 * time.Hour

// refreshAfter is how soon the device should come back for the bundle. It is the gap between
// a pass being revoked on the desk and a tablet finding out without a link to tell it, so it
// is a safety window, not a page-refresh interval.
const refreshAfter = 15 * time.Minute

// clockSkewTolerance is how far behind the server's clock a tablet may be and still be
// believed. A device whose own timestamp is later than the moment its batch arrived is not
// slow, it is wrong: its clock cannot run into a future it has not reached, and everything it
// says happened "at" that time is a claim about a clock nobody trusts.
const clockSkewTolerance = 2 * time.Minute

// batchCeiling is the biggest queue one batch may carry. It is Core's own limit on the sync
// endpoint, repeated here so a batch that cannot be replayed is refused before any of it has
// landed rather than halfway through.
const batchCeiling = 200

// The two directions a batch moves in, as the ledger files them.
const (
	directionUpload   = "upload"
	directionDownload = "download"
)

// Kind is what a batch carries, in the direction it carries it.
type Kind string

const (
	KindMovements Kind = "movements"
	KindPasses    Kind = "passes"
	KindBlacklist Kind = "blacklist"
	KindFull      Kind = "full"
)

func (k Kind) valid() bool {
	switch k {
	case KindMovements, KindPasses, KindBlacklist, KindFull:
		return true
	}
	return false
}

// Status is where a batch got to. Superseded is the one a device can cause on its own: it
// sent the same queue twice, and the second arrival is filed as the first one's echo.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusPartial    Status = "partial"
	StatusFailed     Status = "failed"
	StatusSuperseded Status = "superseded"
)

// CacheReason is why one pass is in a device's cache, which is also why it may be dropped.
type CacheReason string

const (
	// ReasonToday is a pass whose whole life is inside the window it was cached for.
	ReasonToday CacheReason = "valid_today"
	// ReasonRange is a pass that outlives the window, so the device has to keep checking the
	// expiry it was handed rather than the bundle it came in.
	ReasonRange CacheReason = "valid_range"
	// ReasonMultiEntry is a pass that has already been scanned once and is scannable again. It
	// is the row a stale cache hangs on longest, and the one a contractor's gate needs.
	ReasonMultiEntry CacheReason = "multi_entry"
)

// ConflictType is the kind of disagreement a replayed row can cause.
//
// The table allows three more words - revoked_token, gate_mismatch and quota_exceeded - and
// nothing here writes them: a revoked QR token is a fact the scanning module owns, and a
// quota spent twice over is a fact the rulebook owns. A ledger that filed a conflict it could
// not actually see would be a ledger whose numbers nobody can trust.
type ConflictType string

const (
	// ConflictDuplicate is the device sending a movement the server already has.
	ConflictDuplicate ConflictType = "duplicate"
	// ConflictOutOfOrder is a queue whose own timestamps disagree with the order it arrived in.
	ConflictOutOfOrder ConflictType = "out_of_order"
	// ConflictStatus is the server refusing a scan the device let through. The device cached a
	// pass it was right about at the time; anything that has since made it wrong - a status a
	// desk changed, a window that closed, a listing, a rule - is the same disagreement as far
	// as the ledger is concerned, and the server's own sentence is filed in the detail.
	ConflictStatus ConflictType = "status_mismatch"
	// ConflictClock is a device timestamp that cannot be believed.
	ConflictClock ConflictType = "clock_skew"
	// ConflictUnknown is a row that names a pass the server has never heard of.
	ConflictUnknown ConflictType = "unknown_pass"
)

func (t ConflictType) valid() bool {
	switch t {
	case ConflictDuplicate, ConflictOutOfOrder, ConflictStatus, ConflictClock,
		ConflictUnknown:
		return true
	}
	return false
}

// Resolution is what was done about a conflict. The first four are what the server actually
// did while replaying the batch; the last two are a human overruling it.
type Resolution string

const (
	// ResolvedKept is the documented default: the pass keeps the status the server has, because
	// the server is the one that has been deciding about it all along.
	ResolvedKept Resolution = "server_kept_status"
	// ResolvedBoth is what an out-of-order pair ends in: both movement rows stay, because a
	// movement log that loses a scan to tidy-up is a log nobody can audit.
	ResolvedBoth Resolution = "kept_both_records"
	// ResolvedLate marks a row whose clock was not trusted, so its time is the server's.
	ResolvedLate Resolution = "marked_late"
	// ResolvedDropped is a duplicate that added nothing: the retry is filed, the row is not.
	ResolvedDropped Resolution = "dropped_duplicate"
	// ResolvedRejected is a person saying the row should never have been filed at all.
	ResolvedRejected Resolution = "rejected"
	// ResolvedReview is a person saying somebody else has to look at it first.
	ResolvedReview Resolution = "manual_review"
)

func (r Resolution) valid() bool {
	switch r {
	case ResolvedKept, ResolvedBoth, ResolvedLate, ResolvedDropped, ResolvedRejected,
		ResolvedReview:
		return true
	}
	return false
}

// deviceSyncColumns is the batch row as the ledger both writes and reads.
const deviceSyncColumns = `id, tenant_id, gate_device_id, COALESCE(gate_id, 0) AS gate_id,
	client_batch_id, kind, direction, status, started_at, finished_at, record_count,
	accepted_count, duplicate_count, rejected_count, conflict_count, device_first_at,
	device_last_at, COALESCE(cache_version_from, 0) AS cache_version_from,
	COALESCE(cache_version_to, 0) AS cache_version_to, checksum_sha256, error_code,
	error_message, COALESCE(requested_by_user_id, 0) AS requested_by_user_id, created_at`

const conflictColumns = `c.id, c.tenant_id, c.device_sync_batch_id, c.gate_device_id,
	c.conflict_type, COALESCE(c.device_movement_uuid, '') AS device_movement_uuid,
	COALESCE(c.movement_id, 0) AS movement_id, COALESCE(c.pass_id, 0) AS pass_id,
	c.detail, c.resolution, COALESCE(c.resolution_note, '') AS resolution_note, c.detected_at,
	COALESCE(c.resolved_by_user_id, 0) AS resolved_by_user_id, c.resolved_at,
	b.client_batch_id, gd.label AS device_label, COALESCE(g.name, '') AS gate_name,
	COALESCE(p.pass_no, '') AS pass_no, COALESCE(p.subject_name, '') AS subject_name,
	COALESCE(m.result, '') AS server_result,
	COALESCE(m.reason_note, '') AS server_reason`

// Batch is one exchange between a device and the server: a queue sent up, or a cache handed
// down. The same row shape serves both, because both are the ledger's own record of one
// request.
type Batch struct {
	ID            int64      `db:"id"`
	TenantID      int64      `db:"tenant_id"`
	DeviceID      int64      `db:"gate_device_id"`
	GateID        int64      `db:"gate_id"`
	ClientBatchID string     `db:"client_batch_id"`
	Kind          string     `db:"kind"`
	Direction     string     `db:"direction"`
	Status        string     `db:"status"`
	StartedAt     time.Time  `db:"started_at"`
	FinishedAt    *time.Time `db:"finished_at"`
	RecordCount   int        `db:"record_count"`
	AcceptedCount int        `db:"accepted_count"`
	Duplicate     int        `db:"duplicate_count"`
	Rejected      int        `db:"rejected_count"`
	Conflicts     int        `db:"conflict_count"`
	DeviceFirstAt *time.Time `db:"device_first_at"`
	DeviceLastAt  *time.Time `db:"device_last_at"`
	CacheFrom     int64      `db:"cache_version_from"`
	CacheTo       int64      `db:"cache_version_to"`
	Checksum      *string    `db:"checksum_sha256"`
	ErrorCode     *string    `db:"error_code"`
	ErrorMessage  *string    `db:"error_message"`
	RequestedBy   int64      `db:"requested_by_user_id"`
	CreatedAt     time.Time  `db:"created_at"`
}

// Closed reports whether a batch is still open. A device that never came back for the second
// half of an upload leaves a row in processing, and that is exactly the row a supervisor wants
// to see.
func (b Batch) Closed() bool { return b.FinishedAt != nil }

// Manifest is one pass in one device's cache at one version. It is the ledger's copy of what
// the tablet is holding, which is the only way a server can say what a gate would have done
// while nobody was watching it.
type Manifest struct {
	TenantID   int64     `db:"tenant_id"`
	DeviceID   int64     `db:"gate_device_id"`
	GateID     int64     `db:"gate_id"`
	PassID     int64     `db:"pass_id"`
	PassNo     string    `db:"pass_no"`
	Reason     string    `db:"reason"`
	Version    int64     `db:"cache_version"`
	ValidFrom  time.Time `db:"valid_from"`
	ValidUntil time.Time `db:"valid_until"`
	PushedAt   time.Time `db:"pushed_at"`
}

// Conflict is one row a device and the server do not agree about, and what was done about it.
type Conflict struct {
	ID           int64      `db:"id"`
	TenantID     int64      `db:"tenant_id"`
	BatchID      int64      `db:"device_sync_batch_id"`
	DeviceID     int64      `db:"gate_device_id"`
	Type         string     `db:"conflict_type"`
	DeviceUUID   string     `db:"device_movement_uuid"`
	MovementID   int64      `db:"movement_id"`
	PassID       int64      `db:"pass_id"`
	Detail       *string    `db:"detail"`
	Resolution   string     `db:"resolution"`
	Note         string     `db:"resolution_note"`
	DetectedAt   time.Time  `db:"detected_at"`
	ResolvedBy   int64      `db:"resolved_by_user_id"`
	ResolvedAt   *time.Time `db:"resolved_at"`
	ClientBatch  string     `db:"client_batch_id"`
	DeviceLabel  string     `db:"device_label"`
	GateName     string     `db:"gate_name"`
	PassNo       string     `db:"pass_no"`
	SubjectName  string     `db:"subject_name"`
	ServerResult string     `db:"server_result"`
	ServerReason string     `db:"server_reason"`
}

// Open reports the conflicts a supervisor still has to look at.
func (c Conflict) Open() bool { return c.ResolvedAt == nil }

// Health is one device as the desk sees it: what it holds, when it last said anything, and
// how much it still owes.
type Health struct {
	DeviceID    int64      `db:"gate_device_id"`
	TenantID    int64      `db:"tenant_id"`
	Label       string     `db:"label"`
	GateID      int64      `db:"gate_id"`
	GateName    string     `db:"gate_name"`
	SyncStatus  string     `db:"sync_status"`
	LastSeenAt  *time.Time `db:"last_seen_at"`
	LastSyncAt  *time.Time `db:"last_sync_at"`
	CacheVer    int64      `db:"cache_version"`
	OpenBatches int        `db:"open_batches"`
	Unresolved  int        `db:"unresolved_conflicts"`
}

// Silent reports whether a device has stopped talking. It is a window rather than a status
// because a tablet that is offline is silent for exactly the same reason as one that has
// fallen off the network, and the desk has to decide which one it is looking at.
func (h Health) Silent(now time.Time, after time.Duration) bool {
	if h.LastSeenAt == nil {
		return true
	}
	return now.Sub(*h.LastSeenAt) > after
}

// CachedPass is one pass as the device must hold it. No secret is in it: the QR token half is
// a different module, and until that exists a tablet honours a pass number, not a signature.
type CachedPass struct {
	PassID      int64     `json:"pass_id"`
	PassNo      string    `json:"pass_no"`
	Category    string    `json:"category"`
	Module      string    `json:"source_module"`
	Status      string    `json:"status"`
	SubjectName string    `json:"subject_name"`
	Reason      string    `json:"reason"`
	ValidFrom   time.Time `json:"valid_from"`
	ValidUntil  time.Time `json:"valid_until"`
	EntriesUsed int       `json:"entries_used"`
	PushedAt    time.Time `json:"pushed_at"`
}

// CachedListing is one screening entry as the device must hold it. It carries the value the
// tablet has to compare against, which is the one thing a cached list cannot get from a hash.
type CachedListing struct {
	ListType    string     `json:"list_type"`
	MatchType   string     `json:"match_type"`
	MatchValue  string     `json:"match_value"`
	DisplayName string     `json:"display_name,omitempty"`
	Reason      string     `json:"reason"`
	Severity    string     `json:"severity"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// Bundle is the cache a device is told to hold, and the version it now holds.
type Bundle struct {
	DeviceID     int64         `json:"device_id"`
	GateID       int64         `json:"gate_id"`
	GateName     string        `json:"gate_name"`
	Version      int64         `json:"cache_version"`
	HeldSince    time.Time     `json:"issued_at"`
	RefreshAfter time.Duration `json:"refresh_after_seconds"`
	// Fresh says the device already held everything it should, so nothing was written and the
	// epoch did not move. A tablet that is told this keeps its cache and its clock.
	Fresh bool `json:"fresh"`
	// Full says the opposite: the device's own version could not be believed, so this bundle is
	// the whole cache and anything the tablet is holding that is not in it is stale.
	Full      bool            `json:"full_rebuild"`
	Removed   []int64         `json:"removed_pass_ids"`
	Checksum  string          `json:"checksum_sha256"`
	Passes    []CachedPass    `json:"passes"`
	Screening []CachedListing `json:"screening"`
	// AllowedCategories is the gate's own list, so a device that is cut off can still refuse a
	// pass category its gate was never meant to honour instead of learning that when the link
	// comes back.
	AllowedCategories []string `json:"allowed_categories"`
	// SameGateExit is the tenant's strict mode, handed over because a device cannot ask.
	SameGateExit bool `json:"same_gate_exit_required"`
}

// Screenings is the list half of a device's cache. It is a dependency rather than a query
// because the list belongs to another module, and a module that reached into a second
// module's tables would tie their lifecycles together: a tenant that runs gates offline
// without screening may still want its tablets to hold passes.
//
// An implementation that is not wired returns nothing, and a device gets a cache with the
// passes in it, which is what a site that does not screen already asks for.
type Screenings interface {
	Cached(ctx context.Context, tenantID int64) ([]CachedListing, error)
}

// NewBatch is one exchange as the ledger files it.
type NewBatch struct {
	TenantID    int64
	DeviceID    int64
	GateID      int64
	ClientID    string
	Kind        Kind
	Direction   string
	Status      Status
	CacheFrom   int64
	RequestedBy int64
	At          time.Time
}

// NewConflict is one disagreement as the ledger finds it. Detail is what the two sides said,
// because a conflict a supervisor cannot read is a conflict that gets resolved by guessing.
type NewConflict struct {
	TenantID   int64
	BatchID    int64
	DeviceID   int64
	Type       ConflictType
	UUID       string
	MovementID int64
	PassID     int64
	Detail     map[string]any
	Resolution Resolution
	At         time.Time
}

// Tally is what a closed batch came to. Duplicates are counted separately from accepted
// because a device that retried a batch is not a device that moved twice as many people, and
// a report that adds the two together is a report about a queue nobody walked.
type Tally struct {
	Status     Status
	Records    int
	Accepted   int
	Duplicates int
	Rejected   int
	Conflicts  int
	FirstAt    *time.Time
	LastAt     *time.Time
	CacheTo    int64
	Checksum   string
	ErrorCode  string
	Message    string
}
