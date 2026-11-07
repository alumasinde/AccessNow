package reports

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// params is the stored shape of a schedule's or a run's choices. It is an object rather than a
// bare string so a second choice - columns, a filter - can be added without a migration.
type params struct {
	GroupBy string `json:"group_by,omitempty"`
}

// RunRequest is one export the desk asks for. from and to are optional: with neither, the
// report covers the last day, which is what somebody opening the screen usually wants.
type RunRequest struct {
	DefinitionKey string     `json:"definition_key" validate:"required,max=64"`
	From          *time.Time `json:"from"`
	To            *time.Time `json:"to"`
	GroupBy       string     `json:"group_by" validate:"omitempty,max=32"`
	Format        string     `json:"format" validate:"omitempty,oneof=csv xlsx pdf"`
}

// ScheduleRequest creates or replaces a recurring export.
type ScheduleRequest struct {
	Name               string   `json:"name" validate:"required,max=255"`
	DefinitionKey      string   `json:"definition_key" validate:"required,max=64"`
	Frequency          string   `json:"frequency" validate:"required,oneof=hourly daily weekly monthly"`
	DayOfWeek          *int     `json:"day_of_week" validate:"omitempty,min=1,max=7"`
	DayOfMonth         *int     `json:"day_of_month" validate:"omitempty,min=1,max=28"`
	HourOfDay          *int     `json:"hour_of_day" validate:"omitempty,min=0,max=23"`
	MinuteOfHour       *int     `json:"minute_of_hour" validate:"omitempty,min=0,max=59"`
	Timezone           string   `json:"timezone" validate:"omitempty,max=64"`
	LookbackDays       *int     `json:"lookback_days" validate:"omitempty,min=1,max=90"`
	GroupBy            string   `json:"group_by" validate:"omitempty,max=32"`
	Format             string   `json:"format" validate:"omitempty,oneof=csv xlsx pdf"`
	Recipients         []string `json:"recipients" validate:"omitempty,max=20,dive,email"`
	RecipientRoles     []string `json:"recipient_roles" validate:"omitempty,max=10,dive,max=64"`
	SendToTenantAdmins *bool    `json:"send_to_tenant_admins"`
	IsActive           *bool    `json:"is_active"`
}

// DefinitionView is one catalogue row as the screen reads it.
type DefinitionView struct {
	Key           string   `json:"definition_key"`
	Name          string   `json:"name"`
	ModuleKey     string   `json:"module_key"`
	Description   *string  `json:"description,omitempty"`
	PeriodGrain   string   `json:"period_grain"`
	DefaultFormat string   `json:"default_format"`
	Groupings     []string `json:"groupings"`
}

func definitionView(def Definition) DefinitionView {
	view := DefinitionView{
		Key: def.Key, Name: def.Name, ModuleKey: def.ModuleKey,
		Description: def.Description, PeriodGrain: def.PeriodGrain,
		DefaultFormat: def.DefaultFormat,
	}
	if rep, ok := catalogue[def.Key]; ok {
		view.Groupings = rep.groupsOf()
	}
	return view
}

// RunView is one export on the register. It never carries the bytes: the file is fetched from
// /reports/runs/{runID}/download, so listing fifty runs does not move fifty files.
type RunView struct {
	ID          int64      `json:"id"`
	Definition  string     `json:"definition_key"`
	ScheduleID  *int64     `json:"report_schedule_id,omitempty"`
	TriggeredBy string     `json:"triggered_by"`
	PeriodFrom  *time.Time `json:"period_from,omitempty"`
	PeriodTo    *time.Time `json:"period_to,omitempty"`
	Format      string     `json:"format"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	DurationMS  *int       `json:"duration_ms,omitempty"`
	RowCount    *int       `json:"row_count,omitempty"`
	FileName    *string    `json:"file_name,omitempty"`
	FileSize    *int64     `json:"file_size_bytes,omitempty"`
	Checksum    *string    `json:"checksum_sha256,omitempty"`
	ErrorCode   *string    `json:"error_code,omitempty"`
	Error       *string    `json:"error_message,omitempty"`
	RequestedBy *int64     `json:"requested_by_user_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func runView(run Run) RunView {
	return RunView{
		ID: run.ID, Definition: run.Definition, ScheduleID: run.ScheduleID,
		TriggeredBy: run.TriggeredBy, PeriodFrom: run.PeriodFrom, PeriodTo: run.PeriodTo,
		Format: run.Format, Status: run.Status, StartedAt: run.StartedAt,
		FinishedAt: run.FinishedAt, DurationMS: run.DurationMS, RowCount: run.RowCount,
		FileName: run.FileName, FileSize: run.FileSize, Checksum: run.Checksum,
		ErrorCode: run.ErrorCode, Error: run.ErrorMessage, RequestedBy: run.RequestedBy,
		CreatedAt: run.CreatedAt,
	}
}

