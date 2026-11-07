package reports

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// TestEverySeededDefinitionImplemented is the contract between 0025's catalogue and this
// package: the seeded keys are exactly the ones the desk can run. A definition without SQL is
// a button that 404s, and SQL without a definition is code nobody can reach.
func TestEverySeededDefinitionImplemented(t *testing.T) {
	seeded := []string{
		"gate_movements_daily", "visitor_log", "host_activity", "vehicle_dwell_times",
		"material_outstanding", "material_exceptions", "outpass_register", "contractor_expiry",
		"blocklist_hits", "approval_latency", "gate_utilisation", "guard_activity", "audit_summary",
	}
	for _, key := range seeded {
		if !implemented(key) {
			t.Fatalf("seeded definition %q has no report behind it", key)
		}
	}
	if len(catalogue) != len(seeded) {
		t.Fatalf("catalogue has %d reports for %d seeded definitions", len(catalogue), len(seeded))
	}
}

// TestEveryReportIsRunnable guards the shape a run depends on: a default grouping that exists,
// four bind parameters (tenant, from, to, limit), and a tenant filter that cannot be forgotten.
func TestEveryReportIsRunnable(t *testing.T) {
	for key, rep := range catalogue {
		if _, ok := rep.groups[rep.defGroup]; !ok {
			t.Fatalf("%s: default grouping %q is not one of its groups", key, rep.defGroup)
		}
		for group := range rep.groups {
			query, headers, ok := rep.query(group)
			if !ok {
				t.Fatalf("%s/%s: query refused a grouping it declares", key, group)
			}
			if !strings.Contains(query, "t.tenant_id = ?") {
				t.Fatalf("%s/%s: query has no tenant filter", key, group)
			}
			if got := strings.Count(query, "?"); got != 4 {
				t.Fatalf("%s/%s: query binds %d parameters, want 4", key, group, got)
			}
			if len(headers) != 3 || headers[0] == "" || headers[1] == "" || headers[2] == "" {
				t.Fatalf("%s/%s: headers are not three named columns: %v", key, group, headers)
			}
		}
	}
}

func TestQueryRefusesUnknownGrouping(t *testing.T) {
	rep := catalogue["gate_movements_daily"]
	if _, _, ok := rep.query("warden"); ok {
		t.Fatal("a grouping the report does not offer must be refused before any SQL exists")
	}
	if _, _, ok := rep.query(""); !ok {
		t.Fatal("an empty grouping should fall back to the default")
	}
}

func TestNextRunDaily(t *testing.T) {
	loc := time.UTC
	schedule := Schedule{Frequency: string(FreqDaily), HourOfDay: 7, MinuteOfHour: 30,
		Timezone: "UTC"}

	// Before today's slot: it is today.
	run, err := nextRun(schedule, time.Date(2026, 3, 2, 6, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 2, 7, 30, 0, 0, loc); !run.Equal(want) {
		t.Fatalf("next run = %s, want %s", run, want)
	}

	// After it: tomorrow.
	run, err = nextRun(schedule, time.Date(2026, 3, 2, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 3, 7, 30, 0, 0, loc); !run.Equal(want) {
		t.Fatalf("next run = %s, want %s", run, want)
	}
}

func TestNextRunWeeklyIsMondayOne(t *testing.T) {
	// 2026-03-04 is a Wednesday; the next Monday 1 at 09:00 is 2026-03-09.
	day := 1
	schedule := Schedule{Frequency: string(FreqWeekly), DayOfWeek: &day, HourOfDay: 9,
		Timezone: "UTC"}
	run, err := nextRun(schedule, time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC); !run.Equal(want) {
		t.Fatalf("next run = %s, want %s", run, want)
	}
	if run.Weekday() != time.Monday {
		t.Fatalf("weekly run landed on %s, want Monday", run.Weekday())
	}
}

func TestNextRunMonthlyRollsOver(t *testing.T) {
	day := 28
	schedule := Schedule{Frequency: string(FreqMonthly), DayOfMonth: &day, HourOfDay: 6,
		Timezone: "UTC"}
	run, err := nextRun(schedule, time.Date(2026, 2, 28, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 28, 6, 0, 0, 0, time.UTC); !run.Equal(want) {
		t.Fatalf("next run = %s, want %s", run, want)
	}
}

func TestNextRunRejectsUnknownTimezone(t *testing.T) {
	_, err := nextRun(Schedule{Frequency: string(FreqDaily), Timezone: "Mars/Olympus"}, time.Now())
	if err == nil {
		t.Fatal("an unknown time zone must be refused rather than silently run in UTC")
	}
}

func TestNextRunUsesTheTenantClock(t *testing.T) {
	// 03:00 UTC is 06:00 in Nairobi, so a 07:00 schedule there lands at 04:00 UTC.
	schedule := Schedule{Frequency: string(FreqDaily), HourOfDay: 7, Timezone: "Africa/Nairobi"}
	run, err := nextRun(schedule, time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 2, 4, 0, 0, 0, time.UTC); !run.Equal(want) {
		t.Fatalf("next run = %s, want %s", run, want)
	}
}

func TestRenderCSV(t *testing.T) {
	rows := []Row{
		{Bucket: "2026-03-01", Label: "Main", Value: 12},
		{Bucket: "2026-03-01", Label: "Service, west", Value: 3.5},
	}
	body, err := renderCSV([]string{"Day", "Gate", "Movements"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"Day,Gate,Movements\n", "2026-03-01,Main,12\n", `"Service, west",3.50`} {
		if !strings.Contains(got, want) {
			t.Fatalf("csv missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderXLSX(t *testing.T) {
	body, err := renderXLSX([]string{"Day", "Gate", "Movements"}, []Row{
		{Bucket: "2026-03-01", Label: "Main & service", Value: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("xlsx is not a readable zip: %v", err)
	}
	parts := map[string]string{}
	for _, file := range archive.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[file.Name] = string(content)
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml",
		"xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"} {
		if parts[name] == "" {
			t.Fatalf("xlsx is missing %s", name)
		}
	}
	sheet := parts["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sheet, "Main &amp; service") {
		t.Fatalf("sheet did not escape its text:\n%s", sheet)
	}
	if !strings.Contains(sheet, "<v>12</v>") {
		t.Fatalf("sheet did not write the metric as a number:\n%s", sheet)
	}
}

func TestExportName(t *testing.T) {
	name := exportName("visitor_log", time.Date(2026, 3, 4, 23, 0, 0, 0, time.UTC), FormatXLSX)
	if name != "visitor_log-20260304.xlsx" {
		t.Fatalf("export name = %q", name)
	}
}
