# AccessNow – Module Design and Gate Handling

Cleaned from the "Gate pass system design" chat. Covers the module breakdown, the gate model, and the pending migrations request.

## 1. The independence rule

A small **Core** is always on and is not a sellable module. It holds tenants, login and roles, sites and gates, module on/off flags, and basic audit writing.

- Every module depends only on Core.
- Modules never call each other directly. They publish events (for example `movement.recorded`), and other modules may optionally react.
- If a reacting module is off, a safe default applies. This is what lets any one module run alone.

## 2. Modules

### 1. Visitor Management

- **Does:** Pre-registered and walk-in visitors, host approval, check-in and check-out.
- **Logic:** `PENDING → APPROVED → ON_SITE → COMPLETED`, plus `REJECTED`, `EXPIRED`, `CANCELLED`. A guard creates a walk-in, the host is notified, and the visit is approved when the host replies. Each gate scan writes a movement row, and the time window is checked at entry.
- **Standalone default:** Single host approval with in-app notices.

### 2. Vehicle Tracking

- **Does:** Logs vehicles in and out by plate, with driver and purpose.
- **Logic:** Plate is normalised on entry. Entry opens a movement, exit closes it, and dwell time is computed. Vehicles over the tenant's time limit are flagged as overstaying. Registered fleet vehicles skip the details form, one-off vehicles need it.
- **Standalone default:** Needs only a plate and driver name, no visitor record.

### 3. Material and Asset Logistics

- **Does:** Inward and outward goods, with returnable (RGP) and non-returnable (NRGP) passes.
- **Logic:** Outward passes need approval before leaving. Each pass has line items with quantities, so partial returns work. Returnable items carry an expected return date, and a nightly job in the tenant's timezone marks overdue items and alerts the issuer. NRGP closes at exit. Inward personal items are declared at the gate and verified at exit, with an exception path if the item stays behind or someone else takes it.
- **Standalone default:** One approver, overdue alert shown on the dashboard.

### 4. Staff Outpass

- **Does:** Employees leaving during working hours.
- **Logic:** Employee requests, supervisor approves, guard scans the exit. Return scan is optional per tenant. Anyone not back by end of shift is flagged. Duration limits apply per reason.

### 5. Contractor and Recurring Access

- **Does:** Long-term passes for contractors, suppliers and regular drivers.
- **Logic:** A pass has a validity range, allowed gates and time windows, and allows multiple entries. Each person's documents (permit, insurance, induction) have expiry dates, and an expired document blocks the pass automatically.

### 6. QR and Hardware Scanning

- **Does:** Fast gate validation using QR codes, ID readers, plate cameras (ANPR) and USB scanners.
- **Logic:** A random 128-bit token is issued on approval and only its hash is stored. A scan is checked against tenant, gate, status and time window, then returns green or red. Hardware just feeds the same scan endpoint.
- **Standalone default:** When off, manual search and click check-in still work.

### 7. Approval Workflow

- **Does:** Multi-level routing for any pass type.
- **Logic:** Flows are defined by pass type, department or value. Steps can be sequential or parallel, with delegation when an approver is away and escalation after a set time without response. Every step is its own row.
- **Standalone default:** A single approver, normally the host or the requester's manager.

### 8. Rules and Policy

- **Does:** Automatic restrictions on requests and gate scans.
- **Logic:** A rule is a condition plus an action (block, warn, or require extra approval). Examples: allowed hours, visit quotas, mandatory fields, items that always need a manager. Rules run at request time and again at the gate.
- **Standalone default:** Basic required-field validation only.

### 9. Blacklist and Watchlist

- **Does:** Blocks banned people and vehicles, and quietly alerts on watched ones.
- **Logic:** Entries match on ID number, phone or plate, and every check-in is screened. A blacklist hit denies entry. A watchlist hit allows entry but silently notifies the supervisor.

### 10. Notifications

- **Does:** Email, SMS and WhatsApp alerts, plus in-app notices.
- **Logic:** Events (approval needed, visitor arrived, item overdue) map to templates per channel. A queue handles sending with retries and delivery status. Each tenant picks channels and recipients.
- **Standalone default:** In-app notices only.

### 11. Offline Gate Mode

- **Does:** Keeps gates working when the internet drops.
- **Logic:** The gate device caches today's approved passes and the blacklist, stores movements locally with a device ID and local timestamp, and syncs when back online. On conflict the server keeps the status, all movement records are kept, and late-synced entries are marked.

### 12. Emergency Roll-Call

- **Does:** Shows who is on site right now.
- **Logic:** Lists every open movement (entered, not exited) across all active modules, grouped by site or muster point. During an evacuation guards mark people as accounted for, and the system shows who is still missing.

### 13. Reports and Scheduled Exports

