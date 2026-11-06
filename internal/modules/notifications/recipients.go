package notifications

import (
	"context"
	"fmt"
	"strings"
)

// Addressee is one resolved recipient. UserID is present for anybody with a login, and the
// two contact columns are what an email or SMS row is addressed from - a person with a login
// but no email still has an inbox, and a visitor with an email still gets told.
type Addressee struct {
	UserID   *int64
	Name     string
	Email    *string
	Phone    *string
	Locale   string
	Timezone string
}

// Address picks the way out for this channel and reports whether the recipient can be
// reached on it at all. An unaddressable recipient is skipped rather than failed: a role
// with three holders, one of whom has no phone number, should still tell the other two.
func (a Addressee) Address(channel Channel) (string, bool) {
	switch channel {
	case ChannelInApp:
		if a.UserID == nil {
			return "", false
		}
		return fmt.Sprint(*a.UserID), true
	case ChannelEmail:
		if a.Email == nil || strings.TrimSpace(*a.Email) == "" {
			return "", false
		}
		return strings.TrimSpace(*a.Email), true
	case ChannelSMS, ChannelWhatsApp:
		if a.Phone == nil || strings.TrimSpace(*a.Phone) == "" {
			return "", false
		}
		return strings.TrimSpace(*a.Phone), true
	default:
		// push and webhook address themselves from the route, not from a person.
		return "", false
	}
}

// Audience is everything one event says about who might care. It is built once per event and
// read by every recipient kind, because resolving "the requester" needs the pass row and two
// kinds resolving at once must not query it twice.
type Audience struct {
	TenantID    int64
	SiteID      int64
	ActorUserID int64

	// Pass is the row behind the event when there is one. A PassContext with no pass id means
	// the event came from somewhere with no pass, and only role and user kinds can resolve
	// from it.
	Pass PassContext

	// Notify is who the workflow engine named exactly. It is the only recipient information
	// any event payload carries, and it beats every guess a role lookup can make.
	Notify []int64

	Route Route
}

// kindRoles maps a catalogue recipient type onto the role that holds it when the pass row
// cannot answer. The names come from chk_notification_routes_recipient; site_supervisor is
// the odd one, because the catalogue allows the word and the seeded directory calls that
// role site_manager.
var kindRoles = map[string]string{
	"approver":           "approver",
	"gate_supervisor":    "gate_supervisor",
	"site_supervisor":    "site_manager",
	"tenant_admin":       "tenant_admin",
	"contractor_manager": "contractor_manager",
	"host":               "host",
	"requester":          "requester",
	"department_head":    "department_head",
}

// Recipients turns one recipient kind into people. The order is always the same: whoever the
// event named, then whoever the paper names, then whoever holds the role. That is what lets
// an event be precise without every module having to be.
func (r *Repository) Recipients(ctx context.Context, kind string, channel Channel,
	a Audience) ([]Addressee, error) {
	switch kind {
	case "user":
		if a.Route.RecipientUserID == nil {
			return nil, nil
		}
		return r.people(ctx, a.TenantID, []int64{*a.Route.RecipientUserID})

	case "role":
		roleKey := derefString(a.Route.RecipientRoleKey)
		if roleKey == "" {
			return nil, nil
		}
		return r.rolePeople(ctx, a.TenantID, roleKey, a.SiteID)

	case "static":
		// A route's own address, which is how a tenant tells an address that is not anybody's
		// login: a duty phone, a security desk mailbox.
		address := derefString(a.Route.RecipientAddress)
		if address == "" {
			return nil, nil
		}
		return []Addressee{staticAddressee(address)}, nil

	case "approver":
		if len(a.Notify) > 0 {
			return r.people(ctx, a.TenantID, a.Notify)
		}
		return r.rolePeople(ctx, a.TenantID, kindRoles[kind], a.SiteID)

	case "requester":
		if id := firstID(a.Pass.Requester, a.Pass.CreatedBy); id != 0 {
			return r.people(ctx, a.TenantID, []int64{id})
		}
		return r.rolePeople(ctx, a.TenantID, kindRoles[kind], a.SiteID)

	case "host":
		if a.Pass.Host != nil {
			return r.people(ctx, a.TenantID, []int64{*a.Pass.Host})
		}
		return r.rolePeople(ctx, a.TenantID, kindRoles[kind], a.SiteID)

	case "department_head":
		if a.Pass.DepartmentHead != nil {
			return r.people(ctx, a.TenantID, []int64{*a.Pass.DepartmentHead})
		}
		return r.rolePeople(ctx, a.TenantID, kindRoles[kind], a.SiteID)

	case "actor":
		// Not a recipient_type a route row can hold: it comes from include_actor on the
		// route plus the actor on the event, which is the only place that id is ever written.
		if a.ActorUserID == 0 {
			return nil, nil
		}
		return r.people(ctx, a.TenantID, []int64{a.ActorUserID})

	case "visitor":
		return r.visitor(ctx, channel, a)

	case "gate_supervisor", "site_supervisor", "tenant_admin", "contractor_manager":
		return r.rolePeople(ctx, a.TenantID, kindRoles[kind], a.SiteID)

	default:
		return nil, nil
	}
}

// visitor is the one recipient kind with no login. The person is named on the pass itself,
// so the notice goes to whatever address the guard typed at the gate and never to an inbox.
func (r *Repository) visitor(ctx context.Context, channel Channel, a Audience) ([]Addressee, error) {
	if a.Pass.PassID == 0 {
		return nil, nil
	}
	out := Addressee{Name: a.Pass.SubjectName}
	switch channel {
	case ChannelEmail:
		out.Email = a.Pass.SubjectEmail
	case ChannelSMS, ChannelWhatsApp:
		out.Phone = a.Pass.SubjectPhone
	default:
		// There is no in-app notice for a person who cannot sign in.
		return nil, nil
	}
	if _, ok := out.Address(channel); !ok {
		return nil, nil
	}
	return []Addressee{out}, nil
}

func (r *Repository) people(ctx context.Context, tenantID int64, ids []int64) ([]Addressee, error) {
	people, err := r.People(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Addressee, 0, len(people))
	for _, p := range people {
		id := p.UserID
		out = append(out, Addressee{UserID: &id, Name: p.Name, Email: p.Email,
			Phone: p.Phone, Locale: p.Locale, Timezone: p.Timezone})
	}
	return out, nil
}

func (r *Repository) rolePeople(ctx context.Context, tenantID int64, roleKey string,
	siteID int64) ([]Addressee, error) {
	if roleKey == "" {
		return nil, nil
	}
	holders, err := r.RoleHolders(ctx, tenantID, roleKey, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]Addressee, 0, len(holders))
	for _, p := range holders {
		id := p.UserID
		out = append(out, Addressee{UserID: &id, Name: p.Name, Email: p.Email,
			Phone: p.Phone, Locale: p.Locale, Timezone: p.Timezone})
	}
	return out, nil
}

// staticAddressee sorts a typed-in address by what it looks like rather than by asking the
// tenant to declare it: a duty mailbox has an @ in it and a duty phone does not. Address()
// then refuses it on the wrong channel, so a mis-keyed route loses its notice rather than
// mail-merging a phone number.
func staticAddressee(address string) Addressee {
	out := Addressee{Locale: "en", Name: address}
	if strings.ContainsRune(address, '@') {
		out.Email = &address
	} else {
		out.Phone = &address
	}
	return out
}

func firstID(candidates ...*int64) int64 {
	for _, c := range candidates {
		if c != nil && *c != 0 {
			return *c
		}
	}
	return 0
}
