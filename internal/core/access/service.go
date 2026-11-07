package access

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

const wildcard = "*"

// Service answers module and permission questions from a short lived cache: every
// authenticated request asks at least once, and a toggle takes effect within the TTL
// instead of needing a restart.
type Service struct {
	repo  *Repository
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]entry
}

type entry struct {
	expiresAt time.Time
	values    map[string]bool
}

func NewService(repo *Repository, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Service{repo: repo, ttl: ttl, cache: make(map[string]entry)}
}

func (s *Service) Enabled(ctx context.Context, tenantID int64, moduleKey string) (bool, error) {
	values, err := s.load("modules:"+key(tenantID), func() (map[string]bool, error) {
		return s.repo.EnabledModules(ctx, tenantID)
	})
	if err != nil {
		return false, err
	}
	return values[moduleKey], nil
}

func (s *Service) Can(ctx context.Context, tenantID, userID int64, permission string) (bool, error) {
	if tenantID == 0 || userID == 0 {
		return false, errs.Unauthenticated("request carries no identity")
	}
	values, err := s.load("permissions:"+key(tenantID)+":"+key(userID), func() (map[string]bool, error) {
		return s.repo.Permissions(ctx, tenantID, userID)
	})
	if err != nil {
		return false, err
	}
	return values[permission] || values[wildcard], nil
}

// Permissions is the complete grant set the UI uses to decide which surfaces to show.
// Route guards still ask Can for every request; this is not an authorization shortcut.
func (s *Service) Permissions(ctx context.Context, tenantID, userID int64) ([]string, error) {
	if tenantID == 0 || userID == 0 {
		return nil, errs.Unauthenticated("request carries no identity")
	}
	values, err := s.load("permissions:"+key(tenantID)+":"+key(userID), func() (map[string]bool, error) {
		return s.repo.Permissions(ctx, tenantID, userID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for permission, granted := range values {
		if granted {
			out = append(out, permission)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Modules is the tenant's full switchboard, including disabled entries, so the UI can
// distinguish "not enabled" from "no access".
func (s *Service) Modules(ctx context.Context, tenantID int64) (map[string]bool, error) {
	return s.load("modules:"+key(tenantID), func() (map[string]bool, error) {
		return s.repo.EnabledModules(ctx, tenantID)
	})
}

func (s *Service) DoesGateWork(ctx context.Context, tenantID, userID int64) (bool, error) {
	return s.repo.GateWork(ctx, tenantID, userID)
}

func (s *Service) Auditor(ctx context.Context, tenantID, userID int64) (bool, error) {
	return s.repo.IsAuditor(ctx, tenantID, userID)
}

// RequirePermission guards a state transition a caller could otherwise talk their way
// through by picking a different endpoint.
func (s *Service) RequirePermission(ctx context.Context, scope tenancy.Scope, permission string) error {
	if scope.IsSuperAdmin {
		return nil
	}
	ok, err := s.Can(ctx, scope.TenantID, scope.UserID, permission)
	if err != nil {
		return err
	}
	if !ok {
		return errs.Forbidden("permission " + permission + " is required")
	}
	return nil
}

// Invalidate drops cached answers for a tenant after a role, module or gate change.
func (s *Service) Invalidate(tenantID int64) {
	moduleKey := "modules:" + key(tenantID)
	userPrefix := "permissions:" + key(tenantID) + ":"
	s.mu.Lock()
	defer s.mu.Unlock()
	for cached := range s.cache {
		if cached == moduleKey || strings.HasPrefix(cached, userPrefix) {
			delete(s.cache, cached)
		}
	}
}

func (s *Service) load(cacheKey string, fetch func() (map[string]bool, error)) (map[string]bool, error) {
	now := time.Now()
	s.mu.RLock()
	cached, found := s.cache[cacheKey]
	s.mu.RUnlock()
	if found && now.Before(cached.expiresAt) {
		return cached.values, nil
	}

	values, err := fetch()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[cacheKey] = entry{expiresAt: now.Add(s.ttl), values: values}
	s.mu.Unlock()
	return values, nil
}

func key(parts ...int64) string {
	out := make([]byte, 0, 20*len(parts))
	for _, part := range parts {
		out = append(out, strconv.FormatInt(part, 10)...)
		out = append(out, ':')
	}
	return string(out)
}
