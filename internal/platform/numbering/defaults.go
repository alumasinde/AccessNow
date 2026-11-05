package numbering

import (
	"context"
	"fmt"

	"accessnow/internal/platform/db"
)

// DefaultFormatKey is the format a module gets when it does not name one.
const DefaultFormatKey = "default"

// starter is what a tenant holds before an admin edits anything: one catch-all plus one
// per category, so a visitor badge and a returnable goods pass never share a series.
// Material is the only gap_required shape, because an auditor must be able to count the
// goods passes with no holes in the sequence.
var starter = []Format{
	{FormatKey: DefaultFormatKey, Name: "Default pass number", Pattern: "{PREFIX}{YYYY}{SEQ}",
		Prefix: ptr("PASS-"), SequenceWidth: 5, Separator: "-", ResetPeriod: "yearly"},
	{FormatKey: "visitor", Name: "Visitor pass", Pattern: "{PREFIX}{YY}{MM}{SEQ}",
		Prefix: ptr("VIS-"), CategoryKey: ptr("visitor"), ModuleKey: ptr("visitor_management"),
		SequenceWidth: 4, Separator: "-", ResetPeriod: "monthly"},
	{FormatKey: "vehicle", Name: "Vehicle pass", Pattern: "{PREFIX}{YYYY}{SEQ}",
		Prefix: ptr("VH-"), CategoryKey: ptr("vehicle"), ModuleKey: ptr("vehicle_tracking"),
		SequenceWidth: 5, Separator: "-", ResetPeriod: "yearly"},
	{FormatKey: "material", Name: "Goods pass", Pattern: "{PREFIX}{YYYY}-{SEQ}",
		Prefix: ptr("MP-"), CategoryKey: ptr("material"), ModuleKey: ptr("material_logistics"),
		SequenceWidth: 5, Separator: "-", ResetPeriod: "yearly", GapRequired: true},
	{FormatKey: "staff", Name: "Staff outpass", Pattern: "{PREFIX}{YY}{MM}{SEQ}",
		Prefix: ptr("OP-"), CategoryKey: ptr("staff"), ModuleKey: ptr("staff_outpass"),
		SequenceWidth: 4, Separator: "-", ResetPeriod: "monthly"},
	{FormatKey: "contractor", Name: "Contractor pass", Pattern: "{PREFIX}{YYYY}{SEQ}",
		Prefix: ptr("CTR-"), CategoryKey: ptr("contractor"), ModuleKey: ptr("contractor_access"),
		SequenceWidth: 4, Separator: "-", ResetPeriod: "yearly"},
	{FormatKey: "service", Name: "Service visit", Pattern: "{PREFIX}{YY}{SEQ}",
		Prefix: ptr("SVC-"), CategoryKey: ptr("service"), ModuleKey: ptr("contractor_access"),
		SequenceWidth: 4, Separator: "-", ResetPeriod: "yearly"},
	{FormatKey: "emergency", Name: "Emergency access", Pattern: "{PREFIX}{YY}{MM}{SEQ}",
		Prefix: ptr("EMG-"), CategoryKey: ptr("emergency"), ModuleKey: ptr("emergency_rollcall"),
		SequenceWidth: 4, Separator: "-", ResetPeriod: "monthly"},
}

// EnsureDefaults gives a new tenant a working numbering scheme. It only writes keys that
// are missing, so running it again never rolls back a format the tenant has edited.
func EnsureDefaults(ctx context.Context, c db.Conn, tenantID int64) error {
	repo := NewRepository(c)
	have, err := repo.keys(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, format := range starter {
		if have[format.FormatKey] {
			continue
		}
		format.TenantID = tenantID
		if err := repo.SaveFormat(ctx, format); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) keys(ctx context.Context, tenantID int64) (map[string]bool, error) {
	const query = `SELECT format_key FROM pass_number_formats WHERE tenant_id = ?`
	var keys []string
	if err := r.q.SelectContext(ctx, &keys, query, tenantID); err != nil {
		return nil, fmt.Errorf("read pass number formats: %w", err)
	}
	known := make(map[string]bool, len(keys))
	for _, key := range keys {
		known[key] = true
	}
	return known, nil
}

func ptr(value string) *string { return &value }
