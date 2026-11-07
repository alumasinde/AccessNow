package reports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Service runs the catalogue and owns the schedules. It reads other modules' tables through
// the same database every module shares and never writes one: a report is evidence, not a
// side effect.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	access *access.Service
	events events.Publisher
	chain  audit.Writer
	now    func() time.Time
}

func NewService(pool *sqlx.DB, repo *Repository, accessSvc *access.Service,
	bus events.Publisher, chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, access: accessSvc, events: bus, chain: chain,
		now: func() time.Time { return time.Now().UTC() }}
}

// enabled is the module switch, checked again after the route guard so a worker and a request
// agree on what a disabled module means.
func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// Definitions is the catalogue a tenant may actually run: the module flag filters the rows,
// and a seeded definition with no SQL behind it is left out rather than shown as a button
// that answers 404.
func (s *Service) Definitions(ctx context.Context, tenantID int64) ([]Definition, error) {
	all, err := s.repo.Definitions(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]Definition, 0, len(all))
	for _, def := range all {
		if implemented(def.Key) {
			out = append(out, def)
		}
	}
	return out, nil
}

// Run executes one report and hands back the bytes. Nothing is stored: the run row records
// what was produced, and the caller who asked for it is the only holder of the file.
func (s *Service) Run(ctx context.Context, scope tenancy.Scope, req RunRequest) (Artifact, error) {
	on, err := s.enabled(ctx, scope.TenantID)
	if err != nil {
		return Artifact{}, err
	}
	if !on {
		return Artifact{}, errs.ModuleDisabled(ModuleKey)
	}
	return s.run(ctx, scope.TenantID, scope.UserID, TriggerManual, nil, req)
}

// run is the shared path for a person, a schedule and a replay. scheduleID is nil for the
// first and set for the rest, which is what makes a run attributable after the fact.
func (s *Service) run(ctx context.Context, tenantID, userID int64, trigger string,
	scheduleID *int64, req RunRequest) (Artifact, error) {
	started := s.now()

	def, err := s.repo.Definition(ctx, tenantID, req.DefinitionKey)
	if err != nil {
		return Artifact{}, err
	}
	rep, ok := catalogue[def.Key]
	if !ok {
		return Artifact{}, errs.NotFound("report implementation for " + def.Key)
	}

	format := formatOf(req.Format)
	if !format.Valid() {
		return Artifact{}, errs.Invalid("this report cannot be exported in that format",
			errs.InvalidField("format", "choose csv, xlsx or pdf"))
	}

	group := rep.groupOf(req.GroupBy)
	query, headers, ok := rep.query(group)
	if !ok {
		return Artifact{}, errs.BadField("group_by", "this report does not group by "+group)
	}

	to := s.now()
	if req.To != nil {
		to = req.To.UTC()
	}
	from := to.Add(-24 * time.Hour)
	if req.From != nil {
		from = req.From.UTC()
	}
	if !from.Before(to) {
		return Artifact{}, errs.BadField("from", "must be before to")
	}
	if to.Sub(from) > maxPeriod {
		return Artifact{}, errs.BadField("from", "a single run may not cover more than a year")
	}

	params, err := json.Marshal(params{GroupBy: group})
	if err != nil {
		return Artifact{}, errs.Internal(fmt.Errorf("encode report params: %w", err))
	}
	runID, err := s.repo.InsertRun(ctx, Run{
		TenantID: tenantID, Definition: def.Key, ScheduleID: scheduleID,
		TriggeredBy: trigger, PeriodFrom: &from, PeriodTo: &to, Params: string(params),
		Format: string(format), RequestedBy: optionalID(userID),
	}, started)
	if err != nil {
		return Artifact{}, err
	}

	rows, err := s.repo.Rows(ctx, query, tenantID, from, to, rep.limit)
	if err != nil {
		s.fail(ctx, tenantID, runID, "query_failed", err, started)
		return Artifact{}, err
	}
	bytes, err := render(format, reportTitle(def.Name, from, to), headers, rows)
	if err != nil {
		s.fail(ctx, tenantID, runID, "render_failed", err, started)
		return Artifact{}, err
	}

	fileName := exportName(def.Key, from, format)
	sum := sha256.Sum256(bytes)
	duration := int(s.now().Sub(started).Milliseconds())
	if err := s.repo.FinishRun(ctx, tenantID, runID, len(rows), fileName, int64(len(bytes)),
		hex.EncodeToString(sum[:]), bytes, contentTypeOf(format), duration, s.now()); err != nil {
		return Artifact{}, err
	}

	run, err := s.repo.Run(ctx, tenantID, runID)
	if err != nil {
		return Artifact{}, err
	}
	after := map[string]any{
		"definition": def.Key, "format": string(format), "rows": len(rows),
		"from": from, "to": to, "group_by": group, "file_name": fileName,
	}
	if err := s.record(ctx, tenancy.Scope{TenantID: tenantID, UserID: userID}, "report.run",
		"report_run", runID, "", after); err != nil {
		// The export is already made and recorded; a failed audit row must not throw it away,
		// but it must be visible in the log.
		logging.From(ctx).ErrorContext(ctx, "report run audit failed", "run_id", runID, "error", err)
	}

	return Artifact{
		Run: run, FileName: fileName, ContentType: contentTypeOf(format), Bytes: bytes,
	}, nil
}

