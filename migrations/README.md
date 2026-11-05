# migrations/

Schema for **accessnow**, a multi-tenant gate pass system (MySQL 8 / MariaDB 10, Go backend).
27 numbered migrations, each with an `.up.sql` and `.down.sql` pair: **92 tables, 8 views,
314 foreign keys, 252 CHECK constraints** — counted out of a live MariaDB 12.3, not from the files.

Every `up` file is safe to re-run against a database that already has it applied.

## Requirements

| Engine | Minimum | Why |
| --- | --- | --- |
| MySQL | 8.0.16 | `CHECK` constraints are enforced from 8.0.16 (8.0.13–8.0.15 parse and ignore them) |
| MariaDB | 10.2 | enforced `CHECK`, `DATETIME(6)` defaults, stored generated columns |

Charset is `utf8mb4 / utf8mb4_unicode_ci` everywhere (portable, no `0900_ai_ci` which MariaDB
does not have). All tables are InnoDB with `ROW_FORMAT=DYNAMIC`.

## Run it

```bash
# from this repo. .env is read by config.Load, so no shell sourcing is needed.
go run ./cmd/migrate -direction up
go run ./cmd/migrate -direction up -steps 3      # three migrations, then stop
go run ./cmd/migrate -direction down -steps 1 -allow-destructive
go run ./cmd/migrate -force -direction up        # forget the recorded version and start at 0

# plain golang-migrate against the loose files
migrate -path ./migrations -database "mysql://user:pass@tcp(127.0.0.1:3306)/accessnow" up
```

Two things the tool has to get right, and now does:

- **`multiStatements=true`.** golang-migrate submits a whole file as one statement, so
  `db.OpenMigration` sets it on a connection of its own. The API pool leaves it off: a request
  path that can chain statements is an injection waiting to happen.
- **The advisory lock needs a second connection.** `GET_LOCK` is connection-scoped and is held
  for the whole run, so the migration pool is opened with `MaxOpenConns(2)`. With one connection
  the tool waits on itself and appears to hang with no output.

`-force` clears `schema_migrations` when a file failed partway. MySQL DDL does not roll back, so a
syntax error mid-file leaves the recorded version ahead of the schema, and a plain `up` refuses to
move past it. Use `-force` when nothing actually landed; otherwise fix the file and record the
truth by hand.

```go
// what cmd/migrate does, if you need to drive it from Go
source, err := iofs.New(migrations.FS(), ".")
target, err := mysql.WithInstance(sqlDB, &mysql.Config{
    MigrationsTable: "schema_migrations",
    DatabaseName:    cfg.Database.Name,
})
m, err := migrate.NewWithInstance("iofs", source, "mysql", target)
```

`cmd/migrate` takes a MySQL advisory lock (`GET_LOCK('accessnow:migrate')`) for the whole run, so
two operators or two CI jobs cannot interleave half a migration.

## Grouping

| Range | Group | Owns |
| --- | --- | --- |
| 0001–0009 | **Core** (always on, never sold) | tenants, module flags, sites, gates, users, sessions, RBAC, directory, `passes`, `gate_movements`, events, audit |
| 0010–0014 | Access modules | visitors, vehicles, materials, staff outpass, contractors |
| 0015–0018 | Control modules | QR/hardware scanning, approval workflow, rules, blacklist/watchlist |
| 0019–0021 | Support modules | notifications, offline gate mode, emergency roll-call |
| 0022–0024 | Reporting and governance | reports and scheduled exports, integrations and API, audit and compliance |
| 0025–0026 | Seeds | reference catalogues, permission catalogue and role templates |
| 0027 | Core (numbering) | `pass_number_formats`, `pass_number_counters` |

Core holds the `passes` spine. Each module owns a 1:1 detail row hanging off `pass_id`
(`visits`, `vehicle_passes`, `material_passes`, `outpasses`, `contractor_passes`), so a tenant
can run any single module alone, and a scan writes one `gate_movements` row no matter which
module produced it.

## Design rules in here

**No ENUMs.** Closed sets are `VARCHAR` + `CHECK (col IN (...))`, except where a set is
tenant-editable or grows (statuses, categories), where the value is a `VARCHAR` key into a
lookup table. Adding a state means adding an option to the CHECK, not rewriting a column.

**Tenant safety.** Every tenant-owned table carries `tenant_id`, and the parent tables expose
`UNIQUE KEY (id, tenant_id)` so children use composite foreign keys
`(pass_id, tenant_id) → passes (id, tenant_id)`. The database itself then refuses a row in
tenant A that points at a pass in tenant B. Where a link is optional, the FK is single-column
with `ON DELETE SET NULL`, because a composite `SET NULL` would also null `tenant_id` and MySQL
rejects that at create time.

**Modules never reference each other.** No foreign key crosses from, say, `vehicle_passes` into
`visitors`. Cross-module pointers are untyped `owner_ref_id` / `related_id` columns resolved only
when the other module is on.

