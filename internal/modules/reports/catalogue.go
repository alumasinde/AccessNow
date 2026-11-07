package reports

// The catalogue is the implementation half of report_definitions. Every seeded definition has
// one entry here, and the two are kept honest by a test: a definition the desk can see but
// this table cannot run is a button that fails.
//
// Every report has the same shape - a period bucket, one grouping label, one metric - because
// that is what the schema's period_grain, allowed_groupings and a single value column can
// honestly promise with no chart library. Groupings are expressions chosen from a closed map,
// so a caller picks a name and never a fragment of SQL.

type grouping struct {
	// join is added after the report's base FROM, for groupings that need another table.
	join   string
	expr   string
	header string
}

type report struct {
	key string
	// base is the FROM clause with the t alias, including the joins the metric always needs.
	base string
	// period is the t column the ?/? window is applied to.
	period   string
	bucket   string
	periodHD string
	metric   string
	metricHD string
	// filter narrows every run of the report, whatever grouping is asked for.
	filter   string
	limit    int
	groups   map[string]grouping
	defGroup string
}

var catalogue = buildCatalogue()

func buildCatalogue() map[string]report {
	out := []report{
		{
			key:    "gate_movements_daily",
			base:   "gate_movements t JOIN gates g ON g.id = t.gate_id",
			period: "occurred_at", bucket: "DATE(t.occurred_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Movements",
			defGroup: "gate",
			groups: map[string]grouping{
				"gate":      {expr: "g.name", header: "Gate"},
				"site":      {join: "JOIN sites s ON s.id = t.site_id", expr: "s.name", header: "Site"},
				"direction": {expr: "t.direction", header: "Direction"},
				"result":    {expr: "t.result", header: "Result"},
				"guard":     {join: "JOIN users u ON u.id = t.guard_user_id", expr: person("u"), header: "Guard"},
			},
		},
		{
			key:    "visitor_log",
			base:   "visits t JOIN visitors v ON v.id = t.visitor_id",
			period: "created_at", bucket: "DATE(t.created_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Visits",
			defGroup: "host",
			groups: map[string]grouping{
				"host":    {expr: "t.host_name_snapshot", header: "Host"},
				"company": {join: "JOIN visitor_companies c ON c.id = t.visitor_company_id", expr: "c.name", header: "Company"},
				"gate":    {join: "JOIN gates g ON g.id = t.checked_in_gate_id", expr: "g.name", header: "Gate"},
				"status":  {expr: "t.status", header: "Status"},
			},
		},
		{
			key:    "host_activity",
			base:   "visits t",
			period: "created_at", bucket: "DATE(t.created_at)", periodHD: "Day",
			metric:   "ROUND(COALESCE(AVG(TIMESTAMPDIFF(MINUTE, t.host_notified_at, t.host_responded_at)), 0))",
			metricHD: "Minutes to reply",
			defGroup: "host",
			groups: map[string]grouping{
				"host":     {expr: "t.host_name_snapshot", header: "Host"},
				"response": {expr: "COALESCE(t.host_response, 'none')", header: "Reply"},
			},
		},
		{
			key:    "vehicle_dwell_times",
			base:   "vehicle_passes t",
			period: "created_at", bucket: "DATE(t.created_at)", periodHD: "Day",
			metric: "ROUND(COALESCE(AVG(t.actual_dwell_minutes), 0))", metricHD: "Average minutes on site",
			defGroup: "gate",
			groups: map[string]grouping{
				"gate":   {join: "JOIN gates g ON g.id = t.entry_gate_id", expr: "g.name", header: "Entry gate"},
				"plate":  {expr: "t.plate_display", header: "Plate"},
				"status": {expr: "t.status", header: "Status"},
			},
		},
		{
			key:    "material_outstanding",
			base:   "material_items t JOIN material_passes mp ON mp.id = t.material_pass_id",
			period: "created_at", bucket: "DATE(t.expected_return_at)", periodHD: "Expected back",
			metric: "COUNT(*)", metricHD: "Lines outstanding",
			filter:   "t.returnable = 1 AND t.returned_at IS NULL",
			defGroup: "kind",
			groups: map[string]grouping{
				"kind":     {expr: "mp.kind", header: "Kind"},
				"category": {expr: "COALESCE(t.category, 'uncategorised')", header: "Category"},
				"status":   {expr: "t.status", header: "Status"},
			},
		},
		{
			key:    "material_exceptions",
			base:   "material_passes t",
			period: "created_at", bucket: "DATE(t.created_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Passes",
			defGroup: "status",
			groups: map[string]grouping{
				"status":  {expr: "t.status", header: "Status"},
				"kind":    {expr: "t.kind", header: "Kind"},
				"carrier": {expr: "COALESCE(t.carrier_name, 'not named')", header: "Carrier"},
			},
		},
		{
			key:    "outpass_register",
			base:   "outpasses t JOIN staff s ON s.id = t.staff_id",
			period: "requested_at", bucket: "DATE(t.requested_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Outpasses",
			defGroup: "department",
			groups: map[string]grouping{
				"department": {join: "JOIN departments d ON d.id = t.department_id", expr: "d.name", header: "Department"},
				"staff":      {expr: "CONCAT(s.first_name, ' ', COALESCE(s.last_name, ''))", header: "Staff"},
				"reason":     {expr: "t.reason_key", header: "Reason"},
				"status":     {expr: "t.status", header: "Status"},
			},
		},
		{
			key:    "contractor_expiry",
			base:   "contractor_documents t JOIN contractors c ON c.id = t.contractor_id",
			period: "created_at", bucket: "DATE(t.expires_on)", periodHD: "Expires",
			metric: "COUNT(*)", metricHD: "Documents",
			defGroup: "company",
			groups: map[string]grouping{
				"company": {join: "JOIN contractor_organizations o ON o.id = c.organization_id", expr: "o.name", header: "Company"},
				"type":    {expr: "t.document_type_key", header: "Document"},
				"status":  {expr: "t.status", header: "Status"},
			},
		},
		{
			key:    "blocklist_hits",
			base:   "blocklist_hits t JOIN blocklist_entries e ON e.id = t.entry_id",
			period: "occurred_at", bucket: "DATE(t.occurred_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Hits",
			defGroup: "list",
			groups: map[string]grouping{
				"list":   {expr: "e.list_type", header: "List"},
				"action": {expr: "t.action_taken", header: "Action"},
				"gate":   {join: "JOIN gates g ON g.id = t.gate_id", expr: "g.name", header: "Gate"},
			},
		},
		{
			key:    "approval_latency",
			base:   "approval_request_steps t",
			period: "created_at", bucket: "DATE(t.created_at)", periodHD: "Day",
			metric:   "ROUND(COALESCE(AVG(TIMESTAMPDIFF(MINUTE, t.assigned_at, t.acted_at)), 0))",
			metricHD: "Minutes to decide",
			defGroup: "status",
			groups: map[string]grouping{
				"status": {expr: "t.status", header: "Step status"},
				"role":   {expr: "COALESCE(t.approver_role_key, 'named person')", header: "Approver"},
			},
		},
		{
			key:    "gate_utilisation",
			base:   "gate_sessions t JOIN gates g ON g.id = t.gate_id",
			period: "opened_at", bucket: "DATE(t.opened_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Shifts",
			defGroup: "gate",
			groups: map[string]grouping{
				"gate":  {expr: "g.name", header: "Gate"},
				"guard": {join: "JOIN users u ON u.id = t.user_id", expr: person("u"), header: "Guard"},
			},
		},
		{
			key:    "guard_activity",
			base:   "gate_movements t JOIN users u ON u.id = t.guard_user_id",
			period: "occurred_at", bucket: "DATE(t.occurred_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Movements",
			defGroup: "guard",
			groups: map[string]grouping{
				"guard":  {expr: person("u"), header: "Guard"},
				"gate":   {join: "JOIN gates g ON g.id = t.gate_id", expr: "g.name", header: "Gate"},
				"result": {expr: "t.result", header: "Result"},
			},
		},
		{
			key:    "audit_summary",
			base:   "audit_logs t",
			period: "occurred_at", bucket: "DATE(t.occurred_at)", periodHD: "Day",
			metric: "COUNT(*)", metricHD: "Entries",
			defGroup: "action",
			groups: map[string]grouping{
				"action":  {expr: "t.action", header: "Action"},
				"actor":   {expr: "t.actor_type", header: "Actor"},
				"subject": {expr: "COALESCE(t.subject_type, 'none')", header: "Subject"},
			},
		},
	}

	out2 := make(map[string]report, len(out))
	for _, r := range out {
		if r.limit == 0 {
			r.limit = maxRunRows
		}
		out2[r.key] = r
	}
	return out2
}

