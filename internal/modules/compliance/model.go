// The tools around the audit trail Core
// already writes. Retention policies and legal holds decide what a purge may touch, the purge
// ledger says what it did, and chain verification answers whether the trail still adds up.
package compliance

import "time"

// ModuleKey is the tenant's switch for this surface.
const ModuleKey = "audit_compliance"

// aggregateType names this module's rows on the outbox.
const aggregateType = "compliance"

// purgeBatch caps one run. A purge that walks a hundred thousand rows in one request is a
// purge that holds a lock nobody planned for; the operator runs it again, and each run says
// how much it covered.
const purgeBatch = 5000

// verifyBatch caps one verification walk for the same reason.
const verifyBatch = 50000

// Category is one class of personal data a retention policy may name. The list mirrors
// chk_retention_policies_category, narrowed to the categories this module can actually purge:
// the schema accepts more than the code can honour, and a policy for one of those would be a
// promise the purge cannot keep.
type Category string

const (
	CategoryVisitorIdentity Category = "visitor_identity"
	CategoryVisitorPhoto    Category = "visitor_photo"
	CategoryPassDocument    Category = "pass_subject_document"
	CategoryAttachment      Category = "attachment"
	CategoryNotifyAddress   Category = "notification_address"
)

func (c Category) Valid() bool {
	switch c {
	case CategoryVisitorIdentity, CategoryVisitorPhoto, CategoryPassDocument,
		CategoryAttachment, CategoryNotifyAddress:
		return true
	}
	return false
}

// entityType is the legal_holds.entity_type a hold on this category's rows is filed under.
func (c Category) entityType() string {
	switch c {
	case CategoryVisitorIdentity, CategoryVisitorPhoto:
		return "visitors"
	case CategoryPassDocument:
		return "passes"
	case CategoryAttachment:
		return "pass_attachments"
	case CategoryNotifyAddress:
		return "notifications"
	default:
		return ""
	}
}

// anonable reports whether anonymising the value means something for this category. A photo
// path and an attachment row carry no value of their own; the file is gone and the row says so,
// which is why only the two identifier categories may be anonymised.
func (c Category) anonable() bool {
	return c == CategoryVisitorIdentity || c == CategoryPassDocument
}

// deletable reports whether the row and the personal data are the same thing. Only an
// attachment qualifies: it exists to hold one document, and removing it orphans nothing.
func (c Category) deletable() bool { return c == CategoryAttachment }

// PurgeAction is what a purge does to a row it is allowed to touch.
//
//   - null      - the value is blanked and the row stays, stamped with when.
//   - anonymise - the value is replaced by a stable hash, so joins still work.
//   - delete    - the row itself is removed. Only an attachment is a record whose whole point
//     is the personal data; deleting a visit or a pass would take its trail with it.
//   - archive   - the value is blanked, the row stays, and a receipt is filed in
//     data_purge_archives, so the desk can prove the record was archived rather than merely
//     blanked. The receipt carries a hash, never the value: there is no file store here to
//     move anything to, and keeping the value would defeat the retention policy.
type PurgeAction string

const (
	ActionNull      PurgeAction = "null"
	ActionAnonymise PurgeAction = "anonymise"
	ActionDelete    PurgeAction = "delete"
	ActionArchive   PurgeAction = "archive"
)

func (a PurgeAction) Valid() bool {
	switch a {
	case ActionNull, ActionAnonymise, ActionDelete, ActionArchive:
		return true
	}
	return false
}

// Supports is the whole matrix of what this module can honestly do to a category. A policy that
// names an action the purge cannot take is a promise nobody can keep, so it is refused when it
// is saved rather than discovered when it runs.
func (c Category) Supports(action PurgeAction) bool {
	switch action {
	case ActionNull, ActionArchive:
		return c.Valid()
	case ActionAnonymise:
		return c.anonable()
	case ActionDelete:
		return c.deletable()
	}
	return false
}

// HoldStatus is the lifecycle of a legal hold.
const (
	HoldActive   = "active"
	HoldReleased = "released"
	HoldExpired  = "expired"
)

// Outcome values a verification records.
const (
	OutcomeVerified   = "verified"
	OutcomeMismatch   = "mismatch"
	OutcomeIncomplete = "incomplete"
)

// Run statuses.
const (
	PurgeRunning   = "running"
	PurgeSucceeded = "succeeded"
	PurgeFailed    = "failed"
)

