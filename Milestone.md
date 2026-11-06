# AccessNow — Milestone

Backend tracker for the Go rewrite. One line per thing, ticked against the code that is
actually in the tree — not against the schema, which ships tables for modules that have not
been written yet. Reference: [AccessNow – Module Design and Gate Handling.md](<AccessNow
– Module Design and Gate Handling.md>).

Updated 2026-11-06 · 33 migration pairs · 30 Go packages · 8 module packages · 28 test files

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
- ✅ **7. Approval Workflow** — `internal/modules/approval` — flows by pass type, department
  or value; sequential and parallel steps, one row each; delegation while an approver is away;
  escalation after a timeout; recalled when the pass settles by any other route.
- ✅ **8. Rules and Policy** — `internal/modules/rules` — condition tree plus one action,
  walked at the gate in priority order, block / warn / allow / notify / limit / require_approval,
  quota buckets, a refusal waived only when the rule allows it *and* the holder has
  `rule.override`, full evaluation log. ⬜ Request-stage and nightly rules — the write path
  refuses them so nothing can be filed inert.
- ✅ **9. Blacklist and Watchlist** — `internal/modules/blocklist` — matches on id number,
  phone or plate, screens every scan; a hit denies entry and never denies exit; a watchlist hit
  lets the person through and alerts the supervisor quietly.
- ✅ **10. Notifications** — `internal/modules/notifications` — events mapped to per-tenant
  routes and templates, in-app inbox, email over SMTP, SMS and WhatsApp over an HTTP gateway,
  five attempts with delivery status, background sweep loop. ⬜ push and webhook senders.
- 🟡 **11. Offline Gate Mode** — `POST /api/v1/gate/sync` takes a batch of up to 200 locally
  stored scans, needs an idempotency key per item, replays a duplicate back as the answer it
  already gave, marks late arrivals, and the server keeps the status. ⬜ The device half:
  `offline_cache_manifests`, `device_sync_batches` and `sync_conflicts` have no reader, so a
  tablet has nothing to cache today's passes against.
- ⬜ **5. Contractor and Recurring Access** — tables from `0014` only. A validity window and a
  pass type cover the recurring case for now; document expiry blocking a pass is not written.
- ⬜ **6. QR and Hardware Scanning** — `pass_tokens` and `scan_attempts` exist, `HashToken`
  exists, nothing joins them: the gate resolves `pass_no` or `pass_id` alone, and `token.issue`
  is a permission no code asks for. Manual search and click check-in already work, which is the
  standalone default.
- ⬜ **12. Emergency Roll-Call** — only the open-movements roster, `GET /api/v1/gate/on-site`.
  No incidents, muster points, accounted-for marking, or grouping across modules and gates.
- ⬜ **13. Reports and Scheduled Exports** — `0022` tables only. No aggregation reads, no PDF
  or Excel, no scheduled compile-and-mail.
- 🟡 **14. Audit and Compliance** — hash chaining is written by Core and every module
  (`internal/platform/audit`), and the chain of custody is on every row. ⬜ The verification
  tool (`audit.verify` is seeded and unused), retention rules, legal hold, the purge job that
  blanks ID numbers after a set number of days, and read-only enforcement of the auditor role.
- ⬜ **15. Integrations and API** — `0023` tables only. No API keys, no outbound webhooks on
  events, no HR or ERP sync, no boom-gate or turnstile trigger.

---

## 2. Core and the gate model

Always-on Core, not sellable modules.

- ✅ Tenants, sites, gates, users, roles and permissions, module on/off flags, sessions.
- ✅ Embedded, re-runnable migrations with up and down; applied, rolled back and re-applied.
- ✅ Tenant pass-number formats, one allocator per tenant per period. ⬜ No admin endpoint yet —
  only the seed writes formats, so a tenant cannot change its own numbering through the API.
- ✅ Server-side `active_gate_id`, the picker for a guard with several gates, switching writes
  an audit entry, and every scan takes the gate from the session rather than a form field.
- 🟡 `active_gate_id` is checked against the guard's assignments when it is picked; no
  middleware revalidates it on every request.
- ✅ Gate sessions for shift accountability, closed on switch or `/gates/shift/end`; movements
  carry `gate_id`, `gate_session_id` and the guard. ⬜ The `logout` and `timeout` close reasons.
- ✅ Shared devices: a device token decides the gate on `/api/v1/gate/*`.
- ✅ Gate categories enforced at the barrier — a delivery pass scanned at Main is red with
  "not valid at this gate".
- 🟡 Enter at one gate and exit at another works; `same_gate_exit_required` is read into the
  model and never consulted, so strict mode is a column.
- ⬜ Gate `opens_at` / `closes_at` and `gate_time_windows` are not enforced at scan time — only
  the pass's own validity window is. A gate can be disabled and still answer.
- ⬜ `pass_gates` (a pass restricted to an allowed-gates list) is schema only; the refusal above
  comes from categories.

---

## 3. Platform and delivery

- ✅ Layering enforced by a test: platform → core → modules → server → cmd, modules never
  import each other, `internal/server/deps.go` the only file that names both sides of a seam.
- ✅ Any module switched off hides its surface and the gate still works — proved with
  `rules_policy` and `blocklist_watchlist`: 404 on the desk, and the scan the rulebook was
  refusing turns green.
- ✅ Outbox events with a dispatcher, a registry, and a backlog relay that catches what no
  request relayed.
- ✅ Audit writer, pagination, tenant scoping, error envelope, request logging as platform
  reusables.
- ✅ `cmd/api`, `cmd/migrate`, `cmd/devseed` — one tenant you can log into, re-runnable; a
  `Makefile` with build, fmt, vet, test, check, run and seed.
- ✅ Unit and engine tests, and the layering test. ⬜ HTTP integration tests that drive the
  router itself; ⬜ CI workflow, lint config and a container build.
- ⬜ The other modules' gate surfaces (offline bundle, QR, roll-call, reports, integrations)
  have no tests because they have no code.

---

## 4. Design-doc open items

- ✅ **Migrations request, section 4 — closed.** `migrations/` is numbered `0001…0033` in
  grouped, sensibly named pairs, each with an up and a down; statements are re-runnable;
  `users` keeps `first_name` and `last_name`; roles, permissions, `role_permissions`, visitors,
  `visitor_companies` and the related tables are all there; no `ENUM` anywhere — `VARCHAR` with
  a `CHECK` instead, so a new value is a migration and not a rebuild.
- ⬜ **Next module.** Per the standing brief, the pick is the offline gate bundle (it finishes a
  module already half-built and is what a guard hits when the link drops), then contractor
  access, then roll-call. Reports, audit-compliance and integrations can wait until the gate
  story is whole.
