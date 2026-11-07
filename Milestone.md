# AccessNow — Milestone

Backend tracker for the Go rewrite. One line per thing, ticked against the code that is
actually in the tree — not against the schema, which ships tables for modules that have not
been written yet. Reference: [AccessNow – Module Design and Gate Handling.md](<AccessNow
– Module Design and Gate Handling.md>).

Updated 2026-11-07 · 39 migration pairs · 37 Go packages · 15 module packages · 42 test files

**Legend** — ✅ done and proved live · 🟡 built but partly · ⬜ not started

---

## 1. Modules

- ✅ **1. Visitor Management** — `internal/modules/visitor` — walk-in and pre-registered,
  host and company, check-in and check-out driven by the gate scan, expiry and no-show sweep.
- ✅ **2. Vehicle Tracking** — `internal/modules/vehicle` — plate normalised on entry,
  movement opened and closed, dwell time, overstay flag, fleet vehicles skip the details form.
- ✅ **3. Material and Asset Logistics** — `internal/modules/material` — RGP and NRGP passes
  with line items and partial returns, approval before leaving, inward declarations verified
  at exit with an exception path, overdue sweep in the tenant's timezone.
- ✅ **4. Staff Outpass** — `internal/modules/staff` — request, supervisor approval, guard
  scans the exit, optional return scan, not-back-by-end-of-shift flag, duration limit per reason.
- ✅ **5. Contractor and Recurring Access** — `internal/modules/contractor` — firms, people and
  their paperwork; mandatory documents that expire and blocks that lift themselves when the
  permit is renewed; weekly rota windows per gate, cross-midnight shifts, recurring and day
  passes plus service visits, each mirrored onto Core's pass so the register and the barrier
  agree. Core asks through `movements.Eligibility`, so a lapsed permit turns the gate red
  without Core learning what a contractor is, and `0035` keeps a document block apart from a
  supervisor's block so only the one the paperwork caused is lifted automatically. A sweep
  expires documents, blocks what they cover and reminds the desk; every decision is audited
  and announced on the outbox.
- ✅ **6. QR and Hardware Scanning** — `internal/modules/scanning` — `pass_tokens` issued by
  hand or automatically when a pass becomes usable: 128-bit secrets returned once and stored
  as SHA-256 with an eight-character prefix, expiry inherited from the pass, single-use and
  max-use tokens spent with a compare-and-set, revocation carrying a reason, and a pass that
  settles taking its badges with it. The barrier accepts `token` and the reader kind on
  `POST /api/v1/gate/scan`, resolves it through `movements.Tokens`, and files every read —
  including the refusals that never became a movement — in `scan_attempts` with an outcome and
  a stable response code. With the module off the gate still takes a typed pass number.
- ✅ **7. Approval Workflow** — `internal/modules/approval` — flows by pass type, department
  or value; sequential and parallel steps, one row each; delegation while an approver is away;
  escalation after a timeout; recalled when the pass settles by any other route.
- ✅ **8. Rules and Policy** — `internal/modules/rules` — condition tree plus one action,
  walked at the gate in priority order, block / warn / allow / notify / limit / require_approval,
  quota buckets, a refusal waived only when the rule allows it *and* the holder has
  `rule.override`, full evaluation log. Request-stage rules run when `pass.issued` is relayed;
  nightly rules sweep the open register every six hours; both write the same `rule_evaluations`
  feed as a scan and neither rewrites Core's row.
- ✅ **9. Blacklist and Watchlist** — `internal/modules/blocklist` — matches on id number,
  phone or plate, screens every scan; a hit denies entry and never denies exit; a watchlist hit
  lets the person through and alerts the supervisor quietly.
- ✅ **10. Notifications** — `internal/modules/notifications` — events mapped to per-tenant
  routes and templates, in-app inbox, email over SMTP, SMS and WhatsApp over an HTTP gateway,
  five attempts with delivery status, background sweep loop, and push over the tenant's HTTP
  provider row with a JSON title/body envelope (webhook delivery lives in integrations).
- ✅ **11. Offline Gate Mode** — `internal/modules/offline`. `GET /api/v1/gate/cache` stocks a
  tablet with the passes it may honour and the listings it must refuse, sends only what changed
  since the epoch the device names, and files the bundle in `offline_cache_manifests` so the
  server can say what any device held at any version. `POST /api/v1/gate/sync` takes a batch of
  up to 200 locally stored scans, needs an idempotency key per item, replays a duplicate back as
  the answer it already gave, marks late arrivals, and the server keeps the status. The ledger
  files one argument per movement under five types, tells the supervisor how many rows the
  tablet handed in and how many are argued about, and the desk resolves them at
  `/api/v1/offline/conflicts`. A gate with `offline_capable = 0` is refused a cache. Every
  newly sent pass carries an `offline_token` in the bundle; its hash is filed in
  `offline_cache_manifests.token_hash`, and the same token resolves when the queue replays.
