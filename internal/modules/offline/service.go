package offline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/gates"
	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Service is the offline half of a gate: the cache a tablet is told to hold, the book kept of
// what it sent back, and the desk view of both. It decides nothing about a person at a
// barrier - Core answers that when the queue replays - and it never writes a movement row.
type Service struct {
	pool    *sqlx.DB
	repo    *Repository
	devices *gates.Repository
	passes  *passes.Repository
	// screen is the list half of a cache. It is a dependency rather than a query because the
	// list belongs to another module, and nothing here may require that module to be bought.
	screen Screenings
	// tokens is the scan module's issue seam. A cache with a pass number but no signed token
	// would make the tablet rely on a value the server never checked.
	tokens Tokens
	access *access.Service
	events events.Publisher
	chain  audit.Writer
}

func NewService(pool *sqlx.DB, repo *Repository, devices *gates.Repository,
	passRepo *passes.Repository, screen Screenings, tokens Tokens, accessSvc *access.Service,
	bus events.Publisher, chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, devices: devices, passes: passRepo, screen: screen,
		tokens: tokens, access: accessSvc, events: bus, chain: chain}
}

// enabled is the switch that keeps the module optional. With it off the desk surface is not
// mounted and the ledger keeps no book, so a replayed queue still lands - exactly as it did
// before this module existed - and a gate with no cache to pull simply needs the link.
func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// cacheCeiling is how many passes one device may be handed. It is a ceiling rather than a
// page, because a tablet cannot hold a slice of the register and honour the rest: a site whose
// live register is bigger than a device's memory is a site that needs a second gate, and the
// read says so instead of quietly stocking half a cache.
const cacheCeiling = 2000

// Bundle builds the cache for the device the request came in on, and only for that device's
// gate: a tablet bolted to the Delivery Gate has no business holding the Staff Gate's passes,
// and a cache that over-reaches is a leak that outlives the network it was pulled over.
//
// A pull that changes nothing writes nothing, so a tablet refreshing every quarter-hour costs
// one read of the register rather than an epoch every quarter-hour.
func (s *Service) Bundle(ctx context.Context, scope tenancy.Scope, claim int64) (Bundle, error) {
	if scope.DeviceID == 0 {
		return Bundle{}, errs.Forbidden("a cache is issued to a device, not to a session")
	}
	device, err := s.devices.DeviceByID(ctx, scope.TenantID, scope.DeviceID)
	if err != nil {
		return Bundle{}, err
	}
	if !device.Usable() {
		return Bundle{}, errs.Forbidden("device " + device.Label + " is not bound to an active gate")
	}
	gateID := *device.GateID
	gate, categories, err := s.gateAt(ctx, scope.TenantID, gateID)
	if err != nil {
		return Bundle{}, err
	}
	// offline_capable is the desk's way of saying this barrier must never decide on its own. A
	// cache issued to such a gate would be a second opinion nobody asked for, and the wrong one:
	// the pass revoked ten minutes after the pull keeps opening it until the next one.
	if !gate.OfflineCapable {
		return Bundle{}, errs.Forbidden("gate " + gate.Name + " is not offline-capable")
	}

	now := time.Now().UTC()
	to := now.Add(cacheHorizon)
	current, err := s.passes.Cacheable(ctx, passes.CacheQuery{
		TenantID: scope.TenantID, Categories: categories, From: now, To: to,
		Limit: cacheCeiling + 1,
	})
	if err != nil {
		return Bundle{}, err
	}
	if len(current) > cacheCeiling {
		return Bundle{}, errs.Conflict(fmt.Sprintf("%d passes are scannable at %s, more than one "+
			"device can hold; narrow the gate's categories or split the entrance",
			len(current), gate.Name))
	}

	// The device says what it holds; the server's stamp says what it was told to hold. When the
	// two disagree the tablet has been restored, re-imaged or has been sitting out of contact
	// long enough for its own book to stop meaning anything, so nothing it claims is believed:
	// the whole register goes out as a rebuild and the epoch still only ever moves forward.
	stale := claim != device.CacheVersion
	held := []Manifest(nil)
	if !stale {
		held, err = s.repo.Held(ctx, device.ID, claim)
		if err != nil {
			return Bundle{}, err
		}
	}
	listings, err := s.listings(ctx, scope.TenantID)
	if err != nil {
		return Bundle{}, err
	}

	out := Bundle{
		DeviceID: device.ID, GateID: gateID, GateName: gate.Name, Version: device.CacheVersion,
		HeldSince: now, RefreshAfter: refreshAfter, Full: stale, Screening: listings,
		AllowedCategories: categories, SameGateExit: gate.SameGateExitRequired,
	}
	send, stock, removed := assemble(current, held, now, to)
	if err := s.issueTokens(ctx, scope.TenantID, send, stock); err != nil {
		// A token issue failure is logged rather than fatal: the cache is still useful for the
		// passes whose tokens were minted, and a gate with a stale cache must not be blocked
		// from refreshing it because one token row could not be written.
		logging.From(ctx).ErrorContext(ctx, "issue offline scan tokens",
			"tenant_id", scope.TenantID, "device_id", device.ID, "error", err)
	}
	out.Removed = removed
	if !stale && len(send) == 0 && len(removed) == 0 {
		out.Fresh = true
		out.Checksum = checksum(out)
		return out, nil
	}

	out.Passes = send
	out.Version = device.CacheVersion + 1
	out.Checksum = checksum(out)
	if err := s.stock(ctx, scope, device, out, stock, now); err != nil {
		return Bundle{}, err
	}
	return out, nil
}

