// Package reports is the module behind item 13 of the design: the aggregate registers a
// tenant can run on demand and the schedules that mail them. It reads; it never writes to
// another module's table, and it holds no file store: an export is streamed once and the
// run row keeps what was in it.
package reports

import (
	"strings"
	"time"

	_ "time/tzdata" // a schedule is stored with its own zone, and Windows ships no zoneinfo

	"accessnow/internal/platform/errs"
)

// ModuleKey is the tenant's switch for this surface.
const ModuleKey = "reports_exports"

// aggregateType names this module's rows on the outbox.
const aggregateType = "report_run"

// maxRunRows caps one export. A report that returns more than this is a report that should
// have been narrowed by its period, and a single CSV a browser cannot open is not a favour.
const maxRunRows = 20000

// maxPeriod is how much of the past one run may cover. A year is longer than any seeded
// report is useful over and short enough that the aggregate never scans a whole table.
const maxPeriod = 366 * 24 * time.Hour

// Format is what an export is rendered as. All three are written by this package: csv and xlsx
// by their own encoders, pdf by a small paginated table writer that embeds no fonts.
type Format string

const (
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
	FormatPDF  Format = "pdf"
)

func (f Format) Valid() bool {
	switch f {
	case FormatCSV, FormatXLSX, FormatPDF:
		return true
	}
	return false
}

func formatOf(value string) Format {
	format := Format(strings.ToLower(strings.TrimSpace(value)))
	if format == "" {
		return FormatCSV
	}
	return format
}

// Frequency is how often a schedule fires, on the tenant's own wall clock.
type Frequency string

const (
	FreqHourly  Frequency = "hourly"
	FreqDaily   Frequency = "daily"
	FreqWeekly  Frequency = "weekly"
	FreqMonthly Frequency = "monthly"
)

func (f Frequency) Valid() bool {
	switch f {
	case FreqHourly, FreqDaily, FreqWeekly, FreqMonthly:
		return true
	}
	return false
}

// TriggeredBy says who asked for a run: a person at the desk, a schedule, the API or a
// backfill. It mirrors chk_report_runs_triggered.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
	TriggerAPI      = "api"
)

// Status values a run moves through.
const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
)

// ScheduleStatus is the last outcome recorded on a schedule.
const (
	ScheduleSucceeded = "succeeded"
	ScheduleFailed    = "failed"
	ScheduleSkipped   = "skipped"
)

// Definition is one report the catalogue offers. The JSON columns in the table are not read
// here: the groupings a report actually supports live next to its SQL in catalogue.go, which
// is the only place that can honour them.
type Definition struct {
	ID            int16   `db:"id"`
	Key           string  `db:"definition_key"`
	Name          string  `db:"name"`
	ModuleKey     string  `db:"module_key"`
	Description   *string `db:"description"`
	PeriodGrain   string  `db:"period_grain"`
	DefaultFormat string  `db:"default_format"`
	SortOrder     int16   `db:"sort_order"`
}

