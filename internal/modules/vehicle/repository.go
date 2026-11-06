package vehicle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

// In rebinds the repository to an open transaction, keeping one method set.
func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const vehicleColumns = `id, tenant_id, plate_normalized, plate_display, country_code, make,
	model, color, vehicle_class, body_type, is_fleet, owner_type, owner_user_id,
	owner_staff_id, owner_label, owner_ref_id, insurance_expiry, fitness_expiry,
	permit_required, default_dwell_limit_minutes, status, notes, external_ref,
	created_at, updated_at`

// The desk reads a journey with the Core row behind it, so the join is on the composite
// key and a pass from another tenant can never appear. entry_count comes along because it
// is what says whether this plate may come back in.
const passColumns = `vp.id, vp.tenant_id, vp.pass_id, vp.vehicle_id, vp.plate_normalized,
	vp.plate_display, vp.is_fleet_snapshot, vp.driver_name, vp.driver_phone,
	vp.driver_id_type, vp.driver_staff_id, vp.escort_name, vp.purpose, vp.cargo_description,
	vp.container_no, vp.seal_no, vp.expected_entry_at, vp.expected_exit_at, vp.entered_at,
	vp.exited_at, vp.entry_gate_id, vp.exit_gate_id, vp.dwell_limit_minutes,
	vp.is_overstaying, vp.overstay_flagged_at, vp.actual_dwell_minutes, vp.status, vp.notes,
	vp.created_at, p.pass_no, p.status AS pass_status, p.entry_count`

const passFrom = `
	FROM   vehicle_passes vp
	JOIN   passes p ON p.id = vp.pass_id AND p.tenant_id = vp.tenant_id`

// Vehicles.

func (r *Repository) VehicleByPlate(ctx context.Context, tenantID int64, plateNormalized string) (Vehicle, error) {
	return r.readVehicle(ctx, ` WHERE tenant_id = ? AND plate_normalized = ? AND deleted_at IS NULL`,
		tenantID, plateNormalized)
}

func (r *Repository) VehicleByID(ctx context.Context, tenantID, id int64) (Vehicle, error) {
	return r.readVehicle(ctx, ` WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`, id, tenantID)
}

func (r *Repository) readVehicle(ctx context.Context, where string, args ...any) (Vehicle, error) {
	query := `SELECT ` + vehicleColumns + ` FROM vehicles` + where
	var v Vehicle
	if err := r.q.GetContext(ctx, &v, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Vehicle{}, errs.NotFound("vehicle")
		}
		return Vehicle{}, fmt.Errorf("read vehicle: %w", err)
	}
	return v, nil
}