func runViews(runs []Run) []RunView {
	out := make([]RunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, runView(run))
	}
	return out
}

// ScheduleView is a recurring export with its stored choices unpacked, so a client never has
// to know that two of these columns are JSON text.
type ScheduleView struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Definition     string     `json:"definition_key"`
	Frequency      string     `json:"frequency"`
	DayOfWeek      *int       `json:"day_of_week,omitempty"`
	DayOfMonth     *int       `json:"day_of_month,omitempty"`
	HourOfDay      int        `json:"hour_of_day"`
	MinuteOfHour   int        `json:"minute_of_hour"`
	Timezone       string     `json:"timezone"`
	LookbackDays   int        `json:"lookback_days"`
	Format         string     `json:"format"`
	GroupBy        string     `json:"group_by"`
	Recipients     []string   `json:"recipients"`
	RecipientRoles []string   `json:"recipient_roles"`
	SendToAdmins   bool       `json:"send_to_tenant_admins"`
	IsActive       bool       `json:"is_active"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastStatus     *string    `json:"last_status,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func scheduleView(s Schedule) ScheduleView {
	var saved params
	if err := json.Unmarshal([]byte(s.Params), &saved); err != nil {
		saved = params{}
	}
	return ScheduleView{
		ID: s.ID, Name: s.Name, Definition: s.DefinitionKey, Frequency: s.Frequency,
		DayOfWeek: s.DayOfWeek, DayOfMonth: s.DayOfMonth, HourOfDay: s.HourOfDay,
		MinuteOfHour: s.MinuteOfHour, Timezone: s.Timezone, LookbackDays: s.LookbackDays,
		Format: s.Format, GroupBy: saved.GroupBy, Recipients: jsonStrings(s.Recipients),
		RecipientRoles: jsonStrings(s.RecipientRoles), SendToAdmins: s.SendToAdmins,
		IsActive: s.IsActive, NextRunAt: s.NextRunAt, LastRunAt: s.LastRunAt,
		LastStatus: s.LastStatus, LastError: s.LastError, CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func scheduleViews(schedules []Schedule) []ScheduleView {
	out := make([]ScheduleView, 0, len(schedules))
	for _, schedule := range schedules {
		out = append(out, scheduleView(schedule))
	}
	return out
}

// jsonStrings reads an array stored as JSON. A malformed value is an empty list rather than an
// error: a schedule whose recipients cannot be read still has a definition and a next run, and
// refusing the whole row would hide that.
func jsonStrings(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// ParseRuns reads ?definition_key=&status= and the page cursor.
func ParseRuns(r *http.Request) (RunFilter, httpx.Page, error) {
	query := r.URL.Query()
	filter := RunFilter{Definition: query.Get("definition_key"), Status: query.Get("status")}
	if filter.Status != "" {
		switch filter.Status {
		case RunRunning, RunSucceeded, RunFailed:
		default:
			return filter, httpx.Page{}, errs.BadField("status", "is not a run status")
		}
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	if err != nil {
		return filter, httpx.Page{}, err
	}
	return filter, page, nil
}

// emailList validates the addresses a schedule mails. An address is refused rather than
// dropped: a tenant who typed one wrong letter should hear about it now, not on Monday.
func emailList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		address := strings.TrimSpace(value)
		if address == "" {
			continue
		}
		parsed, err := mail.ParseAddress(address)
		if err != nil {
			return nil, errs.BadField("recipients", address+" is not an email address")
		}
		key := strings.ToLower(parsed.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, parsed.Address)
	}
	return out, nil
}

func roleList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		role := strings.TrimSpace(value)
		if role == "" {
			continue
		}
		if len(role) > 64 || strings.ContainsAny(role, " \t") {
			return nil, errs.BadField("recipient_roles", role+" is not a role key")
		}
		if seen[role] {
			continue
		}
		seen[role] = true
		out = append(out, role)
	}
	return out, nil
}

// exportName is the file the caller is handed. The definition key is already restricted to
// lowercase words and underscores by the seed, and the date comes from the window, so the name
// is stable enough to find again and safe as a filename.
func exportName(key string, from time.Time, format Format) string {
	return key + "-" + from.UTC().Format("20060102") + extensionOf(format)
}