// Policy is one tenant's retention rule for one category.
type Policy struct {
	ID               int64      `db:"id"`
	TenantID         int64      `db:"tenant_id"`
	DataCategory     string     `db:"data_category"`
	RetentionDays    int        `db:"retention_days"`
	PurgeAction      string     `db:"purge_action"`
	NotifyBeforeDays *int       `db:"notify_before_days"`
	LastRunAt        *time.Time `db:"last_run_at"`
	LastNotifiedAt   *time.Time `db:"last_notified_at"`
	IsActive         bool       `db:"is_active"`
	UpdatedBy        *int64     `db:"updated_by"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

// Hold is one legal hold. A held row is counted and skipped, never purged, and the case
// reference is what an incident review follows when it asks why a record is still there.
type Hold struct {
	ID            int64      `db:"id"`
	TenantID      int64      `db:"tenant_id"`
	EntityType    string     `db:"entity_type"`
	EntityID      int64      `db:"entity_id"`
	PassID        *int64     `db:"pass_id"`
	CaseRef       string     `db:"case_ref"`
	Reason        string     `db:"reason"`
	Status        string     `db:"status"`
	PlacedBy      *int64     `db:"placed_by_user_id"`
	PlacedAt      time.Time  `db:"placed_at"`
	ExpiresAt     *time.Time `db:"expires_at"`
	ReviewedAt    *time.Time `db:"reviewed_at"`
	ReleasedBy    *int64     `db:"released_by_user_id"`
	ReleasedAt    *time.Time `db:"released_at"`
	ReleaseReason *string    `db:"release_reason"`
	CreatedAt     time.Time  `db:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
}

// PurgeRun is one walk of one category. is_dry_run is recorded on every row because "how many
// would this have deleted" is the question an operator asks before the answer matters.
type PurgeRun struct {
	ID           int64      `db:"id"`
	TenantID     int64      `db:"tenant_id"`
	PolicyID     *int64     `db:"retention_policy_id"`
	DataCategory string     `db:"data_category"`
	PurgeAction  string     `db:"purge_action"`
	IsDryRun     bool       `db:"is_dry_run"`
	Status       string     `db:"status"`
	RowsScanned  int64      `db:"rows_scanned"`
	RowsPurged   int64      `db:"rows_purged"`
	RowsOnHold   int64      `db:"rows_skipped_on_hold"`
	CutoffAt     *time.Time `db:"cutoff_at"`
	ErrorMessage *string    `db:"error_message"`
	StartedAt    *time.Time `db:"started_at"`
	FinishedAt   *time.Time `db:"finished_at"`
	RequestedBy  *int64     `db:"requested_by_user_id"`
	CreatedAt    time.Time  `db:"created_at"`
}

// Verification is one walk of the audit chain and what it found.
type Verification struct {
	ID                int64     `db:"id"`
	TenantID          int64     `db:"tenant_id"`
	StreamKey         string    `db:"stream_key"`
	FromSeq           uint64    `db:"from_seq"`
	ToSeq             uint64    `db:"to_seq"`
	RowsChecked       int64     `db:"rows_checked"`
	Outcome           string    `db:"outcome"`
	FirstMismatchSeq  *uint64   `db:"first_mismatch_seq"`
	FirstMismatchHash *string   `db:"first_mismatch_hash"`
	TipHash           *string   `db:"tip_hash"`
	DurationMS        *int      `db:"duration_ms"`
	CheckedBy         *int64    `db:"checked_by_user_id"`
	CheckedAt         time.Time `db:"checked_at"`
	Note              *string   `db:"note"`
	CreatedAt         time.Time `db:"created_at"`
}

// Candidate is one row a purge may touch: what table it lives in, which row, and the value the
// policy would blank. Rows a hold protects are still candidates; they are counted and skipped.
type Candidate struct {
	EntityType string `db:"entity_type"`
	EntityID   int64  `db:"entity_id"`
	Value      string `db:"value"`
}

// Archive is the receipt an archive action files. It names the record and hashes the value that
// was erased; it never stores the value itself.
type Archive struct {
	ID           int64     `db:"id"`
	TenantID     int64     `db:"tenant_id"`
	PurgeRunID   *int64    `db:"purge_run_id"`
	DataCategory string    `db:"data_category"`
	EntityType   string    `db:"entity_type"`
	EntityID     int64     `db:"entity_id"`
	ValueHash    *string   `db:"value_hash"`
	ArchivedAt   time.Time `db:"archived_at"`
}