// fail records why a run could not produce a file. The caller still gets the error; the run
// row is how the next person finds out which report has been broken since Tuesday.
func (s *Service) fail(ctx context.Context, tenantID, runID int64, code string, cause error,
	started time.Time) {
	duration := int(s.now().Sub(started).Milliseconds())
	if err := s.repo.FailRun(ctx, tenantID, runID, code, cause.Error(), duration, s.now()); err != nil {
		logging.From(ctx).ErrorContext(ctx, "could not record failed report run",
			"run_id", runID, "error", err)
	}
}

// Runs is the register of what has been produced.
func (s *Service) Runs(ctx context.Context, filter RunFilter) ([]Run, error) {
	return s.repo.Runs(ctx, filter)
}

// RunByID reads one export from the register.
func (s *Service) RunByID(ctx context.Context, tenantID, id int64) (Run, error) {
	return s.repo.Run(ctx, tenantID, id)
}

// Download returns the bytes a run produced. A run that predates stored artifacts, or whose
// file was purged, keeps its receipt and says there is nothing to hand back.
func (s *Service) Download(ctx context.Context, tenantID, id int64) (Artifact, error) {
	run, content, contentType, err := s.repo.RunArtifact(ctx, tenantID, id)
	if err != nil {
		return Artifact{}, err
	}
	if len(content) == 0 || run.Status != RunSucceeded {
		return Artifact{}, errs.Conflict("this run has no stored file")
	}
	name := "report-" + fmt.Sprint(run.ID)
	if run.FileName != nil && *run.FileName != "" {
		name = *run.FileName
	}
	return Artifact{Run: run, FileName: name, ContentType: contentType, Bytes: content}, nil
}

// The schedules.

func (s *Service) Schedules(ctx context.Context, tenantID int64) ([]Schedule, error) {
	return s.repo.Schedules(ctx, tenantID, 100)
}

func (s *Service) Schedule(ctx context.Context, tenantID, id int64) (Schedule, error) {
	return s.repo.Schedule(ctx, tenantID, id)
}

// CreateSchedule files a recurring export. The next run is computed before the row exists so
// a schedule that cannot be placed in the tenant's zone is refused rather than stored inert.
func (s *Service) CreateSchedule(ctx context.Context, scope tenancy.Scope,
	req ScheduleRequest) (Schedule, error) {
	schedule, err := s.buildSchedule(ctx, scope, req, Schedule{})
	if err != nil {
		return Schedule{}, err
	}
	now := s.now()
	schedule.CreatedBy = optionalID(scope.UserID)

	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).InsertSchedule(ctx, schedule, now)
		if err != nil {
			return err
		}
		id = filed
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "report.schedule_created", SubjectType: "report_schedule", SubjectID: filed,
			SiteID: scope.SiteID, After: map[string]any{
				"name": schedule.Name, "definition": schedule.DefinitionKey,
				"frequency": schedule.Frequency, "format": schedule.Format,
				"timezone": schedule.Timezone,
			},
		})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return Schedule{}, errs.Conflict("a schedule with that name already exists")
		}
		return Schedule{}, err
	}
	return s.repo.Schedule(ctx, scope.TenantID, id)
}

