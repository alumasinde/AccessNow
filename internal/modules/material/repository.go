package material

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

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

// The desk always reads a goods pass with the Core row behind it, so the join is on the
// composite key and a pass from another tenant can never appear.
const materialColumns = `m.id, m.tenant_id, m.pass_id, m.kind, m.return_type, m.item_category,
	m.declared_by_name, m.declared_by_staff_id, m.carrier_name, m.carrier_phone, m.driver_name,
	m.vehicle_plate, m.waybill_ref, m.invoice_ref, m.po_ref, m.origin_reference,
	m.destination_reference, m.destination_contact, m.gate_verification_required, m.seal_no,
	m.expected_return_at, m.released_at, m.released_gate_id, m.closed_at, m.verified_at,
	m.status, m.notes, m.created_at, p.pass_no, p.status AS pass_status,
	(SELECT COUNT(*) FROM material_items li
	 WHERE li.tenant_id = m.tenant_id AND li.material_pass_id = m.id) AS line_count`

const materialFrom = `
	FROM   material_passes m
	JOIN   passes p ON p.id = m.pass_id AND p.tenant_id = m.tenant_id`

const itemColumns = `id, material_pass_id, line_no, sku, asset_tag, serial_no, item_name,
	description, category, unit, quantity_declared, quantity_in, quantity_out,
	quantity_returned, unit_value, line_value, currency, returnable, expected_return_at,
	returned_at, overdue_since, status, notes`

func (r *Repository) CreateMaterial(ctx context.Context, in NewMaterial) (int64, error) {
	const query = `
		INSERT INTO material_passes
			(tenant_id, pass_id, kind, return_type, item_category, declared_by_name,
			 declared_by_staff_id, carrier_name, carrier_phone, driver_name, vehicle_plate,
			 waybill_ref, invoice_ref, po_ref, origin_reference, destination_reference,
			 destination_contact, gate_verification_required, seal_no, expected_return_at,
			 status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.PassID, in.Kind, in.ReturnType, text(in.ItemCategory),
		text(in.DeclaredByName), nullable(in.DeclaredByStaffID), text(in.CarrierName),
		text(in.CarrierPhone), text(in.DriverName), text(in.VehiclePlate), text(in.WaybillRef),
		text(in.InvoiceRef), text(in.PORef), text(in.OriginReference), text(in.DestinationReference),
		text(in.DestinationContact), boolInt(in.VerificationRequired), text(in.SealNo),
		nullableTime(in.ExpectedReturnAt), string(in.Status), text(in.Notes))
	if err != nil {
		return 0, fmt.Errorf("create goods pass: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read goods pass id: %w", err)
	}
	return id, nil
}

// InsertItems writes the declared lines and their first event, one statement each.
// line_no is the caller's order, so the desk sees the same numbering it typed.
func (r *Repository) InsertItems(ctx context.Context, tenantID, materialID, actorUserID int64, items []NewItem) error {
	const insert = `
		INSERT INTO material_items
			(tenant_id, material_pass_id, line_no, sku, asset_tag, serial_no, item_name,
			 description, category, unit, quantity_declared, unit_value, currency,
			 returnable, expected_return_at, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'declared', ?)`
	const event = `
		INSERT INTO material_item_events
			(tenant_id, material_pass_id, material_item_id, event, quantity, actor_user_id)
		VALUES (?, ?, ?, 'declared', ?, ?)`
	for index, item := range items {
		result, err := r.q.ExecContext(ctx, insert,
			tenantID, materialID, index+1, text(item.SKU), text(item.AssetTag),
			text(item.SerialNo), item.ItemName, text(item.Description), text(item.Category),
			unit(item.Unit), item.Quantity, item.UnitValue, text(item.Currency),
			boolInt(item.Returnable), nullableTime(item.ExpectedReturnAt), text(item.Notes))
		if err != nil {
			return fmt.Errorf("declare goods line %d: %w", index+1, err)
		}
		itemID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read goods line id: %w", err)
		}
		if _, err := r.q.ExecContext(ctx, event, tenantID, materialID, itemID, item.Quantity,
			nullable(actorUserID)); err != nil {
			return fmt.Errorf("record declared line %d: %w", index+1, err)
		}
	}
	return nil
}

func (r *Repository) MaterialByID(ctx context.Context, tenantID, id int64) (Material, error) {
	return r.read(ctx, ` WHERE m.id = ? AND m.tenant_id = ?`, id, tenantID)
}

// MaterialByPass is how the gate reaction finds its row: the movement knows the pass, and
// only this module owns a goods declaration for it.
func (r *Repository) MaterialByPass(ctx context.Context, tenantID, passID int64) (Material, error) {
	return r.read(ctx, ` WHERE m.pass_id = ? AND m.tenant_id = ?`, passID, tenantID)
}

func (r *Repository) read(ctx context.Context, where string, args ...any) (Material, error) {
	query := `SELECT ` + materialColumns + materialFrom + where
	var m Material
	if err := r.q.GetContext(ctx, &m, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Material{}, errs.NotFound("goods pass")
		}
		return Material{}, fmt.Errorf("read goods pass: %w", err)
	}
	return m, nil
}

func (r *Repository) Items(ctx context.Context, tenantID, materialID int64) ([]Item, error) {
	const query = `SELECT ` + itemColumns + `
		FROM material_items WHERE tenant_id = ? AND material_pass_id = ? ORDER BY line_no`
	var items []Item
	if err := r.q.SelectContext(ctx, &items, query, tenantID, materialID); err != nil {
		return nil, fmt.Errorf("read goods lines: %w", err)
	}
	return items, nil
}

// ListFilter is the goods register. Zero values do not filter.
type ListFilter struct {
	TenantID   int64
	Kind       string
	ReturnType string
	Status     string
	Search     string
	Limit      int
	Before     *httpx.Cursor
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]Material, error) {
	where := []string{"m.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.Kind != "" {
		where = append(where, "m.kind = ?")
		args = append(args, f.Kind)
	}
	if f.ReturnType != "" {
		where = append(where, "m.return_type = ?")
		args = append(args, f.ReturnType)
	}
	if f.Status != "" {
		where = append(where, "m.status = ?")
		args = append(args, f.Status)
	}
	if f.Search != "" {
		where = append(where, `(p.pass_no LIKE ? OR m.declared_by_name LIKE ? OR m.vehicle_plate LIKE ?)`)
		args = append(args, like(f.Search), like(f.Search), like(f.Search))
	}
	if f.Before != nil {
		where = append(where, "(m.created_at < ? OR (m.created_at = ? AND m.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}

	query := `SELECT ` + materialColumns + materialFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY m.created_at DESC, m.id DESC LIMIT ?`
	var rows []Material
	if err := r.q.SelectContext(ctx, &rows, query, append(args, f.Limit)...); err != nil {
		return nil, fmt.Errorf("list goods passes: %w", err)
	}
	return rows, nil
}