func (s *Service) gateAt(ctx context.Context, tenantID, gateID int64) (gates.Gate, []string, error) {
	gate, err := s.devices.ByID(ctx, tenantID, gateID)
	if err != nil {
		return gates.Gate{}, nil, err
	}
	categories, err := s.devices.Categories(ctx, gateID)
	if err != nil {
		return gates.Gate{}, nil, err
	}
	return gate, categories, nil
}

// listings is the screening half of a cache. A tenant that has not bought the list gets an
// empty one, which is exactly what its gates have always honoured.
func (s *Service) listings(ctx context.Context, tenantID int64) ([]CachedListing, error) {
	if s.screen == nil {
		return nil, nil
	}
	out, err := s.screen.Cached(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ListType != out[j].ListType {
			return out[i].ListType < out[j].ListType
		}
		return out[i].MatchValue < out[j].MatchValue
	})
	return out, nil
}

// assemble splits the register into what the device must be sent, what the ledger writes down
// as what it now holds, and what it must be told to drop.
//
// A pass the device already holds unchanged is not sent again, but it is still written at the
// new epoch with the push time it first went out at. The epoch has to describe a whole cache
// rather than a set of fragments, or "what does this tablet hold" could only be answered by
// replaying every bundle it ever pulled.
func assemble(current []passes.Cacheable, held []Manifest, now, to time.Time) (
	send []CachedPass, stock []Manifest, removed []int64) {
	send = make([]CachedPass, 0, len(current))
	byPass := make(map[int64]Manifest, len(held))
	for _, row := range held {
		byPass[row.PassID] = row
	}
	keep := make(map[int64]bool, len(current))
	stock = make([]Manifest, 0, len(current))

	for _, row := range current {
		keep[row.PassID] = true
		into := manifestOf(row, now, to)
		previous, seen := byPass[row.PassID]
		if seen && sameWindow(previous, into) {
			into.PushedAt = previous.PushedAt
			// The tablet still holds the token from the earlier bundle; re-sending it would
			// hand the same pass a second secret and leave both live.
			into.TokenHash = previous.TokenHash
		} else {
			send = append(send, handed(row, into))
		}
		stock = append(stock, into)
	}
	for _, row := range held {
		if !keep[row.PassID] {
			removed = append(removed, row.PassID)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i] < removed[j] })
	return send, stock, removed
}

func sameWindow(previous, into Manifest) bool {
	return previous.Reason == into.Reason && previous.ValidFrom.Equal(into.ValidFrom) &&
		previous.ValidUntil.Equal(into.ValidUntil)
}

