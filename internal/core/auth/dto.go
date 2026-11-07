package auth

import "time"

type TenantView struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type SiteView struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

// Capabilities are the backend rules the UI can safely mirror. Permissions are still
// enforced on every route; these flags answer questions a permission list cannot.
type Capabilities struct {
	SuperAdmin bool `json:"super_admin"`
	GateWork   bool `json:"gate_work"`
	ReadOnly   bool `json:"read_only"`
}

type LoginRequest struct {
	Tenant     string `json:"tenant" validate:"required,max=64"`
	Identifier string `json:"identifier" validate:"required,min=3,max=255"`
	Password   string `json:"password" validate:"required,min=1,max=1024"`
}

// Token is returned once, at login, for clients that cannot hold a cookie. The server
// only ever stores its SHA-256.
type SessionResponse struct {
	Token              string          `json:"token"`
	ExpiresAt          time.Time       `json:"expires_at"`
	MustChangePassword bool            `json:"must_change_password"`
	User               UserView        `json:"user"`
	Tenant             TenantView      `json:"tenant"`
	Site               *SiteView       `json:"site,omitempty"`
	Permissions        []string        `json:"permissions"`
	Modules            map[string]bool `json:"modules"`
	Capabilities       Capabilities    `json:"capabilities"`
	ActiveSiteID       int64           `json:"active_site_id,omitempty"`
	ActiveGateID       int64           `json:"active_gate_id,omitempty"`
}

// CurrentSession is the same bootstrap payload as login, minus the token, plus the
// authoritative scope the server resolved for this request.
type CurrentSession struct {
	User         UserView        `json:"user"`
	Scope        scopeView       `json:"scope"`
	Tenant       TenantView      `json:"tenant"`
	Site         *SiteView       `json:"site,omitempty"`
	Permissions  []string        `json:"permissions"`
	Modules      map[string]bool `json:"modules"`
	Capabilities Capabilities    `json:"capabilities"`
}

type UserView struct {
	ID           int64   `json:"id"`
	TenantID     int64   `json:"tenant_id"`
	FirstName    string  `json:"first_name"`
	LastName     string  `json:"last_name"`
	Email        *string `json:"email,omitempty"`
	Username     *string `json:"username,omitempty"`
	Status       string  `json:"status"`
	Timezone     *string `json:"preferred_timezone,omitempty"`
	IsSuperAdmin bool    `json:"is_super_admin"`
}

func view(u User) UserView {
	return UserView{
		ID: u.ID, TenantID: u.TenantID, FirstName: u.FirstName, LastName: u.LastName,
		Email: u.Email, Username: u.Username, Status: u.Status,
		Timezone: u.PreferredTimezone, IsSuperAdmin: u.IsSuperAdmin,
	}
}