// UpdateSchedule replaces the definition of a schedule. It is a full replace on purpose: a
// patch that leaves yesterday's day_of_week behind while switching to daily is the kind of
// half-configured schedule that runs twice and blames the clock.
func (s *Service) UpdateSchedule(ctx context.Context, scope tenancy.Scope, id int64,
	req ScheduleRequest) (Schedule, error) {
	existing, err := s.repo.Schedule(ctx, scope.TenantID, id)
	if err != nil {
		return Schedule{}, err
	}
	schedule, err := s.buildSchedule(ctx, scope, req, existing)
	if err != nil {
		return Schedule{}, err
	}
	schedule.ID = id
	now := s.now()

	var changed bool
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).UpdateSchedule(ctx, schedule, now)
		if err != nil {
			return err
		}
		changed = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "report.schedule_updated", SubjectType: "report_schedule", SubjectID: id,
			SiteID: scope.SiteID, Before: map[string]any{
				"name": existing.Name, "frequency": existing.Frequency, "format": existing.Format,
			},
			After: map[string]any{
				"name": schedule.Name, "frequency": schedule.Frequency, "format": schedule.Format,
				"timezone": schedule.Timezone, "is_active": schedule.IsActive,
			},
		})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return Schedule{}, errs.Conflict("a schedule with that name already exists")
		}
		return Schedule{}, err
	}
	if !changed {
		return Schedule{}, errs.NotFound("report schedule")
	}
	return s.repo.Schedule(ctx, scope.TenantID, id)
}

func (s *Service) DeleteSchedule(ctx context.Context, scope tenancy.Scope, id int64) error {
	var gone bool
	now := s.now()
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).SoftDelete(ctx, scope.TenantID, id, now)
		if err != nil {
			return err
		}
		gone = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "report.schedule_deleted", SubjectType: "report_schedule", SubjectID: id,
			SiteID: scope.SiteID, After: map[string]any{"deleted": true},
		})
	})
	if err != nil {
		return err
	}
	if !gone {
		return errs.NotFound("report schedule")
	}
	return nil
}

// RunSchedule is the "send it now" button: the same report the schedule would produce, over
// the same lookback, attributed to the person who pressed it.
func (s *Service) RunSchedule(ctx context.Context, scope tenancy.Scope, id int64) (Artifact, error) {
	on, err := s.enabled(ctx, scope.TenantID)
	if err != nil {
		return Artifact{}, err
	}
	if !on {
		return Artifact{}, errs.ModuleDisabled(ModuleKey)
	}
	schedule, err := s.repo.Schedule(ctx, scope.TenantID, id)
	if err != nil {
		return Artifact{}, err
	}
	return s.runSchedule(ctx, schedule, scope.UserID)
}

// runSchedule builds the window from the schedule's own lookback and runs it. A period that
// starts at midnight is what a nightly digest means by "yesterday", so the window ends now and
// reaches back the number of days the tenant chose.
func (s *Service) runSchedule(ctx context.Context, schedule Schedule,
	userID int64) (Artifact, error) {
	to := s.now()
	days := schedule.LookbackDays
	if days <= 0 {
		days = 1
	}
	from := to.AddDate(0, 0, -days)

	var saved params
	if err := json.Unmarshal([]byte(schedule.Params), &saved); err != nil {
		saved = params{}
	}
	id := schedule.ID
	return s.run(ctx, schedule.TenantID, userID, TriggerSchedule, &id, RunRequest{
		DefinitionKey: schedule.DefinitionKey,
		From:          &from,
		To:            &to,
		GroupBy:       saved.GroupBy,
		Format:        schedule.Format,
	})
}