// manifestOf is the server's record of one device holding one pass. The window is the pass's
// own - a tablet must respect the expiry printed on the pass, not the length of the pull that
// delivered it, and above all not the instant of that pull: a window whose start is written as
// now is a window that disagrees with itself on the next pull, which sends the whole register
// out again every time and never lets an epoch settle. Except where the pass has no end: the
// horizon stands in, so the device has a date to drop it by rather than a row that never
// expires.
func manifestOf(row passes.Cacheable, now, to time.Time) Manifest {
	return Manifest{
		PassID: row.PassID, Reason: string(reasonOf(row, to)),
		ValidFrom: row.ValidFrom.UTC(), ValidUntil: dropBy(row, to), PushedAt: now,
	}
}

// dropBy is the date the device stops honouring the pass: the one printed on it, or the horizon
// rounded up to the next refresh edge when there is not. The rounding is what makes an
// unchanged register cheap - two pulls in the same quarter-hour must hand the tablet the same
// date, because a stand-in that moves with every pull is a row that has to be re-sent with it.
// It rounds up rather than down because the table insists the window ends after it opens, and
// a pass that starts late inside this horizon would otherwise be given a drop-by date already
// behind its own start.
func dropBy(row passes.Cacheable, to time.Time) time.Time {
	if row.ValidUntil != nil {
		return row.ValidUntil.UTC()
	}
	return to.UTC().Truncate(refreshAfter).Add(refreshAfter)
}

// reasonOf is why this pass is in the cache, which is also the reason it will leave it. A pass
// that has already been scanned once is here because it is expected to come back, and that is
// worth recording separately: it is the row a stale cache hangs on longest.
func reasonOf(row passes.Cacheable, to time.Time) CacheReason {
	if row.EntryCount > 0 {
		return ReasonMultiEntry
	}
	if row.ValidUntil == nil || row.ValidUntil.After(to) {
		return ReasonRange
	}
	return ReasonToday
}

// issueTokens mints one short-lived token per pass being sent, and files its hash on the
// matching manifest. Passes already held unchanged are not sent and keep the token hash the
// ledger already has.
func (s *Service) issueTokens(ctx context.Context, tenantID int64, send []CachedPass,
	stock []Manifest) error {
	if s.tokens == nil || len(send) == 0 {
		return nil
	}
	index := make(map[int64]int, len(stock))
	for i := range stock {
		index[stock[i].PassID] = i
	}
	for i := range send {
		secret, hash, err := s.tokens.IssueForOffline(ctx, tenantID, send[i].PassID,
			send[i].ValidUntil)
		if err != nil {
			return err
		}
		send[i].OfflineToken = secret
		if at, ok := index[send[i].PassID]; ok {
			stock[at].TokenHash = hash
		}
	}
	return nil
}

func handed(row passes.Cacheable, into Manifest) CachedPass {
	return CachedPass{
		PassID: row.PassID, PassNo: row.PassNo, Category: row.CategoryKey,
		Module: row.ModuleKey, Status: row.Status, SubjectName: row.SubjectName,
		Reason: into.Reason, ValidFrom: into.ValidFrom, ValidUntil: into.ValidUntil,
		EntriesUsed: row.EntryCount, PushedAt: into.PushedAt,
	}
}

// stock writes the new epoch and stamps the device with it in one transaction: a device must
// never be told to hold a version the server has not finished writing down, or the cache it
// trusts is one the ledger cannot describe.
func (s *Service) stock(ctx context.Context, scope tenancy.Scope, device gates.Device,
	bundle Bundle, rows []Manifest, at time.Time) error {
	rows = labelled(scope.TenantID, device, bundle.Version, rows)
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo, devices := s.repo.In(c), s.devices.In(c)
		batch, err := repo.FileBatch(ctx, NewBatch{
			TenantID: scope.TenantID, DeviceID: device.ID, GateID: *device.GateID,
			ClientID: clientBundleID(device.ID, bundle.Version, at), Kind: KindFull,
			Direction: directionDownload, Status: StatusProcessing,
			CacheFrom: device.CacheVersion, RequestedBy: scope.UserID, At: at,
		})
		if err != nil {
			return err
		}
		if err := repo.Stock(ctx, device.ID, at, rows); err != nil {
			return err
		}
		if _, err := repo.CloseBatch(ctx, scope.TenantID, batch.ID, Tally{
			Status: StatusCompleted, Records: len(bundle.Passes), Accepted: len(bundle.Passes),
			Conflicts: len(bundle.Removed), CacheTo: bundle.Version, Checksum: bundle.Checksum,
		}, at); err != nil {
			return err
		}
		return devices.StampSync(ctx, scope.TenantID, device.ID, bundle.Version,
			gates.SyncSynced, at)
	})
}