- ✅ **12. Emergency Roll-Call** — `internal/modules/rollcall` — one incident per site at a
  time, raised from a gate, freezing Core's open movements into `evacuation_roster` so the board
  does not change under a warden. Marks transition one locked row at a time into
  `roll_call_marks` with the status they came from, the totals are recounted from the roster
  rather than incremented, and closing with people still unaccounted demands a summary.
  `rollcall.incident_started` and `rollcall.still_unaccounted` are announced through
  notifications, and every transition is audited. `GET /api/v1/gate/on-site` remains the raw
  roster for a gate that only wants names.
- ✅ **13. Reports and Scheduled Exports** — `internal/modules/reports` — all thirteen seeded
  definitions have SQL behind them, each an aggregate over a period bucket and one grouping the
  definition declares (gate, host, department, company, list, guard and so on), so a caller
  names a grouping and never a SQL fragment. `POST /reports/run` streams the file and records
  `report_runs` (rows, checksum, duration, size); CSV is built with `encoding/csv`, XLSX by a
  hand-written package, and PDF by a paginated A4 table writer that embeds no fonts and encodes
  text as WinAnsi. Schedules are hourly, daily, weekly or monthly on the tenant's own zone
  (`time/tzdata` is embedded), take a lookback, recipients and roles, soft-delete so past runs
  keep their link, and are run by a half-minute loop that marks each outcome, skips a tenant
  whose module is off, and publishes `report.ready` for the notifier. The rendered bytes are
  kept on `report_runs.artifact` with their content type (`0039`), and
  `GET /reports/runs/{runID}/download` returns the same file.
- ✅ **14. Audit and Compliance** — `internal/modules/compliance` — retention policies for the
  five categories the purge can honour, with four actions: **null** blanks a value in place,
  **anonymise** replaces an identifier with a stable hash, **delete** removes an attachment row
  (the one record whose whole point is the data), and **archive** erases in place and files a
  receipt in `data_purge_archives` (`0036`) holding a hash, never the value. A purge defaults to
  a dry run, refuses without a policy, counts held rows and blanks only what no active legal
  hold protects; holds are placed, released and expired on their own date; and chain
  verification rebuilds the writer's hash through `audit.VerifyChain`, catching both an edited
  row and a removed one, with every check recorded in `audit_verifications`. A six-hour sweep
  publishes `compliance.purge_due` before a policy's next eligible date (`notify_before_days`,
  `last_notified_at` in `0039`), and the auditor role is read-only at the router for every
  mutation except its own logout.
- ✅ **15. Integrations and API** — `internal/modules/integrations` — API keys issued with a
  one-time secret, a stored SHA-256 and a short prefix, scopes, environment, expiry and a
  revocation reason, authenticated on the machine surface from `X-API-Key`; **each machine data
  route checks the key's own scope** (`visitor.view` for visitors, `core.pass.view` for a pass,
  `core.gate.view` for movements), and `*` is the only wildcard. Webhook endpoints must be
  public https (checked at save time and again at connect time), deliveries fan out from every
  outbox event through `events.Registry.OnAny`, are HMAC-signed with a timestamp, retried on a
  bounded ladder, buried as dead after the endpoint's budget, and switched off after twenty
  consecutive failures. **HR/ERP sync** configures an `external_systems` row (kind, direction,
  frequency, field map), authenticates from an `ACCESSNOW_SYNC_*` environment reference that is
  never stored, and runs through `sync_jobs`: pull links each remote record with a content hash
  so an unchanged register writes nothing, push sends staff, departments or visitor companies
  under an `items` envelope, a cursor carries between incremental runs, and the transport
  refuses a private address at connect time and on every redirect. **A failed run is requeued
  on its own ladder** — one minute, five, twenty, four attempts in all (`0038`) — and only the
  final failure marks the job failed; the next frequency interval is not the retry.
  **Per-key rate limiting is shared**: the fixed one-minute window lives in
  `api_key_rate_windows` (`0037`), so every API instance charges the same budget, takes
  `rate_limit_per_minute` as the limit (600 when unset), and answers an over-budget call with a
  429 and `Retry-After`. Actuator commands remain idempotent and audited.
  `GET /api/v1/gate/integrations/commands` pulls the queued commands for the device's own gate,
  and `POST .../{commandID}/ack` closes one only from that gate.

---

## 2. Core and the gate model

Always-on Core, not sellable modules.

- ✅ Tenants, sites, gates, users, roles and permissions, module on/off flags, sessions.
- ✅ Embedded, re-runnable migrations with up and down; applied, rolled back and re-applied.
- ✅ Tenant pass-number formats, one allocator per tenant per period, and an admin surface:
  `GET /passes/number-formats` and `PUT /passes/number-formats/{formatKey}` validate the
  pattern, sequence width, reset period and rendered length before saving.
- ✅ Server-side `active_gate_id`, the picker for a guard with several gates, switching writes
  an audit entry, and every scan takes the gate from the session rather than a form field. The
  active gate is revalidated on every authenticated request, so a disabled gate or a revoked
  assignment stops the session instead of waiting for the next pick.
- ✅ Gate sessions for shift accountability, closed on switch, `/gates/shift/end`, logout and
  session timeout, with the schema's own `logout` and `timeout` reasons.
