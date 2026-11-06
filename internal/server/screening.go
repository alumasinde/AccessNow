package server

import (
	"context"
	"time"

	"accessnow/internal/modules/blocklist"
	"accessnow/internal/modules/offline"
)

// listCeiling is the biggest screening list a device cache will carry. It is a ceiling and not
// a page: a tablet that is given half a blocklist is a gate that opens for the other half, and
// a site whose list outgrows this one needs the list at the server, which is what the online
// screen already is.
const listCeiling = 5000

// screeningCache hands the offline bundle the list half of a device cache.
//
// It is a piece of the wiring rather than a dependency of either module because the offline
// module has to keep working for a tenant that never bought screening, and the list module has
// no reason to know a device cache exists. What the tablet gets is the value it has to compare
// against, in the same normalised form the server screens with - a hash is an index, and a
// device cannot index a name it has only just heard.
type screeningCache struct{ svc *blocklist.Service }

func (c screeningCache) Cached(ctx context.Context, tenantID int64) ([]offline.CachedListing, error) {
	if c.svc == nil {
		return nil, nil
	}
	entries, err := c.svc.List(ctx, blocklist.ListFilter{
		TenantID: tenantID, Limit: listCeiling,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]offline.CachedListing, 0, len(entries))
	for _, entry := range entries {
		// A listing that has run out is left out rather than sent with its expiry on it. The
		// device has no second chance to be told, and a ban nobody renewed is not a ban.
		if entry.ExpiredAt(now) {
			continue
		}
		out = append(out, offline.CachedListing{
			ListType: entry.ListType, MatchType: entry.MatchType, MatchValue: entry.MatchValue,
			DisplayName: deref(entry.DisplayName), Reason: entry.Reason,
			Severity: entry.Severity, ExpiresAt: entry.ExpiresAt,
		})
	}
	return out, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