// Outstanding reads the dashboard's overdue list. The view already drops lines that came
// back, so this is the whole answer and nothing has to be computed in Go.
func (r *Repository) Outstanding(ctx context.Context, tenantID int64, limit int) ([]OutstandingRow, error) {
	const query = `
		SELECT material_pass_id, pass_id, pass_no, material_item_id, item_name, serial_no,
		       quantity_outstanding, expected_return_at, overdue_since, status, declared_by_name
		FROM   v_material_returns_outstanding
		WHERE  tenant_id = ?
		ORDER  BY COALESCE(expected_return_at, '9999-12-31'), material_item_id
		LIMIT  ?`
	// An empty page answers with [] rather than null: a dashboard that iterates the
	// list should never have to know that Go distinguishes the two.
	rows := []OutstandingRow{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, limit); err != nil {
		return nil, fmt.Errorf("read outstanding goods: %w", err)
	}
	return rows, nil
}

func (r *Repository) exec(ctx context.Context, query string, args ...any) error {
	if _, err := r.q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update goods pass: %w", err)
	}
	return nil
}

// MarkStatus is a compare and set on the status the caller last read, so two guards at
// two gates cannot both release one lorry and each believe they were first.
func (r *Repository) MarkStatus(ctx context.Context, tenantID, id int64, from, to Status) error {
	return r.exec(ctx, `UPDATE material_passes SET status = ? WHERE id = ? AND tenant_id = ? AND status = ?`,
		string(to), id, tenantID, string(from))
}

// MarkReleased stamps the gate that let the goods through. The status guard makes a
// replayed movement event a no-op instead of a second release.
func (r *Repository) MarkReleased(ctx context.Context, tenantID, id, gateID int64, from Status) error {
	return r.exec(ctx, `
		UPDATE material_passes
		SET    status = ?, released_at = UTC_TIMESTAMP(6), released_gate_id = ?
		WHERE  id = ? AND tenant_id = ? AND status = ? AND released_at IS NULL`,
		string(StatusReleased), nullable(gateID), id, tenantID, string(from))
}

