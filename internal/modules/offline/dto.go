package offline

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// silenceWindow is how long a device may say nothing before the desk screen calls it silent.
// It is a shift rather than a refresh interval: a tablet that has been pulled off the gate for
// lunch is not the one a supervisor has to go and find, one that has been quiet since the last
// shift change is.
const silenceWindow = 8 * time.Hour

// BundleView is the cache as the device receives it.
//
// The duration is handed over in whole seconds because that is the unit a device sets a timer
// with, and encoding a Go duration would put nanoseconds behind a field called seconds.
type BundleView struct {
	DeviceID     int64           `json:"device_id"`
	GateID       int64           `json:"gate_id"`
	GateName     string          `json:"gate_name"`
	Version      int64           `json:"cache_version"`
	HeldSince    time.Time       `json:"issued_at"`
	RefreshAfter int             `json:"refresh_after_seconds"`
	Fresh        bool            `json:"fresh"`
	Full         bool            `json:"full_rebuild"`
	Removed      []int64         `json:"removed_pass_ids"`
	Checksum     string          `json:"checksum_sha256"`
	Passes       []CachedPass    `json:"passes"`
	Screening    []CachedListing `json:"screening"`
	// Categories and SameGateExit are the gate's own rules, so an offline barrier refuses the
	// wrong-category pass for the same reason the online one would.
	Categories   []string `json:"allowed_categories"`
	SameGateExit bool     `json:"same_gate_exit_required"`
}

func (b Bundle) View() BundleView {
	return BundleView{
		DeviceID: b.DeviceID, GateID: b.GateID, GateName: b.GateName, Version: b.Version,
		HeldSince: b.HeldSince, RefreshAfter: int(b.RefreshAfter / time.Second),
		Fresh: b.Fresh, Full: b.Full, Removed: orEmpty(b.Removed), Checksum: b.Checksum,
		Passes: orEmpty(b.Passes), Screening: orEmpty(b.Screening),
		Categories: orEmpty(b.AllowedCategories), SameGateExit: b.SameGateExit,
	}
}

// orEmpty keeps every list in a bundle a list. A tablet that decodes null into an array has to
// special-case "nothing to drop" from "the server said nothing", and the two mean the same
// thing here.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// BatchView is one exchange as the ledger filed it. The counts are the whole story of an
// upload, so they are all on the row rather than behind a second call.
type BatchView struct {
	ID            int64      `json:"id"`
	DeviceID      int64      `json:"gate_device_id"`
	GateID        int64      `json:"gate_id"`
	ClientBatchID string     `json:"client_batch_id"`
	Kind          string     `json:"kind"`
	Direction     string     `json:"direction"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Records       int        `json:"record_count"`
	Accepted      int        `json:"accepted_count"`
	Duplicates    int        `json:"duplicate_count"`
	Rejected      int        `json:"rejected_count"`
	Conflicts     int        `json:"conflict_count"`
	DeviceFirstAt *time.Time `json:"device_first_at,omitempty"`
	DeviceLastAt  *time.Time `json:"device_last_at,omitempty"`
	CacheFrom     int64      `json:"cache_version_from"`
	CacheTo       int64      `json:"cache_version_to"`
	Checksum      *string    `json:"checksum_sha256,omitempty"`
	ErrorCode     *string    `json:"error_code,omitempty"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	RequestedBy   int64      `json:"requested_by_user_id"`
	Open          bool       `json:"is_open"`
}

func viewOf(b Batch) BatchView {
	return BatchView{
		ID: b.ID, DeviceID: b.DeviceID, GateID: b.GateID, ClientBatchID: b.ClientBatchID,
		Kind: b.Kind, Direction: b.Direction, Status: b.Status, StartedAt: b.StartedAt,
		FinishedAt: b.FinishedAt, Records: b.RecordCount, Accepted: b.AcceptedCount,
		Duplicates: b.Duplicate, Rejected: b.Rejected, Conflicts: b.Conflicts,
		DeviceFirstAt: b.DeviceFirstAt, DeviceLastAt: b.DeviceLastAt, CacheFrom: b.CacheFrom,
		CacheTo: b.CacheTo, Checksum: b.Checksum, ErrorCode: b.ErrorCode,
		ErrorMessage: b.ErrorMessage, RequestedBy: b.RequestedBy, Open: !b.Closed(),
	}
}