// clientBundleID names a download the way a device names an upload. Nothing out there minted
// it, so it is built from the two facts that make it unique: the device, and the epoch it was
// handed at the moment it was handed.
func clientBundleID(deviceID, version int64, at time.Time) string {
	return fmt.Sprintf("bundle:%d:%d:%d", deviceID, version, at.Unix())
}

func labelled(tenantID int64, device gates.Device, version int64,
	rows []Manifest) []Manifest {
	out := make([]Manifest, 0, len(rows))
	for _, row := range rows {
		row.TenantID, row.DeviceID, row.Version = tenantID, device.ID, version
		row.GateID = *device.GateID
		out = append(out, row)
	}
	return out
}

// checksum is the bundle reduced to one value the device can be handed and the ledger can
// file. It covers the parts that decide what a gate would do while offline, and never the
// times, which move on every pull.
func checksum(b Bundle) string {
	canonical, err := json.Marshal(struct {
		Passes     []CachedPass    `json:"passes"`
		Removed    []int64         `json:"removed"`
		Screening  []CachedListing `json:"screening"`
		Categories []string        `json:"categories"`
		SameGate   bool            `json:"same_gate"`
	}{
		Passes: b.Passes, Removed: b.Removed, Screening: b.Screening,
		Categories: b.AllowedCategories, SameGate: b.SameGateExit,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// The desk surface.

// Devices is the fleet as the ledger sees it: what each tablet holds, when it last said
// anything, and how much it still owes.
func (s *Service) Devices(ctx context.Context, tenantID int64) ([]Health, error) {
	return s.repo.Health(ctx, tenantID)
}

func (s *Service) Batches(ctx context.Context, f BatchFilter) ([]Batch, error) {
	return s.repo.Batches(ctx, f)
}

func (s *Service) Batch(ctx context.Context, tenantID, id int64) (Batch, error) {
	return s.repo.BatchByID(ctx, tenantID, id)
}

func (s *Service) Conflicts(ctx context.Context, f ConflictFilter) ([]Conflict, error) {
	return s.repo.Conflicts(ctx, f)
}

func (s *Service) Conflict(ctx context.Context, tenantID, id int64) (Conflict, error) {
	return s.repo.ConflictByID(ctx, tenantID, id)
}

// Resolve overrules what the server did about a disagreement with what a person has decided.
// It cannot re-run the scan - the movement row and the pass status are Core's and were settled
// on the way in - which is why the words on offer are about the record rather than the gate.
func (s *Service) Resolve(ctx context.Context, scope tenancy.Scope, id int64,
	resolution Resolution, note string) (Conflict, error) {
	if !resolution.valid() {
		return Conflict{}, errs.BadField("resolution",
			"must be server_kept_status, kept_both_records, marked_late, dropped_duplicate, "+
				"rejected or manual_review")
	}
	at := time.Now().UTC()
	var conflict Conflict
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		found, err := repo.ConflictByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		changed, err := repo.Resolve(ctx, scope.TenantID, id, scope.UserID,
			string(resolution), note, at)
		if err != nil {
			return err
		}
		if !changed {
			return errs.Conflict("this conflict was already resolved as " + found.Resolution)
		}
		conflict, err = repo.ConflictByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "offline.conflict_resolved", SubjectType: "sync_conflict", SubjectID: id,
			SiteID: scope.SiteID, GateID: scope.GateID, Reason: note,
			Before: map[string]any{"resolution": found.Resolution},
			After:  map[string]any{"resolution": string(resolution)},
		})
	})
	if err != nil {
		return Conflict{}, err
	}
	return conflict, nil
}
