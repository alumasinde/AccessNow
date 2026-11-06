package blocklist

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the list surface. With blocklist_watchlist switched off these paths answer
// 404 and the rest of the system is unchanged: the guard Core holds returns an empty verdict,
// every gate keeps opening on the rules it had before, and no listing is consulted.
//
// There is no endpoint for checking a pass here. Screening happens inside a scan, where the
// gate already is, and a second public "is this person banned" route would be a lookup of the
// tenant's bans that any authenticated user could run.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("blocklist.view")
	manage := g.Permission("blocklist.manage")
	review := g.Permission("blocklist.review_hit")

	// Static before the wildcards: chi prefers the exact segment, so a summary is never read
	// as an entry id and a hit is never read as an entry.
	r.With(desk, see).Get("/blocklist/summary", h.Summary)

	r.With(desk, manage).Post("/blocklist/entries", h.Add)
	r.With(desk, see).Get("/blocklist/entries", h.List)
	r.With(desk, see).Get("/blocklist/entries/{entryID}", h.Get)
	r.With(desk, manage).Post("/blocklist/entries/{entryID}/revoke", h.Revoke)

	r.With(desk, see).Get("/blocklist/hits", h.ListHits)
	r.With(desk, see).Get("/blocklist/hits/{hitID}", h.GetHit)
	r.With(desk, review).Post("/blocklist/hits/{hitID}/resolve", h.Resolve)
}