// person is the display name every module stores on users: first and last, with the empty
// last name collapsed rather than left as a trailing space.
func person(alias string) string {
	return "CONCAT(" + alias + ".first_name, ' ', COALESCE(" + alias + ".last_name, ''))"
}

// query builds the one statement a run executes. The grouping name is looked up, never
// interpolated: a group the report does not offer is refused before any SQL exists.
func (r report) query(group string) (string, []string, bool) {
	if group == "" {
		group = r.defGroup
	}
	g, ok := r.groups[group]
	if !ok {
		return "", nil, false
	}
	sql := "SELECT " + r.bucket + " AS period_bucket, " + g.expr + " AS group_label, " +
		r.metric + " AS metric_value FROM " + r.base + " " + g.join +
		" WHERE t.tenant_id = ? AND t." + r.period + " >= ? AND t." + r.period + " < ?"
	if r.filter != "" {
		sql += " AND " + r.filter
	}
	sql += " GROUP BY period_bucket, group_label ORDER BY period_bucket DESC, metric_value DESC LIMIT ?"
	return sql, []string{r.periodHD, g.header, r.metricHD}, true
}

// groupOf names the grouping a run actually used, so a stored run can say what its columns
// meant after the caller forgot.
func (r report) groupOf(group string) string {
	if group == "" {
		return r.defGroup
	}
	return group
}

func (r report) groupsOf() []string {
	out := make([]string, 0, len(r.groups))
	for key := range r.groups {
		out = append(out, key)
	}
	return out
}

// implemented reports whether a definition the database offers has SQL behind it.
func implemented(key string) bool {
	_, ok := catalogue[key]
	return ok
}
