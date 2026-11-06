package notifications

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the desk surface for notices and their configuration. Nothing here decides
// anything about a pass: it reads an inbox, moves a read marker, and edits the wording and the
// routing a reactor will use later.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Inbox is the bell: this person's in-app notices, newest first, with the unread count in the
// same answer so a client does not need a second round trip to paint a badge.
func (h *Handler) Inbox(w http.ResponseWriter, r *http.Request) {
	scope, page, filter, err := readInbox(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.Limit = page.Limit + 1
	rows, err := h.svc.Inbox(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	unread, err := h.svc.Unread(r.Context(), scope.TenantID, scope.UserID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOf(rows)
	httpx.OK(w, inboxPage{
		Envelope: httpx.Paginate(views, page.Limit, func(v NoticeView) httpx.Cursor {
			return httpx.Cursor{At: v.QueuedAt, ID: v.ID}
		}),
		Unread: unread,
	})
}

// UnreadOnly is the badge on its own, which is what a polling client asks for every few
// seconds and does not want a page of bodies for.
func (h *Handler) UnreadOnly(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	count, err := h.svc.Unread(r.Context(), scope.TenantID, scope.UserID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"unread": count})
}

// Read marks one notice. It is a POST rather than a PUT because the only field is "read", and
// a client cannot un-read its own way out of a notice it has seen.
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.MarkRead(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"id": id, "read": true})
}

// ReadAll clears the bell, and reports how many it cleared so a client can reconcile its own
// badge rather than assume it was the only reader.
func (h *Handler) ReadAll(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	count, err := h.svc.MarkAllRead(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"marked_read": count})
}

// Events is the catalogue the two configuration screens are written against.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	events, err := h.svc.Events(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]EventView, 0, len(events))
	for _, e := range events {
		views = append(views, viewOfEvent(e))
	}
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

// ListRoutes is the tenant's own routing decisions. An empty list is not an error and is not
// "muted": it means the catalogue defaults are running.
func (h *Handler) ListRoutes(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	routes, err := h.svc.Routes(r.Context(), scope.TenantID, eventKeyQuery(r))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfRoutes(routes)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

// SaveRoute is an upsert of one decision, keyed by the whole shape of it rather than by an id
// the client has to have remembered.
func (h *Handler) SaveRoute(w http.ResponseWriter, r *http.Request) {
	scope, body, err := bindRoute(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	saved, err := h.svc.SaveRoute(r.Context(), scope, body.toRoute(scope.TenantID))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfRoute(saved))
}

// DeleteRoute takes one decision away by its id.
func (h *Handler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndParam(r, "routeID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.DeleteRoute(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}

// Templates is the wording screen: the tenant's rows and the system defaults they sit on top
// of, so an override is written against something visible.
func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Templates(r.Context(), scope.TenantID, eventKeyQuery(r))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfTemplates(rows)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

// SaveTemplate is an upsert of one of the tenant's own wordings. The version the answer
// carries is the one that ran, which is what makes an old notice explicable later.
func (h *Handler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
	scope, body, err := bindTemplate(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	saved, err := h.svc.SaveTemplate(r.Context(), scope, body.toTemplate(scope.TenantID))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfTemplate(saved))
}

// DeleteTemplate removes an override, which puts the system wording back for this tenant.
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndParam(r, "templateID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.DeleteTemplate(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}

// Outbox is the queue as an operations list: newest rows, narrowed by status, channel or
// event, with the reason columns that explain a stalled one.
func (h *Handler) Outbox(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	status, err := statusQuery(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Outbox(r.Context(), OutboxFilter{
		TenantID: scope.TenantID, Status: string(status),
		Channel: trimSpace(r.URL.Query().Get("channel")), EventKey: eventKeyQuery(r),
		Limit: page.Limit + 1, Before: page.Cursor,
	})
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]OutboxView, 0, len(rows))
	for _, m := range rows {
		views = append(views, viewOfOutbox(m))
	}
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v OutboxView) httpx.Cursor {
		return httpx.Cursor{At: v.QueuedAt, ID: v.ID}
	}))
}

// Ledger is the same queue counted, which is the question an operator asks first.
func (h *Handler) Ledger(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Ledger(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]LedgerView, 0, len(rows))
	for _, row := range rows {
		views = append(views, LedgerView{Status: row.Status, Count: row.N})
	}
	httpx.OK(w, map[string]any{"items": views})
}

// Sweep runs the delivery pass now instead of waiting for the loop. It exists because an
// operator who has just configured a gateway wants to see the queue move, and because a
// deployment that runs the loop elsewhere still needs a way to say "go".
func (h *Handler) Sweep(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	report, err := h.svc.Deliver(r.Context(), scope.TenantID, deliveryBatch)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, report)
}

// Resend re-opens one failed notice and tries it immediately.
func (h *Handler) Resend(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	m, err := h.svc.Resend(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfOutbox(m))
}

func readInbox(r *http.Request) (tenancy.Scope, httpx.Page, InboxFilter, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, httpx.Page{}, InboxFilter{}, err
	}
	filter, page, err := inboxQuery(r, scope)
	return scope, page, filter, err
}

// scopeAndID is the notice endpoints: the session's tenant and one numeric id, or the answer
// is a 404 rather than a move against somebody else's row.
func scopeAndID(r *http.Request) (tenancy.Scope, int64, error) {
	return scopeAndParam(r, "notificationID")
}

func scopeAndParam(r *http.Request, name string) (tenancy.Scope, int64, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, 0, err
	}
	id, err := httpx.ID(r, name)
	if err != nil {
		return scope, 0, err
	}
	return scope, id, nil
}

func bindRoute(r *http.Request) (tenancy.Scope, RouteBody, error) {
	var body RouteBody
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, body, err
	}
	if err := httpx.Decode(r, &body); err != nil {
		return scope, body, err
	}
	if err := checkRoute(body); err != nil {
		return scope, body, err
	}
	return scope, body, nil
}

func bindTemplate(r *http.Request) (tenancy.Scope, TemplateBody, error) {
	var body TemplateBody
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, body, err
	}
	if err := httpx.Decode(r, &body); err != nil {
		return scope, body, err
	}
	if err := checkTemplate(body); err != nil {
		return scope, body, err
	}
	return scope, body, nil
}
