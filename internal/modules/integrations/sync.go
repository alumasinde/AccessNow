package integrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Sync directions and job states mirror chk_external_systems_direction and
// chk_sync_jobs_status.
const (
	SystemIn   = "in"
	SystemOut  = "out"
	SystemBoth = "both"
)

const (
	SyncQueued    = "queued"
	SyncRunning   = "running"
	SyncSucceeded = "succeeded"
	SyncPartial   = "partial"
	SyncFailed    = "failed"
	SyncCancelled = "cancelled"
)

const (
	HealthUnknown  = "unknown"
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthFailing  = "failing"
)

// staleJobClaim is how long a job may sit in 'running' before a pass assumes the process that
// claimed it died. A run that takes longer than this is a remote system that stopped answering.
const staleJobClaim = 10 * time.Minute

// syncMaxAttempts counts every run, the first included. A job that fails all of them is finally
// failed; until then each failure is requeued with the delay below.
const syncMaxAttempts = 4

// syncRetryDelays is the wait before attempt n+1: a blip gets one minute, a system that is down
// longer gets five, then twenty. Backoff stops when the attempts run out.
var syncRetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 20 * time.Minute}

// syncMaxBody caps what a remote system may send back. A sync is a delta, not a backup.
const syncMaxBody = 8 << 20

// syncCredentialName is the only kind of environment name a system may point at. Restricting
// the prefix stops a configuration mistake from naming a database password and posting it to a
// remote URL.
var syncCredentialName = regexp.MustCompile(`^ACCESSNOW_SYNC_[A-Z0-9_]{1,64}$`)

var syncFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

var syncDatabaseName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)

// syncKinds is chk_external_systems_kind. The module can hold a configuration for any of them;
// what it can actually exchange is the entity list below.
var syncKinds = map[string]bool{
	"hr": true, "erp": true, "accounting": true, "access_control": true, "video": true,
	"visitor_pre_registration": true, "payment": true, "other": true,
}

// syncEntities is chk_external_entity_links_type.
var syncEntities = map[string]bool{
	"staff": true, "department": true, "company": true, "visitor_company": true,
	"vehicle": true, "cost_center": true, "user": true, "site": true,
}

// outboundEntities is what this module can read from its own database to push. Inbound records
// are linked as they arrive, whatever their type; pushing a record means reading a register,
// and only these have a stable, non-sensitive shape to send.
var outboundEntities = map[string]bool{
	"staff": true, "department": true, "visitor_company": true,
}

// ExternalSystem is one tenant's HR, ERP or other system. The credential is a name, never a
// value: the secret is read from the environment at the moment it is used.
type ExternalSystem struct {
	ID                   int64      `db:"id"`
	TenantID             int64      `db:"tenant_id"`
	Kind                 string     `db:"kind"`
	Name                 string     `db:"name"`
	BaseURL              *string    `db:"base_url"`
	AuthType             string     `db:"auth_type"`
	CredentialsRef       *string    `db:"credentials_ref"`
	Username             *string    `db:"username"`
	APIVersion           *string    `db:"api_version"`
	SyncDirection        string     `db:"sync_direction"`
	SyncFrequencyMinutes *int       `db:"sync_frequency_minutes"`
	FieldMap             string     `db:"field_map"`
	Options              string     `db:"options"`
	HealthStatus         string     `db:"health_status"`
	LastSyncAt           *time.Time `db:"last_sync_at"`
	LastError            *string    `db:"last_error"`
	IsActive             bool       `db:"is_active"`
	CreatedBy            *int64     `db:"created_by"`
	CreatedAt            time.Time  `db:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"`
}

