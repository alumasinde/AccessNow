package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/config"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/security"
	"accessnow/internal/platform/tenancy"
)

const (
	maxFailedLogins = 5
	cookieName      = "accessnow_session"
)

// Hashed once so a wrong identifier costs the same as a wrong password; without it the
// response time tells an attacker whether the email exists.
var dummyHash = func() string {
	hash, err := security.HashPassword("timing-equaliser")
	if err != nil {
		panic("security: cannot build timing equaliser: " + err.Error())
	}
	return hash
}()

// ShiftCloser is the gate service's side of ending a session. It is an interface here
// because gates already imports auth for its user repository; naming the behaviour rather
// than the type keeps the two packages acyclic.
type ShiftCloser interface {
	CloseShiftForUser(ctx context.Context, scope tenancy.Scope, reason string) error
}

type Service struct {
	pool  *sqlx.DB
	repo  *Repository
	chain audit.Writer
	ttl   time.Duration
	lock  time.Duration
	shift ShiftCloser
}

func NewService(pool *sqlx.DB, repo *Repository, chain audit.Writer, c config.Config) *Service {
	return &Service{pool: pool, repo: repo, chain: chain, ttl: c.SessionTTL, lock: 15 * time.Minute}
}

type LoginInput struct {
	TenantSlug string
	Identifier string
	Password   string
	IPAddress  string
	UserAgent  string
}

type LoginResult struct {
	Token              string
	User               User
	Scope              tenancy.Scope
	Tenant             Tenant
	Site               *Site
	MustChangePassword bool
	ExpiresAt          time.Time
}

func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	tenant, err := s.repo.TenantBySlug(ctx, in.TenantSlug)
	if err != nil {
		// An unknown workspace must cost the same and read the same as a wrong password, or the
		// login form becomes a way to list which tenants exist.
		if errs.CodeOf(err) == errs.CodeNotFound {
			security.VerifyPassword(in.Password, dummyHash)
			return LoginResult{}, errs.Unauthenticated("email or password is wrong")
		}
		return LoginResult{}, err
	}

	user, err := s.repo.FindByIdentifier(ctx, tenant.ID, in.Identifier)
	if err != nil {
		if errs.CodeOf(err) != errs.CodeNotFound {
			return LoginResult{}, err
		}
		security.VerifyPassword(in.Password, dummyHash)
		return LoginResult{}, errs.Unauthenticated("email or password is wrong")
	}

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now().UTC()) {
		return LoginResult{}, errs.New(errs.CodeTooManyRequests, "account is temporarily locked, try again later")
	}
	// The password is checked before the account's status is disclosed, so a suspended account
	// is only revealed to someone who holds its password.
	if user.PasswordHash == nil || !security.VerifyPassword(in.Password, *user.PasswordHash) {
		if user.PasswordHash == nil {
			security.VerifyPassword(in.Password, dummyHash)
		}
		s.recordFailure(ctx, user)
		return LoginResult{}, errs.Unauthenticated("email or password is wrong")
	}
	if !user.Loginable() {
		return LoginResult{}, errs.Forbidden("this account is " + user.Status)
	}

	site, err := s.repo.FirstSite(ctx, tenant.ID)
	if err != nil {
		return LoginResult{}, err
	}
	var siteRow *Site
	if site != nil {
		found, err := s.repo.SiteByID(ctx, tenant.ID, *site)
		if err != nil {
			return LoginResult{}, err
		}
		siteRow = &found
	}
	token, err := security.NewToken()
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := time.Now().UTC().Add(s.ttl)

	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.RecordLogin(ctx, user.ID); err != nil {
			return err
		}
		sessionID, err := repo.CreateSession(ctx, NewSession{
			TenantID: tenant.ID, UserID: user.ID, TokenHash: security.HashToken(token),
			SiteID: site, AuthMethod: "password", IPAddress: in.IPAddress, UserAgent: in.UserAgent,
			ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: tenant.ID, ActorUserID: user.ID, Action: "auth.login",
			SubjectType: "user", SubjectID: user.ID, SiteID: derefInt64(site),
			IPAddress: in.IPAddress, UserAgent: in.UserAgent,
			After: map[string]any{"session_id": sessionID},
		})
	})
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Token:              token,
		User:               user,
		Tenant:             tenant,
		Site:               siteRow,
		MustChangePassword: user.MustChangePassword,
		ExpiresAt:          expiresAt,
		Scope: tenancy.Scope{
			TenantID: tenant.ID, UserID: user.ID, SiteID: derefInt64(site),
			Timezone: tenant.Timezone, AuthMethod: "password", IsSuperAdmin: user.IsSuperAdmin,
		},
	}, nil
}