**Events, not calls.** Modules write `domain_events` in their own transaction; a worker delivers
them (`movement.recorded`, `material.item_overdue`). `notification_routes` subscribes to those
keys. If the consuming module is off, the event simply has no subscriber.

**Append-only history.** `gate_movements`, `audit_logs`, `rule_evaluations`, `scan_attempts`,
`blocklist_hits` and `roll_call_marks` are insert-only. `gate_movements` rows pair an entry to an
exit through `close_of_movement_id`, which is written on the exit row and never mutated on the
entry — so presence is "no later row points at me". That is what makes dwell time, overstay flags
and the roll-call roster fall out of a query rather than a counter.

**Retention-ready PII.** ID numbers, photos, raw payloads and notification bodies each have a
companion `<col>_purged_at`, so the compliance job can blank a value and still prove when it did.
`audit_logs` stores `seq`, `prev_hash` and `row_hash` from day one, so hash chaining can be
verified without a later schema change.

**Secrets stay out.** `pass_tokens`, `api_keys`, `gate_devices` and `user_sessions` store only
SHA-256 hashes. Provider credentials are `credentials_ref` names pointing at the vault or env.

**Soft delete** (`deleted_at`) on people, places and passes; hard cascade on owned detail rows.
`gate_movements` has no `deleted_at` — void it with a `result='voided'` row instead.

## The gate model

`sites` → `gates` (+ `gate_categories`, `gate_time_windows`, `gate_users`, `gate_devices`).
A tenant with one entrance gets a default gate (`is_default=1`) and never sees a picker.
Roles with `does_gate_work=1` get the picker after login; the chosen gate is stored on
`user_sessions.active_gate_id`, is revalidated against `gate_users` on every request, and opening
it creates a `gate_sessions` row (one open session per user, enforced by a unique generated
column). Switching closes the old session with a `close_reason` and writes an audit row.
`gate_devices.pin_code_hash` supports a fixed tablet whose device token decides the gate, so the
guard never picks one.

## Views

`v_open_movements`, `v_recent_gate_decisions`, `v_vehicles_on_site`,
`v_material_returns_outstanding`, `v_staff_out_of_site`, `v_approval_inbox`,
`v_device_sync_health`, `v_purge_candidates`. All are `CREATE OR REPLACE VIEW`, so they are
re-runnable on their own. `v_open_movements` is the roll-call roster: entry rows no exit points
back at, counting `granted`, `warned` and `override` alike, because all three are someone
physically on site. It projects `direction` and `result` as well, because the repository that
reads an open entry to close it reports both.

## Pass numbering (0027)

`pass_number_formats` is one row per tenant per scope (`default`, `visitor`, `vehicle`,
`material`, …) and `pass_number_counters` is one row per format per period. `internal/platform/numbering`
allocates with a single atomic `INSERT … ON DUPLICATE KEY UPDATE last_value = LAST_INSERT_ID(last_value + step)`
then `SELECT LAST_INSERT_ID()`, so two guards at two gates cannot be handed the same number, and
the claim happens inside the transaction that inserts the pass — a rolled back issue burns no
number on a `gap_required` format.

`gap_required = 1` is only set on the material format: auditors may count goods passes with no
holes, while everything else simply resets. `reset_period` (`none`, `daily`, `monthly`, `yearly`)
picks the counter bucket, which is keyed on the *tenant's* wall clock (`clock.PeriodKey`), not UTC.
`pattern` tokens are `{PREFIX} {CATEGORY} {MODULE} {YYYY} {YY} {MM} {DD} {SEQ}`. A pattern with an
unknown token, or one that renders past the 32-character `pass_no` column, is refused by
`Format.Render` — which runs inside the same transaction as the pass insert, so the number claim
rolls back with it instead of leaving a hole.

Editing a format changes numbers issued from then on; the counter keeps running, so a tenant that
switches `VIS-` to `GV-` mid-month does not restart at 0001.

## Provisioning a new tenant

Roles are strictly tenant-scoped, so 0026 seeds **templates**, not live roles. On signup, in one
transaction: insert the tenant, copy `role_templates` into `roles` (set `template_role_key`,
`is_system=1`), copy `role_template_permissions` into `role_permissions`, insert `tenant_modules`
from `modules.default_enabled`, create the default site and its default gate, then create the
first admin user and attach the `tenant_admin` role.

## Adding migration 0028 and later

This folder deliberately contains no `ALTER TABLE`, because a bare `ALTER ... ADD COLUMN` errors on
a re-run. When you must change an existing table, guard it. Works on MySQL and MariaDB and needs
no `DELIMITER`, which matters because golang-migrate sends the whole file to the server as one
multi-statement query and cannot understand a client-side delimiter:

```sql
SET @sql = IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
      WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'passes' AND COLUMN_NAME = 'risk_score') = 0,
    'ALTER TABLE passes ADD COLUMN risk_score TINYINT UNSIGNED NULL AFTER value_currency',
    'SELECT ''already applied''');
PREPARE s FROM @sql; EXECUTE s; DEALLOCATE PREPARE s;
```