// buildSchedule validates a request into a row. existing carries the fields the request does
// not repeat - the creation time and author - so an update does not lose them.
func (s *Service) buildSchedule(ctx context.Context, scope tenancy.Scope, req ScheduleRequest,
	existing Schedule) (Schedule, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Schedule{}, errs.BadField("name", "is required")
	}
	def, err := s.repo.Definition(ctx, scope.TenantID, req.DefinitionKey)
	if err != nil {
		return Schedule{}, err
	}
	rep, ok := catalogue[def.Key]
	if !ok {
		return Schedule{}, errs.NotFound("report implementation for " + def.Key)
	}

	frequency := Frequency(strings.TrimSpace(req.Frequency))
	if !frequency.Valid() {
		return Schedule{}, errs.BadField("frequency", "must be hourly, daily, weekly or monthly")
	}
	group := rep.groupOf(req.GroupBy)
	if _, _, ok := rep.query(group); !ok {
		return Schedule{}, errs.BadField("group_by", "this report does not group by "+group)
	}

	format := formatOf(req.Format)
	if !format.Valid() {
		// The seeded default is pdf for most definitions. A tenant who never chose a format
		// gets csv rather than a schedule that cannot produce a file at all.
		format = FormatCSV
	}

	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = strings.TrimSpace(scope.Timezone)
	}
	if timezone == "" {
		timezone = "UTC"
	}

	schedule := Schedule{
		TenantID:      scope.TenantID,
		DefinitionKey: def.Key,
		Name:          name,
		Frequency:     string(frequency),
		Timezone:      timezone,
		LookbackDays:  1,
		Format:        string(format),
		SendToAdmins:  true,
		IsActive:      true,
		CreatedBy:     existing.CreatedBy,
	}
	if req.HourOfDay != nil {
		schedule.HourOfDay = *req.HourOfDay
	} else if existing.ID != 0 {
		schedule.HourOfDay = existing.HourOfDay
	} else {
		schedule.HourOfDay = 7
	}
	if req.MinuteOfHour != nil {
		schedule.MinuteOfHour = *req.MinuteOfHour
	} else if existing.ID != 0 {
		schedule.MinuteOfHour = existing.MinuteOfHour
	}
	if req.LookbackDays != nil {
		schedule.LookbackDays = *req.LookbackDays
	} else if existing.LookbackDays > 0 {
		schedule.LookbackDays = existing.LookbackDays
	}
	if schedule.LookbackDays < 1 || schedule.LookbackDays > 90 {
		return Schedule{}, errs.BadField("lookback_days", "must be between 1 and 90")
	}
	if schedule.HourOfDay < 0 || schedule.HourOfDay > 23 {
		return Schedule{}, errs.BadField("hour_of_day", "must be between 0 and 23")
	}
	if schedule.MinuteOfHour < 0 || schedule.MinuteOfHour > 59 {
		return Schedule{}, errs.BadField("minute_of_hour", "must be between 0 and 59")
	}
	if req.SendToTenantAdmins != nil {
		schedule.SendToAdmins = *req.SendToTenantAdmins
	}
	if req.IsActive != nil {
		schedule.IsActive = *req.IsActive
	} else if existing.ID != 0 {
		schedule.IsActive = existing.IsActive
	}

	switch frequency {
	case FreqWeekly:
		day := 1
		if req.DayOfWeek != nil {
			day = *req.DayOfWeek
		} else if existing.DayOfWeek != nil {
			day = *existing.DayOfWeek
		}
		if day < 1 || day > 7 {
			return Schedule{}, errs.BadField("day_of_week", "must be Monday 1 to Sunday 7")
		}
		schedule.DayOfWeek = &day
	case FreqMonthly:
		day := 1
		if req.DayOfMonth != nil {
			day = *req.DayOfMonth
		} else if existing.DayOfMonth != nil {
			day = *existing.DayOfMonth
		}
		if day < 1 || day > 28 {
			return Schedule{}, errs.BadField("day_of_month", "must be between 1 and 28")
		}
		schedule.DayOfMonth = &day
	}

	recipients, err := emailList(req.Recipients)
	if err != nil {
		return Schedule{}, err
	}
	roles, err := roleList(req.RecipientRoles)
	if err != nil {
		return Schedule{}, err
	}
	rawRecipients, err := json.Marshal(recipients)
	if err != nil {
		return Schedule{}, errs.Internal(fmt.Errorf("encode recipients: %w", err))
	}
	rawRoles, err := json.Marshal(roles)
	if err != nil {
		return Schedule{}, errs.Internal(fmt.Errorf("encode recipient roles: %w", err))
	}
	rawParams, err := json.Marshal(params{GroupBy: group})
	if err != nil {
		return Schedule{}, errs.Internal(fmt.Errorf("encode schedule params: %w", err))
	}
	schedule.Recipients = string(rawRecipients)
	schedule.RecipientRoles = string(rawRoles)
	schedule.Params = string(rawParams)

	if schedule.IsActive {
		next, err := nextRun(schedule, s.now())
		if err != nil {
			return Schedule{}, err
		}
		schedule.NextRunAt = &next
	}
	if existing.ID != 0 {
		schedule.ID = existing.ID
	}
	return schedule, nil
}