func (r *Repository) MarkClosed(ctx context.Context, tenantID, id int64, to Status, userID int64) error {
	return r.exec(ctx, `
		UPDATE material_passes
		SET    status = ?, closed_at = UTC_TIMESTAMP(6), verified_at = UTC_TIMESTAMP(6),
		       verified_by_user_id = ?
		WHERE  id = ? AND tenant_id = ? AND closed_at IS NULL`,
		string(to), nullable(userID), id, tenantID)
}

// overdueCondition is the sweep's whole question, and it is asked of the database clock so
// that a cron job running on another machine cannot disagree with the register it writes.
// A pass that has already come back is not in it, which is what keeps a late return from
// being reported as a debt afterwards.
const overdueCondition = `m.status IN ('released', 'partially_returned')
	AND m.expected_return_at IS NOT NULL AND m.expected_return_at <= UTC_TIMESTAMP(6)`

// DueOverdue is the rows a sweep owes a notice for. Escalate is the only writer of the
// pass's overdue status, and reading first is what lets each declaration get its own event
// instead of one blunt notice per site.
func (r *Repository) DueOverdue(ctx context.Context, tenantID int64) ([]Material, error) {
	query := `SELECT ` + materialColumns + materialFrom +
		` WHERE m.tenant_id = ? AND (` + overdueCondition + `)`
	rows := []Material{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID); err != nil {
		return nil, fmt.Errorf("read goods passes due back: %w", err)
	}
	return rows, nil
}

// MarkOverdue is the sweep's compare-and-set on the status it read, so a second run in the
// same hour moves nothing and publishes nothing.
func (r *Repository) MarkOverdue(ctx context.Context, tenantID, id int64, from Status) (bool, error) {
	return r.move(ctx, `UPDATE material_passes SET status = ?
		WHERE id = ? AND tenant_id = ? AND status = ?`, string(StatusOverdue), id, tenantID,
		string(from))
}

// MarkDisputed is the desk saying the declaration itself is wrong. It is reached from every
// open state, including overdue: the commonest reason a load is disputed is that somebody
// has been told it is owed back.
func (r *Repository) MarkDisputed(ctx context.Context, tenantID, id int64, from Status) (bool, error) {
	return r.move(ctx, `UPDATE material_passes SET status = ?
		WHERE id = ? AND tenant_id = ? AND status = ?`, string(StatusDisputed), id, tenantID,
		string(from))
}

// MarkLinesOverdue stamps the individual lines the load is owed on and reports how many it
// took. A line that carries its own date is judged by it, because a lorry of six things with
// three return dates is not one debt; a line with no date of its own is judged by the pass,
// which is why the sweep only ever calls this for a pass it has just found to be due.
func (r *Repository) MarkLinesOverdue(ctx context.Context, tenantID, materialID int64) (int, error) {
	const query = `
		UPDATE material_items
		SET    overdue_since = COALESCE(overdue_since, UTC_TIMESTAMP(6)), status = ?
		WHERE  tenant_id = ? AND material_pass_id = ? AND returnable = 1
		  AND  status IN ('` + ItemReleased + `', '` + ItemPartial + `')
		  AND  (expected_return_at IS NULL OR expected_return_at <= UTC_TIMESTAMP(6))`
	result, err := r.q.ExecContext(ctx, query, ItemOverdue, tenantID, materialID)
	if err != nil {
		return 0, fmt.Errorf("mark lines overdue: %w", err)
	}
	n, err := result.RowsAffected()
	return int(n), err
}

func (r *Repository) move(ctx context.Context, query string, args ...any) (bool, error) {
	result, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update goods pass: %w", err)
	}
	return db.RowsChanged(result)
}

// ReleaseLines moves every declared line across the barrier at once: outward goods are
// counted as released, inward goods as received. Either way what actually moved is
// recorded separately from what the desk declared.
func (r *Repository) ReleaseLines(ctx context.Context, tenantID, materialID int64, outward bool) error {
	column := "quantity_in"
	if outward {
		column = "quantity_out"
	}
	query := `
		UPDATE material_items
		SET    ` + column + ` = quantity_declared, status = ?
		WHERE  tenant_id = ? AND material_pass_id = ? AND status = '` + ItemDeclared + `'`
	return r.exec(ctx, query, ItemReleased, tenantID, materialID)
}