Same shape for indexes (`information_schema.STATISTICS`) and constraints
(`information_schema.TABLE_CONSTRAINTS`).

Rules for a new file:
1. copy the highest number plus one, keep the `NNNN_group_thing.up.sql` shape
2. `CREATE TABLE IF NOT EXISTS`, and declare every column, index and constraint in that one
   statement — re-running then changes nothing at all
3. never reference a table created in a later file; `tenant_id` plus `UNIQUE (id, tenant_id)` on
   the parent if a child needs the composite
4. write the `.down.sql` as drops in the exact reverse of the create order
5. seeds use `INSERT ... ON DUPLICATE KEY UPDATE`; keep semicolons out of string literals
6. no `ENUM`, no cross-module FK, no plaintext secret

## Down files

`.down.sql` drops what the matching `.up.sql` created, reverse-ordered. It is real but
**destructive** — down from 0026 wipes operational data. Treat `migrate down` as a development
tool only, and never run it against a live tenant.

## Checked before shipping

Static pass: every file parsed for up/down pairing and drop order, forward references, FK target
key coverage, FK column type width match, `SET NULL` against `NOT NULL` columns, `ENUM` use, and
non-idempotent statements in `up`. Seven composite `SET NULL` foreign keys were found that would
have nullated a `NOT NULL tenant_id`, and one `approval_requests` parent was missing the
`UNIQUE (id, tenant_id)` its children point at. All fixed.
Result of that static pass: `tables=89 views=8 foreignKeys=309 checkConstraints=210 indexes=310`,
no problems. The live run below counts 92 / 8 / 314 / 252 — the extra tables and constraints are
0027 and the numbering checks.

Live pass on MariaDB 12.3, throwaway schema, every statement run through the real server:

- each `up` applied **twice in a row**, each `down` run **twice in a row** → 89 tables, 8 views,
  309 FKs, 248 check constraints, seeds land as 16 modules / 14 pass types / 85 permissions /
  11 role templates / 336 template grants; all 8 views readable; full rollback leaves
  `remaining_objects=0`.
- Two things only a server catches: MariaDB rejects a generated column that reads a column
  governed by an `ON UPDATE CASCADE` FK (errno 1901), so `notification_routes` and
  `webhook_deliveries` use plain composite unique keys instead.
- A gate-shift data walk (tenant → site → gate → guard → session → pass → token → scan → exit)
  proved the guards actually bite: cross-tenant `pass_id` → ERROR 1452, a status outside the
  CHECK list → ERROR 4025 `chk_passes_status`, a second open gate session for one guard →
  ERROR 1062 `uk_gate_sessions_one_open`, duplicate `pass_no` inside a tenant → ERROR 1062
  `uk_passes_no` — while the same `pass_no` in another tenant inserts cleanly.

That data walk is also what caught `v_open_movements` filtering on the entry row's own
`close_of_movement_id`, which is always NULL: the roll-call would have shown people who had
already left the site. It now excludes entries that another row points back at.

## Verified against a live server

MariaDB 12.3.2, `accessnow_demo`, run through `cmd/migrate` (the embedded copy, `.env` for
credentials) rather than the `mysql` client, so the Go path is what was tested:

- `-force -direction up` from an empty schema reached **version 27, dirty 0** with 92 base tables
  and 8 views. Re-running `-direction up` answers "nothing to do", which is the re-runnability
  promise kept by the tool instead of by hand.
- A second throwaway schema (`DB_NAME=accessnow_scratch`) was taken to 27 and then rolled back
  with `-direction down -steps 27 -allow-destructive`; only `schema_migrations` was left, and the
  schema was dropped afterwards. Every `.down.sql` runs.
- `cmd/devseed` provisioned the tenant, 16 module flags, 11 roles, 341 role grants, site, gate,
  two users, the guard's gate assignment, the tablet device and 8 numbering formats. Run twice,
  the counts stayed identical — the seed is idempotent, and the second run rotates only the device
  token.
- Data walk over HTTP: login → create visit → approve → `POST /api/v1/gate/scan` in → the same
  idempotency key replayed twice → `/gate/on-site` → exit scan → `POST /api/v1/gate/sync` batch
  and that batch replayed. Numbers issued were `VIS-26110001..4`, `entry_count` moved 0 → 1,
  dwell filled in on the exit row, and the replays returned the original `movement_id` with no
  duplicate rows (`COUNT(*) = COUNT(DISTINCT idempotency_key)`).

Two defects only a live run shows, both fixed in Go, not in SQL:

- `separator` is a reserved word (it belongs to `GROUP_CONCAT … SEPARATOR`), so
  `pass_number_formats` calls the column `separator_char`.
- A scan recorded `gate_session_id` from the login session (`user_sessions.id`) instead of the
  guard's open shift (`gate_sessions.id`), so the first scan of a shift hit ERROR 1452. `Scope`
  now carries both fields, named so they cannot be confused, and the device middleware fills the
  shift id.

To repeat it on your own machine:

```bash
go run ./cmd/migrate -direction up
go run ./cmd/devseed
go run ./cmd/api
```