// systemOptions is the JSON column that holds the choices the schema has no column for.
type systemOptions struct {
	Entity          string `json:"entity"`
	Provider        string `json:"provider,omitempty"`
	Database        string `json:"database,omitempty"`
	Path            string `json:"path,omitempty"`
	ExternalIDField string `json:"external_id_field,omitempty"`
	CursorField     string `json:"cursor_field,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
}

func (s ExternalSystem) SyncOptions() systemOptions {
	var out systemOptions
	if strings.TrimSpace(s.Options) != "" {
		_ = json.Unmarshal([]byte(s.Options), &out)
	}
	return out
}

func (o systemOptions) externalField() string {
	if strings.TrimSpace(o.ExternalIDField) == "" {
		return "id"
	}
	return o.ExternalIDField
}

func (o systemOptions) provider() string {
	if strings.TrimSpace(o.Provider) == "" {
		return "generic"
	}
	return o.Provider
}

func (o systemOptions) timeout() time.Duration {
	if o.TimeoutMS <= 0 {
		return 10 * time.Second
	}
	return time.Duration(o.TimeoutMS) * time.Millisecond
}

// EntityLink ties a remote record to a local one. internal_id stays NULL until a local module
// claims the record: linking is not the same as writing.
type EntityLink struct {
	ID           int64      `db:"id"`
	TenantID     int64      `db:"tenant_id"`
	SystemID     int64      `db:"external_system_id"`
	EntityType   string     `db:"entity_type"`
	ExternalID   string     `db:"external_id"`
	InternalID   *int64     `db:"internal_id"`
	ContentHash  *string    `db:"content_hash"`
	LastSyncedAt *time.Time `db:"last_synced_at"`
	SystemName   string     `db:"system_name"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

type SyncJob struct {
	ID            int64      `db:"id"`
	TenantID      int64      `db:"tenant_id"`
	SystemID      int64      `db:"external_system_id"`
	EntityType    string     `db:"entity_type"`
	Direction     string     `db:"direction"`
	TriggerSource string     `db:"trigger_source"`
	Status        string     `db:"status"`
	Attempts      int        `db:"attempts"`
	NextAttemptAt *time.Time `db:"next_attempt_at"`
	RecordsSeen   int        `db:"records_seen"`
	RecordsMade   int        `db:"records_created"`
	RecordsMoved  int        `db:"records_updated"`
	RecordsFailed int        `db:"records_failed"`
	CursorValue   *string    `db:"cursor_value"`
	ErrorSummary  *string    `db:"error_summary"`
	StartedAt     *time.Time `db:"started_at"`
	FinishedAt    *time.Time `db:"finished_at"`
	CreatedAt     time.Time  `db:"created_at"`
	SystemName    string     `db:"system_name"`
}

// ExternalSystemRequest creates or replaces a system. CredentialsRef names an environment value
// with the ACCESSNOW_SYNC_ prefix; the value itself never reaches the database or the log.
type ExternalSystemRequest struct {
	Kind                 string            `json:"kind" validate:"required,oneof=hr erp accounting access_control video visitor_pre_registration payment other"`
	Name                 string            `json:"name" validate:"required,max=128"`
	BaseURL              string            `json:"base_url" validate:"required,max=1024"`
	AuthType             string            `json:"auth_type" validate:"omitempty,oneof=none basic bearer api_key"`
	CredentialsRef       string            `json:"credentials_ref" validate:"omitempty,max=80"`
	Username             string            `json:"username" validate:"omitempty,max=128"`
	APIVersion           string            `json:"api_version" validate:"omitempty,max=32"`
	SyncDirection        string            `json:"sync_direction" validate:"omitempty,oneof=in out both"`
	SyncFrequencyMinutes *int              `json:"sync_frequency_minutes" validate:"omitempty,min=1,max=1440"`
	EntityType           string            `json:"entity_type" validate:"omitempty,max=32"`
	Provider             string            `json:"provider" validate:"omitempty,oneof=generic odoo"`
	Database             string            `json:"database" validate:"omitempty,max=64"`
	Path                 string            `json:"path" validate:"omitempty,max=255"`
	ExternalIDField      string            `json:"external_id_field" validate:"omitempty,max=64"`
	CursorField          string            `json:"cursor_field" validate:"omitempty,max=64"`
	TimeoutMS            *int              `json:"timeout_ms" validate:"omitempty,min=500,max=30000"`
	FieldMap             map[string]string `json:"field_map"`
	IsActive             *bool             `json:"is_active"`
}

type SyncRequest struct {
	Direction string `json:"direction" validate:"omitempty,oneof=in out"`
}

type ExternalSystemView struct {
	ID                   int64             `json:"id"`
	Kind                 string            `json:"kind"`
	Name                 string            `json:"name"`
	BaseURL              *string           `json:"base_url,omitempty"`
	AuthType             string            `json:"auth_type"`
	CredentialsRef       *string           `json:"credentials_ref,omitempty"`
	Username             *string           `json:"username,omitempty"`
	APIVersion           *string           `json:"api_version,omitempty"`
	SyncDirection        string            `json:"sync_direction"`
	SyncFrequencyMinutes *int              `json:"sync_frequency_minutes,omitempty"`
	EntityType           string            `json:"entity_type"`
	Provider             string            `json:"provider"`
	Database             string            `json:"database,omitempty"`
	Path                 string            `json:"path"`
	ExternalIDField      string            `json:"external_id_field"`
	CursorField          string            `json:"cursor_field,omitempty"`
	TimeoutMS            int               `json:"timeout_ms"`
	FieldMap             map[string]string `json:"field_map"`
	HealthStatus         string            `json:"health_status"`
	LastSyncAt           *time.Time        `json:"last_sync_at,omitempty"`
	LastError            *string           `json:"last_error,omitempty"`
	IsActive             bool              `json:"is_active"`
	CreatedAt            time.Time         `json:"created_at"`
}

func systemView(system ExternalSystem) ExternalSystemView {
	opts := system.SyncOptions()
	fields := systemFieldMap(system)
	return ExternalSystemView{
		ID: system.ID, Kind: system.Kind, Name: system.Name, BaseURL: system.BaseURL,
		AuthType: system.AuthType, CredentialsRef: system.CredentialsRef,
		Username: system.Username, APIVersion: system.APIVersion,
		SyncDirection: system.SyncDirection, SyncFrequencyMinutes: system.SyncFrequencyMinutes,
		EntityType: opts.Entity, Provider: opts.provider(), Database: opts.Database,
		Path: opts.Path, ExternalIDField: opts.externalField(),
		CursorField: opts.CursorField, TimeoutMS: int(opts.timeout().Milliseconds()),
		FieldMap: fields, HealthStatus: system.HealthStatus, LastSyncAt: system.LastSyncAt,
		LastError: system.LastError, IsActive: system.IsActive, CreatedAt: system.CreatedAt,
	}
}

type SyncJobView struct {
	ID            int64      `json:"id"`
	SystemID      int64      `json:"external_system_id"`
	SystemName    string     `json:"system_name"`
	EntityType    string     `json:"entity_type"`
	Direction     string     `json:"direction"`
	TriggerSource string     `json:"trigger_source"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	RecordsSeen   int        `json:"records_seen"`
	RecordsMade   int        `json:"records_created"`
	RecordsMoved  int        `json:"records_updated"`
	RecordsFailed int        `json:"records_failed"`
	CursorValue   *string    `json:"cursor_value,omitempty"`
	ErrorSummary  *string    `json:"error_summary,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

func jobView(job SyncJob) SyncJobView {
	return SyncJobView{ID: job.ID, SystemID: job.SystemID, SystemName: job.SystemName,
		EntityType: job.EntityType, Direction: job.Direction, TriggerSource: job.TriggerSource,
		Status: job.Status, Attempts: job.Attempts, NextAttemptAt: job.NextAttemptAt,
		RecordsSeen: job.RecordsSeen, RecordsMade: job.RecordsMade,
		RecordsMoved: job.RecordsMoved, RecordsFailed: job.RecordsFailed,
		CursorValue: job.CursorValue, ErrorSummary: job.ErrorSummary, StartedAt: job.StartedAt,
		FinishedAt: job.FinishedAt, CreatedAt: job.CreatedAt}
}

type EntityLinkView struct {
	ID           int64      `json:"id"`
	SystemID     int64      `json:"external_system_id"`
	SystemName   string     `json:"system_name"`
	EntityType   string     `json:"entity_type"`
	ExternalID   string     `json:"external_id"`
	InternalID   *int64     `json:"internal_id,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func linkView(link EntityLink) EntityLinkView {
	return EntityLinkView{ID: link.ID, SystemID: link.SystemID, SystemName: link.SystemName,
		EntityType: link.EntityType, ExternalID: link.ExternalID, InternalID: link.InternalID,
		LastSyncedAt: link.LastSyncedAt, UpdatedAt: link.UpdatedAt}
}

// The desk surface.

func (s *Service) Systems(ctx context.Context, tenantID int64) ([]ExternalSystem, error) {
	return s.repo.Systems(ctx, tenantID)
}

func (s *Service) ExternalSystem(ctx context.Context, tenantID, id int64) (ExternalSystem, error) {
	return s.repo.ExternalSystem(ctx, tenantID, id)
}

func (s *Service) CreateSystem(ctx context.Context, scope tenancy.Scope,
	req ExternalSystemRequest) (ExternalSystem, error) {
	system, err := buildSystem(req, ExternalSystem{TenantID: scope.TenantID})
	if err != nil {
		return ExternalSystem{}, err
	}
	system.CreatedBy = optionalID(scope.UserID)
	now := s.now()

	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).InsertSystem(ctx, system, now)
		if err != nil {
			return err
		}
		id = filed
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "integration.system_created", SubjectType: "external_system",
			SubjectID: filed, SiteID: scope.SiteID,
			After: map[string]any{"kind": system.Kind, "name": system.Name,
				"direction": system.SyncDirection, "auth_type": system.AuthType},
		})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return ExternalSystem{}, errs.Conflict("a system of that kind and name already exists")
		}
		return ExternalSystem{}, err
	}
	return s.repo.ExternalSystem(ctx, scope.TenantID, id)
}

func (s *Service) UpdateSystem(ctx context.Context, scope tenancy.Scope, id int64,
	req ExternalSystemRequest) (ExternalSystem, error) {
	existing, err := s.repo.ExternalSystem(ctx, scope.TenantID, id)
	if err != nil {
		return ExternalSystem{}, err
	}
	system, err := buildSystem(req, existing)
	if err != nil {
		return ExternalSystem{}, err
	}
	system.ID = id
	now := s.now()

	var changed bool
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).UpdateSystem(ctx, system, now)
		if err != nil {
			return err
		}
		changed = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "integration.system_updated", SubjectType: "external_system",
			SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"name": system.Name, "direction": system.SyncDirection,
				"is_active": system.IsActive, "auth_type": system.AuthType},
		})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return ExternalSystem{}, errs.Conflict("a system of that kind and name already exists")
		}
		return ExternalSystem{}, err
	}
	if !changed {
		return ExternalSystem{}, errs.NotFound("external system")
	}
	return s.repo.ExternalSystem(ctx, scope.TenantID, id)
}

// SuspendSystem switches a system off without deleting the links and jobs behind it: those are
// the record of what was exchanged, and a cascade would erase it.
func (s *Service) SuspendSystem(ctx context.Context, scope tenancy.Scope, id int64) error {
	var moved bool
	now := s.now()
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		changed, err := s.repo.In(c).SetSystemActive(ctx, scope.TenantID, id, false, now)
		if err != nil {
			return err
		}
		moved = changed
		if !changed {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "integration.system_suspended", SubjectType: "external_system",
			SubjectID: id, SiteID: scope.SiteID, After: map[string]any{"is_active": false},
		})
	})
	if err != nil {
		return err
	}
	if !moved {
		return errs.NotFound("external system")
	}
	return nil
}

// StartSync queues one exchange now. It does not make the call: a remote system that takes forty
// seconds should not hold an HTTP request open, and a manual run deserves the same retry and
// audit path as a scheduled one.
func (s *Service) StartSync(ctx context.Context, scope tenancy.Scope, systemID int64,
	req SyncRequest) (SyncJob, error) {
	enabled, err := s.enabled(ctx, scope.TenantID)
	if err != nil {
		return SyncJob{}, err
	}
	if !enabled {
		return SyncJob{}, errs.ModuleDisabled(ModuleKey)
	}
	system, err := s.repo.ExternalSystem(ctx, scope.TenantID, systemID)
	if err != nil {
		return SyncJob{}, err
	}
	if !system.IsActive {
		return SyncJob{}, errs.Conflict("external system is switched off")
	}
	opts := system.SyncOptions()
	entity := opts.Entity
	if entity == "" {
		entity = "staff"
	}
	direction := strings.TrimSpace(req.Direction)
	if direction == "" {
		direction = system.SyncDirection
		if direction == SystemBoth {
			direction = SystemIn
		}
	}
	if direction != SystemIn && direction != SystemOut {
		return SyncJob{}, errs.BadField("direction", "must be in or out")
	}
	if system.SyncDirection != SystemBoth && direction != system.SyncDirection {
		return SyncJob{}, errs.BadField("direction",
			"system is configured for "+system.SyncDirection)
	}
	if direction == SystemOut && !outboundEntities[entity] {
		return SyncJob{}, errs.BadField("direction", "outbound sync is not available for "+entity)
	}

	now := s.now()
	job := SyncJob{TenantID: scope.TenantID, SystemID: system.ID, EntityType: entity,
		Direction: direction, TriggerSource: "manual", Status: SyncQueued}
	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).QueueJob(ctx, job, now)
		if err != nil {
			return err
		}
		id = filed
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "integration.sync_started", SubjectType: "sync_job", SubjectID: filed,
			SiteID: scope.SiteID, After: map[string]any{"system_id": system.ID,
				"entity_type": entity, "direction": direction},
		})
	})
	if err != nil {
		return SyncJob{}, err
	}
	return s.repo.Job(ctx, scope.TenantID, id)
}

func (s *Service) SyncJobs(ctx context.Context, tenantID, systemID int64) ([]SyncJob, error) {
	return s.repo.SyncJobs(ctx, tenantID, systemID, 100)
}

func (s *Service) Links(ctx context.Context, tenantID, systemID int64,
	entity string) ([]EntityLink, error) {
	return s.repo.Links(ctx, tenantID, systemID, entity, 200)
}

func (s *Service) DeleteLink(ctx context.Context, scope tenancy.Scope, id int64) error {
	var removed bool
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		gone, err := s.repo.In(c).DeleteLink(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		removed = gone
		if !gone {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "integration.link_removed", SubjectType: "external_entity_link",
			SubjectID: id, SiteID: scope.SiteID, After: map[string]any{"deleted": true},
		})
	})
	if err != nil {
		return err
	}
	if !removed {
		return errs.NotFound("entity link")
	}
	return nil
}

// buildSystem validates a system request into a row. Existing carries the fields the request
// does not repeat, so an update does not lose who filed it.
func buildSystem(req ExternalSystemRequest, existing ExternalSystem) (ExternalSystem, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !syncKinds[kind] {
		return ExternalSystem{}, errs.BadField("kind", "is not a system kind this module knows")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return ExternalSystem{}, errs.BadField("name", "is required")
	}
	direction := strings.TrimSpace(req.SyncDirection)
	if direction == "" {
		direction = SystemIn
	}
	if direction != SystemIn && direction != SystemOut && direction != SystemBoth {
		return ExternalSystem{}, errs.BadField("sync_direction", "must be in, out or both")
	}
	auth := strings.TrimSpace(req.AuthType)
	if auth == "" {
		auth = "none"
	}
	if auth != "none" && auth != "basic" && auth != "bearer" && auth != "api_key" {
		return ExternalSystem{}, errs.BadField("auth_type",
			"must be none, basic, bearer or api_key; client_credentials and oauth2 need a token exchange this module does not implement")
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = "generic"
	}
	if provider != "generic" && provider != "odoo" {
		return ExternalSystem{}, errs.BadField("provider", "must be generic or odoo")
	}
	database := strings.TrimSpace(req.Database)
	if provider == "odoo" {
		if auth != "basic" {
			return ExternalSystem{}, errs.BadField("auth_type",
				"odoo sessions use basic credentials")
		}
		if database == "" || !syncDatabaseName.MatchString(database) {
			return ExternalSystem{}, errs.BadField("database", "must name the Odoo database")
		}
	}
	base := strings.TrimSpace(req.BaseURL)
	if err := validSyncBaseURL(base); err != nil {
		return ExternalSystem{}, err
	}
	entity := strings.TrimSpace(req.EntityType)
	if entity == "" {
		entity = "staff"
	}
	if !syncEntities[entity] {
		return ExternalSystem{}, errs.BadField("entity_type", "is not an entity a sync can link")
	}
	if provider == "odoo" && entity != "staff" && entity != "department" {
		return ExternalSystem{}, errs.BadField("entity_type",
			"odoo sync supports staff and department")
	}
	if direction != SystemIn && !outboundEntities[entity] {
		return ExternalSystem{}, errs.BadField("entity_type",
			"outbound sync supports staff, department and visitor_company")
	}
	ref := strings.TrimSpace(req.CredentialsRef)
	if auth != "none" && ref == "" {
		return ExternalSystem{}, errs.BadField("credentials_ref",
			"is required for "+auth+" authentication")
	}
	if ref != "" && !syncCredentialName.MatchString(ref) {
		return ExternalSystem{}, errs.BadField("credentials_ref",
			"must name an environment variable starting with ACCESSNOW_SYNC_")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		if provider == "odoo" {
			path = "/web/dataset/call_kw"
		} else {
			path = "/"
		}
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || len(path) > 255 {
		return ExternalSystem{}, errs.BadField("path", "must be an absolute path without ..")
	}
	externalField := strings.TrimSpace(req.ExternalIDField)
	if externalField == "" {
		externalField = "id"
	}
	if !syncFieldName.MatchString(externalField) {
		return ExternalSystem{}, errs.BadField("external_id_field", "is not a field name")
	}
	cursorField := strings.TrimSpace(req.CursorField)
	if cursorField != "" && !syncFieldName.MatchString(cursorField) {
		return ExternalSystem{}, errs.BadField("cursor_field", "is not a field name")
	}
	timeout := 10000
	if req.TimeoutMS != nil {
		timeout = *req.TimeoutMS
	}
	if timeout < 500 || timeout > 30000 {
		return ExternalSystem{}, errs.BadField("timeout_ms", "must be between 500 and 30000")
	}
	if req.SyncFrequencyMinutes != nil &&
		(*req.SyncFrequencyMinutes < 1 || *req.SyncFrequencyMinutes > 1440) {
		return ExternalSystem{}, errs.BadField("sync_frequency_minutes",
			"must be between 1 and 1440")
	}
	if len(req.FieldMap) > 100 {
		return ExternalSystem{}, errs.BadField("field_map", "may map at most 100 fields")
	}
	for remote, local := range req.FieldMap {
		if !syncFieldName.MatchString(remote) || !syncFieldName.MatchString(local) {
			return ExternalSystem{}, errs.BadField("field_map",
				remote+": both sides must be field names")
		}
	}
	rawOptions, err := json.Marshal(systemOptions{Entity: entity, Provider: provider,
		Database: database, Path: path, ExternalIDField: externalField,
		CursorField: cursorField, TimeoutMS: timeout})
	if err != nil {
		return ExternalSystem{}, errs.Internal(fmt.Errorf("encode system options: %w", err))
	}
	rawFields, err := json.Marshal(req.FieldMap)
	if err != nil {
		return ExternalSystem{}, errs.Internal(fmt.Errorf("encode field map: %w", err))
	}
	active := true
	if existing.ID != 0 {
		active = existing.IsActive
	}
	if req.IsActive != nil {
		active = *req.IsActive
	}

	return ExternalSystem{
		TenantID: existing.TenantID, ID: existing.ID, Kind: kind, Name: name,
		BaseURL: &base, AuthType: auth, CredentialsRef: optionalText(ref),
		Username:      optionalText(strings.TrimSpace(req.Username)),
		APIVersion:    optionalText(strings.TrimSpace(req.APIVersion)),
		SyncDirection: direction, SyncFrequencyMinutes: req.SyncFrequencyMinutes,
		FieldMap: string(rawFields), Options: string(rawOptions), IsActive: active,
		CreatedBy: existing.CreatedBy,
	}, nil
}

// validSyncBaseURL applies the same rule as a webhook receiver: https, no credentials in the
// URL, and no private address. The dialer applies it again at connect time.
func validSyncBaseURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errs.BadField("base_url", "must be an https URL")
	}
	if parsed.User != nil {
		return errs.BadField("base_url", "must not carry credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errs.BadField("base_url", "must not carry a query or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errs.BadField("base_url", "must not point at this host")
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return errs.BadField("base_url", "must not point at a private address")
	}
	return nil
}

// The worker's local half. They are interfaces so a test can drive pull and push against a fake
// receiver without a database; *Repository is the only production implementation.
type linkUpserter interface {
	UpsertLink(ctx context.Context, tenantID, systemID int64, entity, external string,
		internal *int64, digest string, at time.Time) (bool, bool, error)
}

type localRecordReader interface {
	LocalRecords(ctx context.Context, tenantID int64, entity string,
		limit int) ([]json.RawMessage, error)
}

// The worker.

// RunSyncDue is one pass: reclaim abandoned jobs, file what the schedule says is due, then run a
// handful. A failure on one job is recorded on that job; it never stops the pass.
func (s *Service) RunSyncDue(ctx context.Context) (int, error) {
	now := s.now()
	if _, err := s.repo.ReclaimStaleJobs(ctx, now.Add(-staleJobClaim), 10); err != nil {
		return 0, err
	}
	systems, err := s.repo.ScheduledSystems(ctx)
	if err != nil {
		return 0, err
	}
	for _, system := range systems {
		if !systemDue(system, now) {
			continue
		}
		opts := system.SyncOptions()
		entity := opts.Entity
		if entity == "" {
			entity = "staff"
		}
		directions := []string{system.SyncDirection}
		if system.SyncDirection == SystemBoth {
			directions = []string{SystemIn, SystemOut}
		}
		for _, direction := range directions {
			if direction == SystemOut && !outboundEntities[entity] {
				continue
			}
			open, err := s.repo.HasOpenJob(ctx, system.ID, entity, direction)
			if err != nil {
				return 0, err
			}
			if open {
				continue
			}
			if _, err := s.repo.QueueJob(ctx, SyncJob{
				TenantID: system.TenantID, SystemID: system.ID, EntityType: entity,
				Direction: direction, TriggerSource: "schedule", Status: SyncQueued,
			}, now); err != nil {
				return 0, err
			}
		}
	}

	jobs, err := s.repo.DueJobs(ctx, now, 5)
	if err != nil {
		return 0, err
	}
	ran := 0
	for _, job := range jobs {
		claimed, err := s.repo.ClaimJob(ctx, job.ID, now)
		if err != nil {
			return ran, err
		}
		if !claimed {
			continue
		}
		if err := s.execute(ctx, job); err != nil {
			logging.From(ctx).ErrorContext(ctx, "sync job failed to record",
				"job_id", job.ID, "error", err)
		}
		ran++
	}
	return ran, nil
}

// systemDue compares in Go rather than in SQL: the interval is a column, and a stored
// last_sync_at is UTC, so the comparison is one subtraction either way.
func systemDue(system ExternalSystem, now time.Time) bool {
	if system.SyncFrequencyMinutes == nil || *system.SyncFrequencyMinutes <= 0 {
		return false
	}
	if system.LastSyncAt == nil {
		return true
	}
	return !now.Before(system.LastSyncAt.Add(
		time.Duration(*system.SyncFrequencyMinutes) * time.Minute))
}

func (s *Service) execute(ctx context.Context, job SyncJob) error {
	system, err := s.repo.ExternalSystem(ctx, job.TenantID, job.SystemID)
	if err != nil {
		return s.finish(ctx, job, SyncFailed, 0, 0, 0, 0, "",
			"external system no longer exists")
	}
	if !system.IsActive {
		return s.finish(ctx, job, SyncCancelled, 0, 0, 0, 0, "",
			"external system is switched off")
	}
	enabled, err := s.enabled(ctx, job.TenantID)
	if err != nil {
		return s.finish(ctx, job, SyncFailed, 0, 0, 0, 0, "", "module check failed")
	}
	if !enabled {
		return s.finish(ctx, job, SyncCancelled, 0, 0, 0, 0, "",
			"integrations are switched off for this tenant")
	}
	opts := system.SyncOptions()
	if opts.Entity == "" {
		opts.Entity = job.EntityType
	}
	if opts.Entity == "" {
		opts.Entity = "staff"
	}

	var seen, created, updated, failed int
	var cursor string
	var runErr error
	switch job.Direction {
	case SystemIn:
		seen, created, updated, failed, cursor, runErr = s.pull(ctx, system, opts, job, s.repo)
	case SystemOut:
		seen, updated, runErr = s.push(ctx, system, opts, s.repo)
	default:
		runErr = fmt.Errorf("unknown sync direction %q", job.Direction)
	}

	status := SyncSucceeded
	summary := ""
	if runErr != nil {
		summary = truncate(runErr.Error(), 900)
		attempt := job.Attempts + 1
		if delay, retry := syncRetryDelay(attempt); retry {
			logging.From(ctx).WarnContext(ctx, "sync job will retry", "job_id", job.ID,
				"system_id", system.ID, "attempt", attempt, "in", delay, "error", summary)
			return s.scheduleRetry(ctx, job, attempt, delay, summary)
		}
		status = SyncFailed
		if seen > 0 || created > 0 || updated > 0 {
			status = SyncPartial
		}
		logging.From(ctx).WarnContext(ctx, "sync job failed", "job_id", job.ID,
			"system_id", system.ID, "attempt", attempt, "error", summary)
	}
	if err := s.finish(ctx, job, status, seen, created, updated, failed, cursor,
		summary); err != nil {
		return err
	}
	health := HealthHealthy
	if status == SyncFailed {
		health = HealthFailing
	} else if status == SyncPartial || failed > 0 {
		health = HealthDegraded
	}
	return s.repo.MarkSystemResult(ctx, system.ID, health, summary, s.now())
}

// syncRetryDelay answers the wait before the next run, and whether there is one. The attempt
// number is the run that just failed, counting from one.
func syncRetryDelay(attempt int) (time.Duration, bool) {
	if attempt < 1 || attempt >= syncMaxAttempts {
		return 0, false
	}
	index := attempt - 1
	if index >= len(syncRetryDelays) {
		index = len(syncRetryDelays) - 1
	}
	return syncRetryDelays[index], true
}

// scheduleRetry requeues the run and files why, in one transaction. The system's health is left
// to the final failure: an attempt that is going to be retried is not yet a verdict.
func (s *Service) scheduleRetry(ctx context.Context, job SyncJob, attempt int,
	delay time.Duration, summary string) error {
	now := s.now()
	next := now.Add(delay)
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).RetryJob(ctx, job.ID, summary, next, now)
		if err != nil {
			return err
		}
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: job.TenantID, Action: "integration.sync_retry_scheduled",
			SubjectType: "sync_job", SubjectID: job.ID,
			After: map[string]any{"attempt": attempt, "next_attempt_at": next,
				"reason": summary},
		})
	})
}

// finish records the job and its audit row in one transaction: a job that says it moved records
// without an audit entry is a job nobody can explain later.
func (s *Service) finish(ctx context.Context, job SyncJob, status string, seen, created,
	updated, failed int, cursor, summary string) error {
	now := s.now()
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		if err := s.repo.In(c).FinishJob(ctx, job.ID, status, seen, created, updated, failed,
			optionalText(cursor), optionalText(summary), now); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: job.TenantID, Action: "integration.sync_finished",
			SubjectType: "sync_job", SubjectID: job.ID,
			After: map[string]any{"status": status, "direction": job.Direction,
				"entity_type": job.EntityType, "records_seen": seen,
				"records_created": created, "records_updated": updated,
				"records_failed": failed},
		})
	})
}

// pull reads the remote register and links what it finds. It never writes another module's
// table: a link records that the remote record exists and which local row it means, and the
// owning module decides what to do with it.
func (s *Service) pull(ctx context.Context, system ExternalSystem, opts systemOptions,
	job SyncJob, links linkUpserter) (seen, created, updated, failed int, cursor string, err error) {
	fields := systemFieldMap(system)
	if opts.provider() == "odoo" {
		return s.pullOdoo(ctx, system, opts, links, fields)
	}
	endpoint, err := syncEndpoint(system, opts)
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	if job.CursorValue != nil && *job.CursorValue != "" && opts.CursorField != "" {
		query := endpoint.Query()
		query.Set("cursor", *job.CursorValue)
		endpoint.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("build sync request: %w", err)
	}
	headers, err := authorize(system)
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	request.Header = headers
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "AccessNow-Sync/1")

	response, err := syncHTTPClient(opts.timeout()).Do(request)
	if err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("call %s: %w", system.Name, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, syncMaxBody))
	if err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("read %s: %w", system.Name, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return 0, 0, 0, 0, "", fmt.Errorf("receiver answered %d: %s",
			response.StatusCode, truncate(string(payload), 300))
	}
	records, next, err := decodeRecords(payload)
	if err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("decode %s: %w", system.Name, err)
	}

	now := s.now()
	for _, record := range records {
		external := recordString(record, opts.externalField())
		if external == "" {
			failed++
			continue
		}
		// internal_id is left to the module that owns the record: a sync links what the remote
		// system has, and claiming a local row is a decision this module does not make.
		made, changed, err := links.UpsertLink(ctx, system.TenantID, system.ID, opts.Entity,
			external, nil, mappedDigest(record, fields), now)
		if err != nil {
			return seen, created, updated, failed, cursor, err
		}
		switch {
		case made:
			created++
		case changed:
			updated++
		}
		seen++
		if opts.CursorField != "" {
			if value := recordString(record, opts.CursorField); value != "" {
				cursor = value
			}
		}
	}
	if cursor == "" {
		cursor = next
	}
	return seen, created, updated, failed, cursor, nil
}

// pullOdoo reads an Odoo register through its session RPC. Odoo's JSON-2 surface needs an
// API key that the demo will not mint from a password session, while the same session can call
// search_read directly. The field map is applied to the record digest, so a change to a mapped
// column is a change and a change to an unmapped one is not.
func (s *Service) pullOdoo(ctx context.Context, system ExternalSystem, opts systemOptions,
	links linkUpserter, mapped map[string]string) (
	seen, created, updated, failed int, cursor string, err error) {
	if opts.Database == "" {
		return 0, 0, 0, 0, "", errs.BadField("database", "is required for an Odoo system")
	}
	if opts.Entity != "staff" && opts.Entity != "department" {
		return 0, 0, 0, 0, "", errs.BadField("entity_type",
			"Odoo sync supports staff and department")
	}
	if system.BaseURL == nil || strings.TrimSpace(*system.BaseURL) == "" {
		return 0, 0, 0, 0, "", errs.BadField("base_url", "is not set on this system")
	}
	secret, err := syncCredential(system)
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	client := syncHTTPClient(opts.timeout())
	jar, err := cookiejar.New(nil)
	if err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("create Odoo session jar: %w", err)
	}
	client.Jar = jar
	base := strings.TrimRight(*system.BaseURL, "/")
	login, err := url.Parse(base + "/web/session/authenticate")
	if err != nil {
		return 0, 0, 0, 0, "", errs.BadField("base_url", "is not a URL")
	}
	var session struct {
		UID int64 `json:"uid"`
	}
	if err := odooCall(ctx, client, login, "", map[string]any{
		"db": opts.Database, "login": odooLogin(system), "password": secret,
	}, &session); err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("odoo login: %w", err)
	}
	if session.UID == 0 {
		return 0, 0, 0, 0, "", fmt.Errorf("odoo login was refused")
	}

	endpoint, err := syncEndpoint(system, opts)
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	fields := odooFields(opts.Entity, opts.externalField(), opts.CursorField, mapped)
	var records []json.RawMessage
	if err := odooCall(ctx, client, endpoint, opts.Database, map[string]any{
		"model": odooModel(opts.Entity), "method": "search_read",
		"args": []any{[]any{}},
		"kwargs": map[string]any{
			"fields": fields,
			"limit":  500,
		},
	}, &records); err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("odoo search_read: %w", err)
	}

	now := s.now()
	for _, record := range records {
		external := recordString(record, opts.externalField())
		if external == "" {
			failed++
			continue
		}
		made, changed, err := links.UpsertLink(ctx, system.TenantID, system.ID, opts.Entity,
			external, nil, mappedDigest(record, mapped), now)
		if err != nil {
			return seen, created, updated, failed, cursor, err
		}
		switch {
		case made:
			created++
		case changed:
			updated++
		}
		seen++
		if opts.CursorField != "" {
			if value := recordString(record, opts.CursorField); value != "" {
				cursor = value
			}
		}
	}
	return seen, created, updated, failed, cursor, nil
}

func odooLogin(system ExternalSystem) string {
	if system.Username != nil && strings.TrimSpace(*system.Username) != "" {
		return strings.TrimSpace(*system.Username)
	}
	return "admin"
}

func odooModel(entity string) string {
	if entity == "department" {
		return "hr.department"
	}
	return "hr.employee"
}

// odooFields asks for the mapped columns plus the identifiers the link and cursor need. The
// extra personnel fields let a live run prove that an unmapped change does not rewrite a link.
func odooFields(entity, external, cursorField string, mapped map[string]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(mapped)+8)
	add := func(field string) {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			return
		}
		seen[field] = true
		out = append(out, field)
	}
	add(external)
	add(cursorField)
	if entity == "department" {
		add("name")
		add("parent_id")
		add("active")
	} else {
		add("name")
		add("work_email")
		add("department_id")
		add("job_title")
		add("active")
	}
	for remote := range mapped {
		add(remote)
	}
	sort.Strings(out)
	return out
}

// odooCall posts one JSON-RPC call and unwraps result or error. It never puts the credential in
// an error string: a login failure is reported without the password that produced it.
func odooCall(ctx context.Context, client *http.Client, endpoint *url.URL, database string,
	params map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "call", "params": params, "id": 1,
	})
	if err != nil {
		return fmt.Errorf("encode Odoo request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(),
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Odoo request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "AccessNow-Sync/1")
	if database != "" {
		request.Header.Set("X-Odoo-Database", database)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call Odoo: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, syncMaxBody))
	if err != nil {
		return fmt.Errorf("read Odoo: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("Odoo answered %d: %s", response.StatusCode,
			truncate(string(payload), 300))
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode Odoo response: %w", err)
	}
	if envelope.Error != nil {
		message := envelope.Error.Data.Message
		if message == "" {
			message = envelope.Error.Message
		}
		return fmt.Errorf("Odoo error: %s", truncate(message, 300))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode Odoo result: %w", err)
	}
	return nil
}

// push sends this tenant's own register to the remote system. Only entities with a stable,
// non-sensitive shape are offered, and the body is an array under "items" so a receiver has one
// envelope to parse.
func (s *Service) push(ctx context.Context, system ExternalSystem, opts systemOptions,
	source localRecordReader) (seen, updated int, err error) {
	records, err := source.LocalRecords(ctx, system.TenantID, opts.Entity, 500)
	if err != nil {
		return 0, 0, err
	}
	if len(records) == 0 {
		return 0, 0, nil
	}
	endpoint, err := syncEndpoint(system, opts)
	if err != nil {
		return 0, 0, err
	}
	body, err := json.Marshal(map[string]any{"entity": opts.Entity, "items": records})
	if err != nil {
		return 0, 0, fmt.Errorf("encode sync body: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(),
		bytes.NewReader(body))
	if err != nil {
		return 0, 0, fmt.Errorf("build sync request: %w", err)
	}
	headers, err := authorize(system)
	if err != nil {
		return 0, 0, err
	}
	request.Header = headers
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "AccessNow-Sync/1")

	response, err := syncHTTPClient(opts.timeout()).Do(request)
	if err != nil {
		return 0, 0, fmt.Errorf("call %s: %w", system.Name, err)
	}
	defer response.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(response.Body, syncMaxBody))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return len(records), 0, fmt.Errorf("receiver answered %d: %s",
			response.StatusCode, truncate(string(reply), 300))
	}
	return len(records), len(records), nil
}

// authorize builds the headers for one call. The secret is read from the environment by name at
// this moment; it is never stored, cached or logged.
func authorize(system ExternalSystem) (http.Header, error) {
	headers := http.Header{}
	if system.AuthType == "" || system.AuthType == "none" {
		return headers, nil
	}
	secret, err := syncCredential(system)
	if err != nil {
		return nil, err
	}
	switch system.AuthType {
	case "bearer":
		headers.Set("Authorization", "Bearer "+secret)
	case "api_key":
		headers.Set("X-API-Key", secret)
	case "basic":
		user := ""
		if system.Username != nil {
			user = *system.Username
		}
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + secret))
		headers.Set("Authorization", "Basic "+token)
	default:
		return nil, errs.BadField("auth_type", "is not a supported authentication type")
	}
	return headers, nil
}

func syncCredential(system ExternalSystem) (string, error) {
	if system.CredentialsRef == nil || strings.TrimSpace(*system.CredentialsRef) == "" {
		return "", errs.BadField("credentials_ref", "is required for "+system.AuthType)
	}
	name := *system.CredentialsRef
	if !syncCredentialName.MatchString(name) {
		return "", errs.BadField("credentials_ref",
			"must name an environment variable starting with ACCESSNOW_SYNC_")
	}
	secret := strings.TrimSpace(os.Getenv(name))
	if secret == "" {
		return "", errs.BadField("credentials_ref", name+" is not set in the environment")
	}
	return secret, nil
}

func syncEndpoint(system ExternalSystem, opts systemOptions) (*url.URL, error) {
	if system.BaseURL == nil || strings.TrimSpace(*system.BaseURL) == "" {
		return nil, errs.BadField("base_url", "is not set on this system")
	}
	path := opts.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	parsed, err := url.Parse(strings.TrimRight(*system.BaseURL, "/") + path)
	if err != nil {
		return nil, errs.BadField("base_url", "is not a URL")
	}
	return parsed, nil
}

// syncTransport builds the transport a sync dials through. It is a variable so a test can hand
// the client a receiver it trusts; production only ever gets guardedSyncTransport.
var syncTransport = func(timeout time.Duration) http.RoundTripper {
	return guardedSyncTransport(timeout)
}

// syncHTTPClient is the webhook transport's rule applied to a sync: connect only to a public
// address, and re-check every redirect, because a name can resolve differently between the save
// and the call.
func syncHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: syncTransport(timeout),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("sync target redirected too many times")
			}
			return validSyncBaseURL(req.URL.String())
		},
	}
}

func guardedSyncTransport(timeout time.Duration) *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !publicIP(tcp.IP) {
				_ = conn.Close()
				return nil, fmt.Errorf("sync target resolved to a private address")
			}
			return conn, nil
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
}

// decodeRecords accepts either a bare array or an envelope with items and a next cursor. A
// receiver that returns neither is a receiver this module cannot read, and saying so is better
// than syncing zero records as if that were the answer.
func decodeRecords(payload []byte) ([]json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("receiver returned an empty body")
	}
	if trimmed[0] == '[' {
		var list []json.RawMessage
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, "", fmt.Errorf("records are not a JSON array: %w", err)
		}
		return list, "", nil
	}
	var envelope struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"next"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, "", fmt.Errorf("records are not JSON: %w", err)
	}
	if envelope.Items == nil {
		return nil, "", fmt.Errorf("receiver returned no items array")
	}
	return envelope.Items, envelope.Next, nil
}

func recordString(record json.RawMessage, field string) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(record, &object); err != nil {
		return ""
	}
	value, ok := object[field]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		return number.String()
	}
	return ""
}

func systemFieldMap(system ExternalSystem) map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(system.FieldMap) != "" {
		_ = json.Unmarshal([]byte(system.FieldMap), &fields)
	}
	return fields
}

// mappedDigest hashes only the remote fields the system maps. A sync link carries no copied
// values, so the digest is where a field map has an effect: mapped changes rewrite the link,
// unmapped changes do not.
func mappedDigest(record json.RawMessage, fields map[string]string) string {
	if len(fields) == 0 {
		return digestOf(record)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(record, &source); err != nil {
		return digestOf(record)
	}
	mapped := make(map[string]json.RawMessage, len(fields))
	for remote, local := range fields {
		if value, ok := source[remote]; ok {
			mapped[local] = value
		}
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		return digestOf(record)
	}
	return digestOf(encoded)
}

func digestOf(record json.RawMessage) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, record); err != nil {
		compact.Write(record)
	}
	sum := sha256.Sum256(compact.Bytes())
	return hex.EncodeToString(sum[:])
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