func (r *Repository) RecordLineEvent(ctx context.Context, tenantID, materialID, itemID int64,
	event string, quantity float64, gateID, movementID, actorUserID int64) error {
	const query = `
		INSERT INTO material_item_events
			(tenant_id, material_pass_id, material_item_id, event, quantity, gate_id,
			 movement_id, actor_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	return r.exec(ctx, query, tenantID, materialID, itemID, event, quantity,
		nullable(gateID), nullable(movementID), nullable(actorUserID))
}

// ReturnLine adds one return against a line and moves it to returned when the whole
// outstanding quantity is back. Partial returns are the normal case, so the caller does
// the arithmetic and this only ever adds.
//
// MySQL evaluates a SET list left to right, so the CASE below sees quantity_returned
// already increased. That is deliberate: it is what makes one statement enough.
func (r *Repository) ReturnLine(ctx context.Context, tenantID, itemID int64, quantity float64) error {
	return r.exec(ctx, `
		UPDATE material_items
		SET    quantity_returned = quantity_returned + ?,
		       returned_at = UTC_TIMESTAMP(6),
		       status = CASE WHEN quantity_returned >= quantity_out THEN ? ELSE ? END
		WHERE  id = ? AND tenant_id = ? AND returnable = 1 AND status IN (?, ?)`,
		quantity, ItemReturned, ItemPartial, itemID, tenantID,
		ItemReleased, ItemPartial)
}

// RefreshReturnStatus moves a released outward declaration to partially_returned or
// returned from its own lines, never from what the caller claims. An RGP with nothing left
// outstanding is returned; anything still owed stays partially_returned so the outstanding
// view keeps showing it.
func (r *Repository) RefreshReturnStatus(ctx context.Context, tenantID, materialID int64) error {
	return r.exec(ctx, `
		UPDATE material_passes m
		SET    m.status = CASE WHEN NOT EXISTS (
					SELECT 1 FROM material_items mi
					WHERE  mi.material_pass_id = m.id AND mi.tenant_id = m.tenant_id
					       AND mi.returnable = 1 AND mi.quantity_returned < mi.quantity_out)
			       THEN ? ELSE ? END
		WHERE  m.id = ? AND m.tenant_id = ? AND m.return_type = ?
		       AND m.status IN (?, ?)`,
		string(StatusReturned), string(StatusPartiallyReturned), materialID, tenantID,
		ReturnRGP, string(StatusReleased), string(StatusPartiallyReturned))
}

// LineEvents records the scan against every line that actually crossed. It runs after
// ReleaseLines, so the quantity it reads is the one the gate just counted.
func (r *Repository) LineEvents(ctx context.Context, tenantID, materialID int64, event string,
	outward bool, gateID, movementID, actorUserID int64) error {
	items, err := r.Items(ctx, tenantID, materialID)
	if err != nil {
		return err
	}
	for _, item := range items {
		quantity := item.QuantityOut
		if !outward {
			quantity = item.QuantityIn
		}
		if quantity <= 0 {
			continue
		}
		if err := r.RecordLineEvent(ctx, tenantID, materialID, item.ID, event, quantity,
			gateID, movementID, actorUserID); err != nil {
			return err
		}
	}
	return nil
}

// ReturnEverything is the gate saying the whole load came back: each outstanding line is
// closed at its own outstanding quantity and the declaration is re-evaluated from the
// lines, not from the claim.
func (r *Repository) ReturnEverything(ctx context.Context, tenantID, materialID int64,
	gateID, movementID, actorUserID int64) error {
	items, err := r.Items(ctx, tenantID, materialID)
	if err != nil {
		return err
	}
	for _, item := range items {
		outstanding := item.Outstanding()
		if !item.Returnable || outstanding <= 0 {
			continue
		}
		if err := r.ReturnLine(ctx, tenantID, item.ID, outstanding); err != nil {
			return err
		}
		if err := r.RecordLineEvent(ctx, tenantID, materialID, item.ID, "returned", outstanding,
			gateID, movementID, actorUserID); err != nil {
			return err
		}
	}
	return r.RefreshReturnStatus(ctx, tenantID, materialID)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unit(value string) string {
	if value == "" {
		return "pcs"
	}
	return value
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
