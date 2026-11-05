package auth

import "time"

type LoginRequest struct {
	Tenant     string `json:"tenant" validate:"required,max=64"`
	Identifier string `json:"identifier" validate:"required,min=3,max=255"`
	Password   string `json:"password" validate:"required,min=1,max=1024"`
}

// Token is returned once, at login, for clients that cannot hold a cookie. The server
// only ever stores its SHA-256.
type SessionResponse struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expires_at"`
	MustChangePassword bool      `json:"must_change_password"`
	User               UserView  `json:"user"`
	ActiveSiteID       int64     `json:"active_site_id,omitempty"`
	ActiveGateID       int64     `json:"active_gate_id,omitempty"`
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