func batchViewsOf(batches []Batch) []BatchView {
	views := make([]BatchView, 0, len(batches))
	for _, batch := range batches {
		views = append(views, viewOf(batch))
	}
	return views
}

// ConflictView is one argument between a device and the server. detail is handed back as the
// object it was written as - the two sides of what each of them said - because a conflict the
// desk can only read as a blob is a conflict it resolves by guessing.
type ConflictView struct {
	ID            int64           `json:"id"`
	BatchID       int64           `json:"device_sync_batch_id"`
	ClientBatchID string          `json:"client_batch_id"`
	DeviceID      int64           `json:"gate_device_id"`
	DeviceLabel   string          `json:"device_label"`
	GateName      string          `json:"gate_name"`
	Type          string          `json:"conflict_type"`
	DeviceUUID    string          `json:"device_movement_uuid"`
	MovementID    int64           `json:"movement_id"`
	PassID        int64           `json:"pass_id"`
	PassNo        string          `json:"pass_no"`
	SubjectName   string          `json:"subject_name"`
	ServerResult  string          `json:"server_result"`
	ServerReason  string          `json:"server_reason"`
	Detail        json.RawMessage `json:"detail,omitempty"`
	Resolution    string          `json:"resolution"`
	Note          string          `json:"resolution_note"`
	DetectedAt    time.Time       `json:"detected_at"`
	ResolvedBy    int64           `json:"resolved_by_user_id"`
	ResolvedAt    *time.Time      `json:"resolved_at,omitempty"`
	Open          bool            `json:"is_open"`
}

func conflictViewOf(c Conflict) ConflictView {
	view := ConflictView{
		ID: c.ID, BatchID: c.BatchID, ClientBatchID: c.ClientBatch, DeviceID: c.DeviceID,
		DeviceLabel: c.DeviceLabel, GateName: c.GateName, Type: c.Type,
		DeviceUUID: c.DeviceUUID, MovementID: c.MovementID, PassID: c.PassID, PassNo: c.PassNo,
		SubjectName: c.SubjectName, ServerResult: c.ServerResult, ServerReason: c.ServerReason,
		Resolution: c.Resolution, Note: c.Note, DetectedAt: c.DetectedAt,
		ResolvedBy: c.ResolvedBy, ResolvedAt: c.ResolvedAt, Open: c.Open(),
	}
	// A detail that will not parse is left out rather than half-shown: the row's own words are
	// the record, and a corrupt blob is not a reason to hand the desk a string it cannot read.
	if c.Detail != nil && json.Valid([]byte(*c.Detail)) {
		view.Detail = json.RawMessage(*c.Detail)
	}
	return view
}

func conflictViewsOf(conflicts []Conflict) []ConflictView {
	views := make([]ConflictView, 0, len(conflicts))
	for _, conflict := range conflicts {
		views = append(views, conflictViewOf(conflict))
	}
	return views
}

