package approval

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the desk surface for the workflow. Everything here reads or moves a chain;
// none of it decides at a barrier, and none of it writes a pass row - the pass moves through
// Core, which is the only place a pass becomes scannable.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Inbox is whose turn it is: the asks addressed to the caller by name, plus the asks
// addressed to a role they hold today. An empty inbox is a normal answer, not an error.
func (h *Handler) Inbox(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.svc.Inbox(r.Context(), scope.TenantID, scope.UserID, page.Limit)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := inboxViews(rows)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

// List is the register of chains - what a supervisor opens when they want what is late
// rather than what is theirs.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseList(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	chains, err := h.svc.ListRequests(r.Context(), scope, filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := summariesOf(chains)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v ChainSummary) httpx.Cursor {
		return httpx.Cursor{At: v.StartedAt, ID: v.ID}
	}))
}

// Get opens one chain by its own id, which is how a closed chain is read months later from
// the audit trail.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "requestID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	detail, err := h.svc.DetailByID(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, chainViewOf(scope, detail))
}

// GetByPass is the same story reached from the pass, which is the link a pass screen wants.
func (h *Handler) GetByPass(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "passID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	detail, err := h.svc.DetailByPass(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, chainViewOf(scope, detail))
}

// Decide is one approver's turn, taken. The chain answers first and the pass follows, so the
// response carries both: the chain says who is next, the pass says whether the gate is open.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "stepID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	done, decided, err := h.svc.Answer(r.Context(), scope, id, req.toDecision())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, DecisionView{Request: summaryOf(done), Pass: briefOf(decided)})
}

// Cancel is the requester taking their own pass out of the queue. The service checks it
// really is theirs; nobody else can withdraw a step-out somebody filed.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "requestID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ReasonRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	closed, err := h.svc.Cancel(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, summaryOf(closed))
}

// Reset is the edit-after-approval rule made callable: the paper changed, so the signature
// did not. The response is the fresh chain, because that is what the desk has to work now.
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "passID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ReasonRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	restarted, err := h.svc.Reset(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	detail, err := h.svc.DetailByID(r.Context(), scope, restarted.ID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, chainViewOf(scope, detail))
}

// StartChain files a chain for a pass that is still asking - the pass issued the day before
// this module was switched on, or the one a failed start left behind.
func (h *Handler) StartChain(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "passID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	detail, err := h.svc.StartChain(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, chainViewOf(scope, detail))
}

// Escalate is the timeout sweep, exposed as a POST so a site can hang a cron job or a
// supervisor button on the same code without a scheduler in this process.
func (h *Handler) Escalate(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	moved, err := h.svc.Escalate(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, moved)
}

func (h *Handler) ListFlows(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	flows, err := h.svc.ListFlows(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := flowViewsOf(flows)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

func (h *Handler) GetFlow(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "flowID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	flow, err := h.svc.Flow(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, flowViewOf(flow))
}

// SaveFlow is the one write a flow gets: the rule and its chain, together, always. POST
// files a new rule and PUT redefines an existing one; both refuse a chain with no steps.
func (h *Handler) SaveFlow(created bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := tenancy.Require(r.Context())
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		var id int64
		if !created {
			id, err = httpx.ID(r, "flowID")
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
		}
		var body FlowBody
		if err := httpx.Decode(r, &body); err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		saved, err := h.svc.SaveFlow(r.Context(), scope, body.toFlow(), body.toSteps(), id)
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		if created {
			httpx.Created(w, flowViewOf(saved))
			return
		}
		httpx.OK(w, flowViewOf(saved))
	}
}

// DeleteFlow retires a rule. 204 because there is nothing left to read back: the chains it
// already started keep running under their own snapshot.
func (h *Handler) DeleteFlow(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "flowID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.DeleteFlow(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}

// ListDelegations is the cover board. ?from_user_id= narrows it to one approver; left out,
// it is the tenant's whole board, which is what an administrator reads.
func (h *Handler) ListDelegations(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	from, err := optionalID(r.URL.Query(), "from_user_id")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	delegations, err := h.svc.ListDelegations(r.Context(), scope, from)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := delegationViewsOf(delegations)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

func (h *Handler) SaveDelegation(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var body DelegationBody
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	saved, err := h.svc.SaveDelegation(r.Context(), scope, body.toDelegation(scope))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, delegationViewOf(saved))
}

func (h *Handler) EndDelegation(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "delegationID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.EndDelegation(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}