// Schedule is one tenant's recurring export. Params is the JSON object the report is run
// with; Recipients and Roles are JSON arrays kept as text because nothing reads them but
// this module and the notifier.
type Schedule struct {
	ID             int64      `db:"id"`
	TenantID       int64      `db:"tenant_id"`
	DefinitionKey  string     `db:"definition_key"`
	Name           string     `db:"name"`
	Frequency      string     `db:"frequency"`
	DayOfWeek      *int       `db:"day_of_week"`
	DayOfMonth     *int       `db:"day_of_month"`
	HourOfDay      int        `db:"hour_of_day"`
	MinuteOfHour   int        `db:"minute_of_hour"`
	Timezone       string     `db:"timezone"`
	LookbackDays   int        `db:"lookback_days"`
	Params         string     `db:"params"`
	Format         string     `db:"format"`
	Recipients     string     `db:"recipients"`
	RecipientRoles string     `db:"recipient_roles"`
	SendToAdmins   bool       `db:"send_to_tenant_admins"`
	IsActive       bool       `db:"is_active"`
	NextRunAt      *time.Time `db:"next_run_at"`
	LastRunAt      *time.Time `db:"last_run_at"`
	LastStatus     *string    `db:"last_status"`
	LastError      *string    `db:"last_error"`
	CreatedBy      *int64     `db:"created_by"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

// Run is one execution of one definition. The rendered bytes are kept on the row so the
// register can hand the same file back later; the retention module can purge that column.
type Run struct {
	ID          int64      `db:"id"`
	TenantID    int64      `db:"tenant_id"`
	Definition  string     `db:"definition_key"`
	ScheduleID  *int64     `db:"report_schedule_id"`
	TriggeredBy string     `db:"triggered_by"`
	PeriodFrom  *time.Time `db:"period_from"`
	PeriodTo    *time.Time `db:"period_to"`
	Params      string     `db:"params"`
	Format      string     `db:"format"`
	Status      string     `db:"status"`
	StartedAt   *time.Time `db:"started_at"`
	FinishedAt  *time.Time `db:"finished_at"`
	DurationMS  *int       `db:"duration_ms"`
	RowCount    *int       `db:"row_count"`
	FileName    *string    `db:"file_name"`
	FileSize    *int64     `db:"file_size_bytes"`
	Checksum    *string    `db:"checksum_sha256"`
	// ContentType describes the stored artifact. The bytes are read by RunArtifact and are
	// never part of a listing.
	ContentType  *string   `db:"artifact_content_type"`
	ErrorCode    *string   `db:"error_code"`
	ErrorMessage *string   `db:"error_message"`
	RequestedBy  *int64    `db:"requested_by_user_id"`
	CreatedAt    time.Time `db:"created_at"`
}

// RunFilter is the run register's question.
type RunFilter struct {
	TenantID   int64
	Definition string
	Status     string
	Limit      int
	Before     *time.Time
	BeforeID   int64
}

// Artifact is a rendered export on its way to the caller. Nothing keeps it.
type Artifact struct {
	Run         Run
	FileName    string
	ContentType string
	Bytes       []byte
}

// nextRun is the tenant's wall clock, converted back to UTC for the column. It is computed
// from the schedule's own zone rather than from UTC so a report scheduled for 07:00 still
// arrives at 07:00 on the morning the clocks change.
func nextRun(s Schedule, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.Time{}, errs.BadField("timezone", "is not a known time zone")
	}
	local := after.In(loc)
	at := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, s.HourOfDay, s.MinuteOfHour, 0, 0, loc)
	}

	switch Frequency(s.Frequency) {
	case FreqHourly:
		next := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(),
			s.MinuteOfHour, 0, 0, loc)
		if !next.After(local) {
			next = next.Add(time.Hour)
		}
		return next.UTC(), nil
	case FreqDaily:
		next := at(local.Year(), local.Month(), local.Day())
		if !next.After(local) {
			next = next.AddDate(0, 0, 1)
		}
		return next.UTC(), nil
	case FreqWeekly:
		if s.DayOfWeek == nil {
			return time.Time{}, errs.BadField("day_of_week", "is required for a weekly schedule")
		}
		for offset := 0; offset <= 7; offset++ {
			day := local.AddDate(0, 0, offset)
			next := at(day.Year(), day.Month(), day.Day())
			if isoWeekday(next) != *s.DayOfWeek || !next.After(local) {
				continue
			}
			return next.UTC(), nil
		}
		return time.Time{}, errs.New(errs.CodeInternal, "could not place the next weekly run")
	case FreqMonthly:
		if s.DayOfMonth == nil {
			return time.Time{}, errs.BadField("day_of_month", "is required for a monthly schedule")
		}
		next := at(local.Year(), local.Month(), *s.DayOfMonth)
		if !next.After(local) {
			next = at(local.Year(), local.Month()+1, *s.DayOfMonth)
		}
		return next.UTC(), nil
	default:
		return time.Time{}, errs.BadField("frequency", "must be hourly, daily, weekly or monthly")
	}
}

// isoWeekday is Monday 1 .. Sunday 7, which is how the column is documented.
func isoWeekday(t time.Time) int {
	if t.Weekday() == time.Sunday {
		return 7
	}
	return int(t.Weekday())
}
