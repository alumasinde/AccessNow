package integrations

import (
	"net/http"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Header the machine surface presents its credential in. It is not Authorization: that header
// belongs to a person's session, and mixing the two would make one leak look like the other.
const keyHeader = "X-API-Key"

// Handler is the integrations desk and the machine surface behind it.
type Handler struct {
	svc    *Service
	now    func() time.Time
	limits rateLimiter
}

func NewHandler(svc *Service) *Handler {
	h := &Handler{svc: svc, now: func() time.Time { return time.Now().UTC() }}
	if svc != nil && svc.pool != nil {
		h.limits = newDBKeyLimiter(svc.pool)
	}
	return h
}

// RequireKey authenticates a machine caller. It stamps a scope with the key's tenant and no
// user: a credential is not a person, and a permission check for a person must never pass on
// one.
func (h *Handler) RequireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := h.svc.Authenticate(r.Context(), r.Header.Get(keyHeader))
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		ctx := tenancy.Put(r.Context(), tenancy.Scope{
			TenantID: key.TenantID, AuthMethod: "api_key",
		})
		ctx = withKey(ctx, key)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WhoAmI lets a machine confirm the credential it holds, its scopes and its environment before
// it tries anything else.
func (h *Handler) WhoAmI(w http.ResponseWriter, r *http.Request) {
	key, ok := keyFrom(r.Context())
	if !ok {
		httpx.Fail(r.Context(), w, r, errs.Unauthenticated("api key is required"))
		return
	}
	httpx.OK(w, keyView(key, h.now()))
}

func (h *Handler) IssueKey(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req KeyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	key, secret, err := h.svc.IssueKey(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, IssuedKeyView{KeyView: keyView(key, h.now()), Secret: secret})
}

func (h *Handler) Keys(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	keys, err := h.svc.Keys(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": keyViews(keys, h.now())})
}

func (h *Handler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "keyID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RevokeKeyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	key, err := h.svc.RevokeKey(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, keyView(key, h.now()))
}

func (h *Handler) Webhooks(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hooks, err := h.svc.Webhooks(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": webhookViews(hooks)})
}

func (h *Handler) GetWebhook(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "endpointID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hook, err := h.svc.Webhook(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, webhookView(hook))
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req WebhookRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hook, err := h.svc.CreateWebhook(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, webhookView(hook))
}

func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "endpointID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req WebhookRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hook, err := h.svc.UpdateWebhook(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, webhookView(hook))
}

func (h *Handler) RetireWebhook(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "endpointID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.RetireWebhook(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) TestWebhook(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "endpointID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	delivery, err := h.svc.TestWebhook(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, deliveryView(delivery))
}

func (h *Handler) Deliveries(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	endpointID, err := httpx.QueryID(r, "endpoint_id")
	if err != nil && r.URL.Query().Get("endpoint_id") != "" {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Deliveries(r.Context(), scope.TenantID, endpointID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": deliveryViews(rows)})
}

func (h *Handler) EnqueueCommand(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req CommandRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	command, err := h.svc.EnqueueCommand(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, commandView(command))
}

func (h *Handler) Commands(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	status, err := ParseCommandStatus(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	commands, err := h.svc.Commands(r.Context(), scope.TenantID, status)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": commandViews(commands)})
}

// GateCommands is the tablet's poll for actuator work. The scope was put there by the device
// middleware, so the handler never reads a gate id from the request.
func (h *Handler) GateCommands(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	commands, err := h.svc.GateCommands(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": commandViews(commands)})
}

// GateAckCommand closes one instruction from the barrier that executed it.
func (h *Handler) GateAckCommand(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "commandID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req AckRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	command, err := h.svc.AckGateCommand(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, commandView(command))
}

func (h *Handler) AckCommand(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "commandID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req AckRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	command, err := h.svc.AckCommand(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, commandView(command))
}