- ✅ Shared devices: a device token decides the gate on `/api/v1/gate/*`, and the device path
  re-checks the guard's assignment before it attaches a shift.
- ✅ Gate categories enforced at the barrier — a delivery pass scanned at Main is red with
  "not valid at this gate".
- ✅ `same_gate_exit_required` is enforced: an exit through a different gate is refused when
  the pass has an open entry.
- ✅ Gate `opens_at` / `closes_at`, `gate_time_windows` (day, category, allow/block, overnight)
  and the gate's `status` are enforced at scan time; `pass_gates` narrows a pass to its listed
  gates.

---

## 3. Platform and delivery

- ✅ Layering enforced by a test: platform → core → modules → server → cmd, modules never
  import each other, `internal/server/deps.go` the only file that names both sides of a seam.
- ✅ Any module switched off hides its surface and the gate still works — proved with
  `rules_policy` and `blocklist_watchlist`: 404 on the desk, and the scan the rulebook was
  refusing turns green.
- ✅ Outbox events with a dispatcher, a registry, and a backlog relay that catches what no
  request relayed. A subscriber may name one key or, as webhooks do, ask for every event
  (`OnAny`); with an any-handler no key is settled as unsubscribed.
- ✅ Audit writer, pagination, tenant scoping, error envelope, request logging as platform
  reusables. The chain's hash formula is exported for verification instead of being copied.
- ✅ `cmd/api`, `cmd/migrate`, `cmd/devseed` — one tenant you can log into, re-runnable; a
  `Makefile` with build, fmt, vet, test, check, run and seed.
- ✅ Unit and engine tests, the layering test, and a router-level HTTP test that serves
  `/healthz` and the not-found boundary through the real handler stack. CI (`.github/workflows`)
  runs gofmt, build, vet and tests, with `.golangci.yml` and a multi-stage `Dockerfile`. The
  webhook and sync transports are tested for signing, address refusal, backoff and receiver
  behaviour, but have not been exercised against a real external system.
- ✅ Every gate surface that has code now has tests: visitor, vehicle, material, staff,
  contractor, scanning, roll-call, offline, blocklist, rules, approval, notifications, reports
  (including PDF structure and pagination), compliance (including the action matrix) and
  integrations (including scope matching, record decoding, sync target refusal and the retry
  ladder). The sync pull and push paths run against a fake HTTPS receiver: a 503 is retried, a
  failed call links nothing, a 502 on push is retried, the auth header and cursor cross the
  wire, and the production dialer still refuses a loopback address. The rate limiter is tested
  for its window, its fail-open behaviour and its isolation between keys; the shared window was
  proved live with two API instances on one database.

---

## 4. Design-doc open items

- ✅ **Migrations request, section 4 — closed.** `migrations/` is numbered `0001…0039` in
  grouped, sensibly named pairs, each with an up and a down; statements are re-runnable;
  `users` keeps `first_name` and `last_name`; roles, permissions, `role_permissions`, visitors,
  `visitor_companies` and the related tables are all there; no `ENUM` anywhere — `VARCHAR` with
  a `CHECK` instead, so a new value is a migration and not a rebuild. The later additions are
  `0036` (archive receipts), `0037` (shared rate window), `0038` (sync attempt and retry
  columns) and `0039` (report artifacts, pre-purge warning stamp, purge-due event).
- ✅ **Next module — the standing brief is covered.** Items 1–15 are written. Nothing below is
  a new module; each one is a named edge of code that already exists.

---

## 5. What still remains

Nothing on this list is a new feature request; these are the unfinished edges the tracker has
been carrying in ⬜ and 🟡, collected here so they can be closed one at a time.

**Gate behaviour (Core)**

- ✅ Pass-number admin endpoints, validated against the same renderer the allocator uses.
- ✅ `active_gate_id` revalidated on every authenticated request.
- ✅ `same_gate_exit_required` enforced at the barrier.
- ✅ Gate status, opening hours, `gate_time_windows` and `pass_gates` enforced at scan time.
- ✅ Gate sessions close with `logout` and `timeout`.

**Module edges**

- ✅ Rules: request-stage evaluation on `pass.issued`, nightly sweep over the open register.
- ✅ Notifications: push sender over the tenant's HTTP provider row.
- ✅ Offline: per-pass scan token issued with the cache and hashed in the manifest.
- ✅ Reports: stored artifact with a download endpoint.
- ✅ Compliance: `compliance.purge_due` warnings before the next eligible date.
- ✅ Compliance: auditor role enforced read-only at the router.
- ✅ Integrations: gate-device actuator pull and gate-scoped acknowledgement.

**Delivery**

- ✅ Router-level HTTP test, CI workflow, lint configuration and a multi-stage `Dockerfile`.
  Go build, vet, tests and lint were run locally, and the service booted against MySQL with
  `/readyz` and `/healthz` both returning 200. The image itself was not built because the
  Docker daemon was not running.
- ⬜ Webhooks and sync have not run against a real third-party system. The fake receiver proves
  the wire shape; a vendor's own quirks still need one run with real credentials and a real
  endpoint.