// CreateVehicle registers a plate. uk_vehicles_plate is the only thing standing between
// two operators filing the same lorry twice, so the caller turns a duplicate into a
// conflict rather than checking first.
func (r *Repository) CreateVehicle(ctx context.Context, in NewVehicle) (int64, error) {
	const query = `
		INSERT INTO vehicles
			(tenant_id, plate_normalized, plate_display, country_code, make, model, color,
			 vehicle_class, body_type, is_fleet, owner_type, owner_user_id, owner_staff_id,
			 owner_label, insurance_expiry, fitness_expiry, permit_required,
			 default_dwell_limit_minutes, status, notes, external_ref, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	class := in.Class
	if class == "" {
		class = defaultClass
	}
	ownerType := in.OwnerType
	if ownerType == "" {
		ownerType = OwnerUnknown
	}
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.PlateNormalized, in.PlateDisplay, text(in.CountryCode), text(in.Make),
		text(in.Model), text(in.Color), class, text(in.BodyType), boolInt(in.Fleet), ownerType,
		nullable(in.OwnerUserID), nullable(in.OwnerStaffID), text(in.OwnerLabel),
		nullableDate(in.InsuranceExpiry), nullableDate(in.FitnessExpiry),
		boolInt(in.PermitRequired), in.DefaultDwellLimitMinutes, VehicleActive,
		text(in.Notes), text(in.ExternalRef), nullable(in.CreatedBy))
	if err != nil {
		return 0, fmt.Errorf("register vehicle: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read vehicle id: %w", err)
	}
	return id, nil
}

// UpdateVehicle writes the paperwork fields an admin keeps up with. The plate is not in
// this set: a plate that was typed wrong is a new registration, not an edit, because the
// passes already point at the old row.
func (r *Repository) UpdateVehicle(ctx context.Context, tenantID, id int64, in UpdateVehicle) error {
	set := []string{"updated_at = UTC_TIMESTAMP(6)"}
	args := []any{}
	add := func(column string, value any) {
		set = append(set, column+" = ?")
		args = append(args, value)
	}
	if in.Make != nil {
		add("make", *in.Make)
	}
	if in.Model != nil {
		add("model", *in.Model)
	}
	if in.Color != nil {
		add("color", *in.Color)
	}
	if in.Class != nil {
		add("vehicle_class", *in.Class)
	}
	if in.BodyType != nil {
		add("body_type", *in.BodyType)
	}
	if in.Fleet != nil {
		add("is_fleet", boolInt(*in.Fleet))
	}
	if in.OwnerType != nil {
		add("owner_type", *in.OwnerType)
	}
	if in.OwnerLabel != nil {
		add("owner_label", *in.OwnerLabel)
	}
	if in.InsuranceExpiry != nil {
		add("insurance_expiry", nullableDate(in.InsuranceExpiry))
	}
	if in.FitnessExpiry != nil {
		add("fitness_expiry", nullableDate(in.FitnessExpiry))
	}
	if in.PermitRequired != nil {
		add("permit_required", boolInt(*in.PermitRequired))
	}
	if in.DefaultDwellLimitMinutes != nil {
		add("default_dwell_limit_minutes", *in.DefaultDwellLimitMinutes)
	}
	if in.Status != nil {
		add("status", *in.Status)
	}
	if in.Notes != nil {
		add("notes", *in.Notes)
	}

	args = append(args, id, tenantID)
	query := `UPDATE vehicles SET ` + strings.Join(set, ", ") +
		` WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`
	if _, err := r.q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update vehicle: %w", err)
	}
	return nil
}

// ListVehicleFilter is the fleet register. Zero values do not filter.
type ListVehicleFilter struct {
	TenantID int64
	Status   string
	Class    string
	Fleet    *bool
	Search   string
	Limit    int
	Before   *httpx.Cursor
}

func (r *Repository) ListVehicles(ctx context.Context, f ListVehicleFilter) ([]Vehicle, error) {
	where := []string{"tenant_id = ?", "deleted_at IS NULL"}
	args := []any{f.TenantID}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Class != "" {
		where = append(where, "vehicle_class = ?")
		args = append(args, f.Class)
	}
	if f.Fleet != nil {
		where = append(where, "is_fleet = ?")
		args = append(args, boolInt(*f.Fleet))
	}
	if f.Search != "" {
		where = append(where, `(plate_normalized LIKE ? OR plate_display LIKE ?
			OR make LIKE ? OR model LIKE ? OR owner_label LIKE ?)`)
		for range 5 {
			args = append(args, like(f.Search))
		}
	}
	if f.Before != nil {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}

	query := `SELECT ` + vehicleColumns + ` FROM vehicles WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY created_at DESC, id DESC LIMIT ?`
	rows := []Vehicle{}
	if err := r.q.SelectContext(ctx, &rows, query, append(args, f.Limit)...); err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	return rows, nil
}

// Passes.

func (r *Repository) CreatePass(ctx context.Context, in NewPass) (int64, error) {
	const query = `
		INSERT INTO vehicle_passes
			(tenant_id, pass_id, vehicle_id, plate_normalized, plate_display,
			 is_fleet_snapshot, driver_name, driver_phone, driver_id_type, driver_staff_id,
			 escort_name, purpose, cargo_description, container_no, seal_no,
			 expected_entry_at, expected_exit_at, dwell_limit_minutes, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.PassID, nullable(in.VehicleID), in.PlateNorm, text(in.PlateDisplay),
		boolInt(in.FleetSnapshot), text(in.DriverName), text(in.DriverPhone),
		text(in.DriverIDType), nullable(in.DriverStaffID), text(in.EscortName), text(in.Purpose),
		text(in.Cargo), text(in.ContainerNo), text(in.SealNo), nullableTime(in.ExpectedEntry),
		nullableTime(in.ExpectedExit), in.DwellLimit, string(in.Status), text(in.Notes))
	if err != nil {
		return 0, fmt.Errorf("create vehicle pass: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read vehicle pass id: %w", err)
	}
	return id, nil
}

func (r *Repository) PassByID(ctx context.Context, tenantID, id int64) (Pass, error) {
	return r.readPass(ctx, ` WHERE vp.id = ? AND vp.tenant_id = ?`, id, tenantID)
}

// PassByPass is how the gate reaction finds its row: the movement knows the pass, and only
// this module owns a vehicle journey for it.
func (r *Repository) PassByPass(ctx context.Context, tenantID, passID int64) (Pass, error) {
	return r.readPass(ctx, ` WHERE vp.pass_id = ? AND vp.tenant_id = ?`, passID, tenantID)
}

func (r *Repository) readPass(ctx context.Context, where string, args ...any) (Pass, error) {
	query := `SELECT ` + passColumns + passFrom + where
	var p Pass
	if err := r.q.GetContext(ctx, &p, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Pass{}, errs.NotFound("vehicle pass")
		}
		return Pass{}, fmt.Errorf("read vehicle pass: %w", err)
	}
	return p, nil
}

func (r *Repository) MarkStatus(ctx context.Context, tenantID, id int64, from, to Status) (bool, error) {
	const query = `UPDATE vehicle_passes SET status = ? WHERE id = ? AND tenant_id = ? AND status = ?`
	result, err := r.q.ExecContext(ctx, query, string(to), id, tenantID, string(from))
	if err != nil {
		return false, fmt.Errorf("update vehicle pass status: %w", err)
	}
	return changed(result)
}

