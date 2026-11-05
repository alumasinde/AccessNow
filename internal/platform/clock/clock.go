package clock

import "time"

// Location falls back to UTC so a bad tzname in a tenant row degrades instead of
// failing every scheduled job for that tenant.
func Location(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// PeriodKey is the counter bucket for a reset policy, counted on the tenant wall
// clock: a Nairobi tenant starts its day at 03:00 UTC, not at midnight UTC.
func PeriodKey(reset string, at time.Time, loc *time.Location) string {
	local := at.In(loc)
	switch reset {
	case "daily":
		return local.Format("20060102")
	case "monthly":
		return local.Format("200601")
	case "yearly":
		return local.Format("2006")
	default:
		return ""
	}
}