- **Does:** On-demand analytics and automatic reports.
- **Logic:** Reads operational tables and aggregates by period, type, gate or host. Scheduled reports compile on the tenant's chosen day and time, produce PDF or Excel, and email the admins. Only data from enabled modules appears.

### 14. Audit and Compliance

- **Does:** Tamper-evident history and data protection.
- **Logic:** Core always writes basic audit entries. This module adds hash chaining (each row stores the previous row's hash) with a verification tool, a read-only Auditor role, retention rules, and auto-purge of ID photos and numbers after a set number of days.

### 15. Integrations and API

- **Does:** Connects the system to other tools.
- **Logic:** API keys per tenant, webhooks on events, HR or ERP sync for staff records, and triggers for boom gates or turnstiles on approved scans.

## 3. Gates

A **gate** is a physical checkpoint belonging to a **site**. A hotel might have Main Gate, Staff Gate and Delivery Gate. Gates live in Core, so every module uses them. A tenant with one entrance gets a default gate automatically and never sees a picker.

Each gate has:

- **Type or purpose:** main, staff, delivery, vehicle-only, service
- **Allowed pass categories:** e.g. Delivery takes materials and vehicles, Staff takes staff and outpasses, Main takes visitors
- **Direction:** in only, out only, or both
- **Operating hours** and an on/off status
- **Registered devices:** scanners, tablets, ANPR cameras
- **Assigned users:** which guards may work it

### Where the gate is used

- Every movement row stores `gate_id`, so reports show who passed which gate, when, and which guard handled it.
- Passes can be restricted to gates. An optional allowed-gates list on a pass, where empty means any gate. A delivery pass scanned at Main returns red: "Not valid here, use Delivery Gate".
- Rules and time windows can be set per gate (staff-gate curfew, delivery-gate hours).
- The guard screen adapts. Tabs shown are the tenant's enabled modules intersected with the gate's allowed categories. At the Delivery Gate the guard sees Materials and Vehicles, not Visitors.
- Offline mode caches per gate, so each device stores only the passes valid at its gate.
- Roll-call and reports can group by gate, and notifications can route to that gate's supervisor.

### Selecting the gate after login

1. User logs in and the tenant is resolved.
2. If the role has no gate work (requester, approver, auditor), skip the picker.
3. If a guard has exactly one assigned gate, select it automatically.
4. If a guard has several, show the picker with the last used gate preselected.
5. Supervisors and admins get the picker plus an "All gates" monitoring view.
6. The choice is saved in the **server-side session** as `active_gate_id` and survives page refreshes.
7. The header always shows the active gate as a badge, e.g. "Delivery Gate – Shift started 07:00".

**Safety rules**

- Validate `active_gate_id` against the user's assignments on every request. Never trust it from the browser.
- A guard cannot pick an unassigned gate.
- Switching gates is allowed only if the role permits it, and it writes an audit entry.
- Every scan and manual action takes the gate from the session, not a form field.

### Gate sessions (shift accountability)

A `gate_sessions` table records `user_id, gate_id, device_id, started_at, ended_at`.

- Picking a gate opens a session. Switching gate, logging out, or timeout closes it.
- This answers "who was on duty at the Staff Gate at 2am?" and makes handovers explicit.

### Shared devices (optional, recommended)

For a fixed tablet at a gate, register the device to the gate. The device token decides the gate, the guard logs in with a PIN or credentials, and the picker is skipped.

### Gate tables

```
sites          (id, tenant_id, name, timezone)
gates          (id, tenant_id, site_id, name, type, direction, status, opens_at, closes_at)
gate_categories(gate_id, category)          -- allowed pass categories
gate_users     (gate_id, user_id)           -- who may work this gate
gate_devices   (id, gate_id, device_token, label)
gate_sessions  (id, user_id, gate_id, device_id, started_at, ended_at)
pass_gates     (pass_id, gate_id)           -- optional restriction, empty = any
gate_movements (…, gate_id, gate_session_id, guard_id)
```

### Cross-gate behaviour (decide per tenant)

- **Enter at one gate, exit at another:** allowed by default with both gates recorded. A strict mode can require exit at the same gate.
- **Gate closed or disabled:** scans are refused with a clear message. Supervisors can override with an audited reason.
- **Two guards at one gate:** allowed, each with their own gate session.

## 4. Open item: migrations

The last request was not delivered because the usage limit was hit. Brief as sent:

- `migrations/` folder, files named `0001...`, `0002...` with up and down, grouped sensibly
- Re-runnable statements
- Users always have `first_name` and `last_name`
- Tables for roles, permissions, role\_permissions, visitors, visitor\_companies and related
- Avoid ENUMs where possible
- Target quality: 10/10, written as a senior MySQL/MariaDB and Go engineer