// MarkEntered stamps the journey as live. It accepts an approved pass and a completed one
// alike: a fleet vehicle with entries left in its allowance comes back through the barrier
// on the same pass, and this row tracks the journey it is on now while gate_movements keeps
// every one of them.
//
// The result says whether the row moved. A relayed event may be delivered twice, and a
// journey that is already on site has nothing left to record, so the caller must not
// publish a second "entered" for it.
func (r *Repository) MarkEntered(ctx context.Context, tenantID, id, gateID int64, from []Status) (bool, error) {
	query := `
		UPDATE vehicle_passes
		SET    status = ?, entered_at = UTC_TIMESTAMP(6), entry_gate_id = ?,
		       exited_at = NULL, exit_gate_id = NULL, actual_dwell_minutes = NULL,
		       is_overstaying = 0, overstay_flagged_at = NULL
		WHERE  id = ? AND tenant_id = ? AND status IN (` +
		strings.TrimSuffix(strings.Repeat("?, ", len(from)), ", ") + `)`
	args := append([]any{string(StatusOnSite), nullable(gateID), id, tenantID}, statuses(from)...)
	result, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("mark vehicle entered: %w", err)
	}
	return changed(result)
}

// MarkExited closes the journey: the exit gate, the dwell the vehicle actually used, and
// whether it went past the limit it was given. Like MarkEntered it reports the move, so a
// replayed scan cannot notify twice.
func (r *Repository) MarkExited(ctx context.Context, tenantID, id, gateID int64,
	dwellMinutes int, overstayed bool) (bool, error) {
	const query = `
		UPDATE vehicle_passes
		SET    status = ?, exited_at = UTC_TIMESTAMP(6), exit_gate_id = ?,
		       actual_dwell_minutes = ?, is_overstaying = ?,
		       overstay_flagged_at = CASE WHEN ? THEN UTC_TIMESTAMP(6) ELSE overstay_flagged_at END
		WHERE  id = ? AND tenant_id = ? AND status IN (?, ?)`
	result, err := r.q.ExecContext(ctx, query, string(StatusCompleted), nullable(gateID),
		dwellMinutes, boolInt(overstayed), boolInt(overstayed), id, tenantID,
		string(StatusOnSite), string(StatusOverdue))
	if err != nil {
		return false, fmt.Errorf("mark vehicle exited: %w", err)
	}
	return changed(result)
}

// ListPassFilter is the vehicle register at the desk. Zero values do not filter.
type ListPassFilter struct {
	TenantID  int64
	Status    string
	VehicleID int64
	Plate     string
	Search    string
	Limit     int
	Before    *httpx.Cursor
}

func (r *Repository) ListPasses(ctx context.Context, f ListPassFilter) ([]Pass, error) {
	where := []string{"vp.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.Status != "" {
		where = append(where, "vp.status = ?")
		args = append(args, f.Status)
	}
	if f.VehicleID > 0 {
		where = append(where, "vp.vehicle_id = ?")
		args = append(args, f.VehicleID)
	}
	if f.Plate != "" {
		where = append(where, "vp.plate_normalized = ?")
		args = append(args, f.Plate)
	}
	if f.Search != "" {
		where = append(where, `(p.pass_no LIKE ? OR vp.plate_normalized LIKE ?
			OR vp.plate_display LIKE ? OR vp.driver_name LIKE ?)`)
		for range 4 {
			args = append(args, like(f.Search))
		}
	}
	if f.Before != nil {
		where = append(where, "(vp.created_at < ? OR (vp.created_at = ? AND vp.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}

	query := `SELECT ` + passColumns + passFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY vp.created_at DESC, vp.id DESC LIMIT ?`
	rows := []Pass{}
	if err := r.q.SelectContext(ctx, &rows, query, append(args, f.Limit)...); err != nil {
		return nil, fmt.Errorf("list vehicle passes: %w", err)
	}
	return rows, nil
}

// OnSite is the roster. The view already limits it to the journeys that are live and does
// the dwell arithmetic, so nothing here computes a second answer that could disagree.
func (r *Repository) OnSite(ctx context.Context, tenantID int64, limit int) ([]OnSiteRow, error) {
	const query = `
		SELECT id, pass_id, pass_no, plate_normalized, plate_display, driver_name, purpose,
		       entered_at, dwell_limit_minutes, is_overstaying, current_dwell_minutes
		FROM   v_vehicles_on_site
		WHERE  tenant_id = ?
		ORDER  BY entered_at
		LIMIT  ?`
	rows := []OnSiteRow{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, limit); err != nil {
		return nil, fmt.Errorf("read vehicles on site: %w", err)
	}
	return rows, nil
}

// statuses turns the filter values into bind arguments for an IN list.
func statuses(from []Status) []any {
	args := make([]any, 0, len(from))
	for _, status := range from {
		args = append(args, string(status))
	}
	return args
}

// changed reads a compare-and-set update. Zero rows means the row had already moved, which
// is how a handler tells its own first delivery from the relay's second one.
func changed(result sql.Result) (bool, error) { return db.RowsChanged(result) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func like(term string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
}

func text(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullable(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

// nullableDate is nullableTime for the register's expiry columns, which are DATE. The
// driver sends the date part only, which is what the column wants.
func nullableDate(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format("2006-01-02")
}