func (s *Service) record(ctx context.Context, scope tenancy.Scope, action, subjectType string,
	subjectID int64, reason string, after map[string]any) error {
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: action,
			SubjectType: subjectType, SubjectID: subjectID, SiteID: scope.SiteID,
			Reason: reason, After: after,
		})
	})
}

// RunDue is one pass of the worker: every schedule whose moment has come, run, recorded and
// announced. It never returns the per-schedule failures as an error, because one broken
// definition must not stop the rest of the tenants from getting their morning mail.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	due, err := s.repo.Due(ctx, s.now(), 25)
	if err != nil {
		return 0, err
	}
	ran := 0
	for _, schedule := range due {
		next, nextErr := nextRun(schedule, s.now())
		if nextErr != nil {
			s.mark(ctx, schedule, ScheduleFailed, nextErr.Error(), nil)
			continue
		}

		on, err := s.repo.ModuleEnabled(ctx, schedule.TenantID)
		if err != nil {
			s.mark(ctx, schedule, ScheduleFailed, err.Error(), &next)
			continue
		}
		if !on {
			s.mark(ctx, schedule, ScheduleSkipped, "reports are switched off for this tenant", &next)
			continue
		}

		artifact, runErr := s.runSchedule(ctx, schedule, 0)
		if runErr != nil {
			s.mark(ctx, schedule, ScheduleFailed, runErr.Error(), &next)
			continue
		}
		s.mark(ctx, schedule, ScheduleSucceeded, "", &next)
		s.announce(ctx, schedule, artifact)
		ran++
	}
	return ran, nil
}

func (s *Service) mark(ctx context.Context, schedule Schedule, status, message string,
	next *time.Time) {
	var reason *string
	if strings.TrimSpace(message) != "" {
		short := clip(message, 480)
		reason = &short
	}
	if err := s.repo.MarkRun(ctx, schedule.ID, status, reason, next, s.now()); err != nil {
		logging.From(ctx).ErrorContext(ctx, "could not record schedule outcome",
			"schedule_id", schedule.ID, "error", err)
	}
	if status == ScheduleFailed {
		logging.From(ctx).ErrorContext(ctx, "report schedule failed",
			"schedule_id", schedule.ID, "error", message)
	}
}

// announce tells the schedule's people the report is ready. It rides the outbox so the notice
// survives this process dying between the run and the mail, and a tenant without the
// notifications module simply has an event nobody subscribed to.
func (s *Service) announce(ctx context.Context, schedule Schedule, artifact Artifact) {
	if s.events == nil {
		return
	}
	payload := map[string]any{
		"report_key": schedule.DefinitionKey, "report_name": schedule.Name,
		"run_id": artifact.Run.ID, "rows": artifact.Run.RowCount,
		"format": schedule.Format, "file_name": artifact.FileName,
		"schedule_id": schedule.ID, "recipients": jsonStrings(schedule.Recipients),
		"recipient_roles":       jsonStrings(schedule.RecipientRoles),
		"send_to_tenant_admins": schedule.SendToAdmins,
	}
	if err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		return s.events.Publish(ctx, c, events.Event{
			TenantID: schedule.TenantID, EventKey: "report.ready",
			AggregateType: aggregateType, AggregateID: artifact.Run.ID,
			DedupeKey: "report-ready:" + fmt.Sprint(artifact.Run.ID), Payload: payload,
		})
	}); err != nil {
		logging.From(ctx).ErrorContext(ctx, "could not announce report",
			"run_id", artifact.Run.ID, "error", err)
	}
}

func optionalID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