// HealthView is one device on the fleet screen. is_silent is computed at read time, because
// whether a tablet has stopped talking is a question about now, not about the last row written.
type HealthView struct {
	DeviceID    int64      `json:"gate_device_id"`
	Label       string     `json:"label"`
	GateID      int64      `json:"gate_id"`
	GateName    string     `json:"gate_name"`
	SyncStatus  string     `json:"sync_status"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	LastSyncAt  *time.Time `json:"last_sync_at,omitempty"`
	CacheVer    int64      `json:"cache_version"`
	OpenBatches int        `json:"open_batches"`
	Unresolved  int        `json:"unresolved_conflicts"`
	Silent      bool       `json:"is_silent"`
}

func healthViewsOf(rows []Health) []HealthView {
	now := time.Now().UTC()
	views := make([]HealthView, 0, len(rows))
	for _, row := range rows {
		views = append(views, HealthView{
			DeviceID: row.DeviceID, Label: row.Label, GateID: row.GateID, GateName: row.GateName,
			SyncStatus: row.SyncStatus, LastSeenAt: row.LastSeenAt, LastSyncAt: row.LastSyncAt,
			CacheVer: row.CacheVer, OpenBatches: row.OpenBatches, Unresolved: row.Unresolved,
			Silent: row.Silent(now, silenceWindow),
		})
	}
	return views
}

// ResolveRequest is a person overruling what the server did about one row. The note is
// optional because the resolution word already says what happened; it is there for the case
// where the word is not enough for whoever reads the batch next.
type ResolveRequest struct {
	Resolution string `json:"resolution" validate:"required,max=32"`
	Note       string `json:"note" validate:"omitempty,max=512"`
}

// ParseBatches reads the batch filters: ?device_id=&gate_id=&status=&open=.
func ParseBatches(r *http.Request) (BatchFilter, httpx.Page, error) {
	query := r.URL.Query()
	deviceID, err := optionalID(query, "device_id")
	if err != nil {
		return BatchFilter{}, httpx.Page{}, err
	}
	gateID, err := optionalID(query, "gate_id")
	if err != nil {
		return BatchFilter{}, httpx.Page{}, err
	}
	open, err := optionalBool(query, "open")
	if err != nil {
		return BatchFilter{}, httpx.Page{}, err
	}
	filter := BatchFilter{
		DeviceID: deviceID, GateID: gateID, Status: query.Get("status"),
		OpenOnly: open != nil && *open,
	}
	if filter.Status != "" && !knownStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a batch status")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

// ParseConflicts reads the conflict filters: ?device_id=&batch_id=&type=&open=.
func ParseConflicts(r *http.Request) (ConflictFilter, httpx.Page, error) {
	query := r.URL.Query()
	deviceID, err := optionalID(query, "device_id")
	if err != nil {
		return ConflictFilter{}, httpx.Page{}, err
	}
	batchID, err := optionalID(query, "batch_id")
	if err != nil {
		return ConflictFilter{}, httpx.Page{}, err
	}
	open, err := optionalBool(query, "open")
	if err != nil {
		return ConflictFilter{}, httpx.Page{}, err
	}
	filter := ConflictFilter{
		DeviceID: deviceID, BatchID: batchID, Type: query.Get("type"),
		Unresolved: open != nil && *open,
	}
	if filter.Type != "" && !ConflictType(filter.Type).valid() {
		return filter, httpx.Page{}, errs.BadField("type",
			"must be duplicate, out_of_order, status_mismatch, clock_skew or unknown_pass")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

// ParseCacheVersion is the epoch the device says it holds. An absent claim is version zero,
// which asks for the whole register - the right answer for a tablet that has just been
// enrolled, flashed, or reset.
func ParseCacheVersion(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("cache_version")
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errs.BadField("cache_version", "expected a whole number of epochs")
	}
	return value, nil
}

func knownStatus(status string) bool {
	for _, candidate := range []Status{
		StatusPending, StatusProcessing, StatusCompleted, StatusPartial, StatusFailed,
		StatusSuperseded,
	} {
		if string(candidate) == status {
			return true
		}
	}
	return false
}

func optionalID(query url.Values, name string) (int64, error) {
	raw := query.Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errs.BadField(name, "expected a positive id")
	}
	return value, nil
}

// optionalBool refuses a value that is neither yes nor no: quietly ignoring ?open=maybe would
// hand back every batch and look like a filter that worked.
func optionalBool(query url.Values, name string) (*bool, error) {
	switch strings.ToLower(query.Get(name)) {
	case "":
		return nil, nil
	case "true", "1":
		yes := true
		return &yes, nil
	case "false", "0":
		no := false
		return &no, nil
	default:
		return nil, errs.BadField(name, "expected true or false")
	}
}