func (s *Service) recordFailure(ctx context.Context, user User) {
	lockedUntil := time.Now().UTC().Add(s.lock)
	if err := s.repo.RecordFailure(ctx, user.ID, maxFailedLogins, lockedUntil); err != nil {
		logging.From(ctx).ErrorContext(ctx, "could not record a failed login", "error", err)
	}
	if err := s.chain.Write(ctx, s.pool, audit.Entry{
		TenantID: user.TenantID, ActorUserID: user.ID, Action: "auth.login_failed",
		SubjectType: "user", SubjectID: user.ID,
		Reason: fmt.Sprintf("attempt %d of %d", user.FailedAttempts+1, maxFailedLogins),
	}); err != nil {
		logging.From(ctx).ErrorContext(ctx, "audit failed", "error", err)
	}
}

// Resolve turns a token into a request scope. It re-reads the user on every call so a
// revoked guard stops working at the gate instead of at session expiry.
func (s *Service) Resolve(ctx context.Context, token string) (tenancy.Scope, error) {
	session, err := s.repo.SessionByTokenHash(ctx, security.HashToken(token))
	if err != nil {
		return tenancy.Scope{}, err
	}
	if !session.Live(time.Now().UTC()) {
		if s.shift != nil {
			_ = s.shift.CloseShiftForUser(ctx, tenancy.Scope{
				TenantID: session.TenantID, UserID: session.UserID, SessionID: session.ID,
				GateID: derefInt64(session.ActiveGateID),
			}, "timeout")
		}
		return tenancy.Scope{}, errs.Unauthenticated("session has expired")
	}
	user, err := s.repo.UserByID(ctx, session.UserID)
	if err != nil {
		return tenancy.Scope{}, err
	}
	if user.Status != "active" {
		return tenancy.Scope{}, errs.Forbidden("this account is " + user.Status)
	}

	return tenancy.Scope{
		TenantID: session.TenantID, UserID: user.ID, SessionID: session.ID,
		SiteID: derefInt64(session.ActiveSiteID), GateID: derefInt64(session.ActiveGateID),
		DeviceID: derefInt64(session.DeviceID), Timezone: session.TenantTimezone,
		AuthMethod: session.AuthMethod, IsSuperAdmin: user.IsSuperAdmin,
	}, nil
}

// SetShiftCloser binds the gate service after both exist. An unbound one leaves logout
// exactly as it was: the session ends and the shift row is closed by the next scan.
func (s *Service) SetShiftCloser(closer ShiftCloser) { s.shift = closer }

func (s *Service) Current(ctx context.Context, scope tenancy.Scope) (User, error) {
	return s.repo.UserByID(ctx, scope.UserID)
}

func (s *Service) TenantByID(ctx context.Context, id int64) (Tenant, error) {
	return s.repo.TenantByID(ctx, id)
}

func (s *Service) SiteByID(ctx context.Context, tenantID, siteID int64) (*Site, error) {
	if siteID == 0 {
		return nil, nil
	}
	site, err := s.repo.SiteByID(ctx, tenantID, siteID)
	if err != nil {
		return nil, err
	}
	return &site, nil
}

func (s *Service) Logout(ctx context.Context, scope tenancy.Scope) error {
	if scope.SessionID == 0 {
		return nil
	}
	// The shift is closed before the session is revoked, so the reason is filed while the
	// session that owns it is still the caller's. A failure here must not keep a person
	// signed in.
	if s.shift != nil {
		if err := s.shift.CloseShiftForUser(ctx, scope, "logout"); err != nil {
			logging.From(ctx).ErrorContext(ctx, "close the gate shift on logout", "error", err)
		}
	}
	if err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		if err := s.repo.In(c).RevokeSession(ctx, scope.SessionID, "logout"); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "auth.logout",
			SubjectType: "user", SubjectID: scope.UserID, GateID: scope.GateID,
		})
	}); err != nil {
		return err
	}
	return nil
}

func CookieName() string { return cookieName }

func Cookie(ttl time.Duration, secure bool) *http.Cookie {
	return &http.Cookie{
		Name: cookieName, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	}
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
