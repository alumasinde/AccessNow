package notifications

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/platform/audit"
)

// Service is the reactor: it reads an outbox event and files notices. Delivery is a
// separate concern (deliver.go) because a queued row must survive the gateway being down,
// and nothing that happens at a gate may wait on an SMTP handshake.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	access *access.Service

	// chain records the configuration changes and the operator resends: this module decides
	// who was told about an incident, so the settings are worth a trail of their own.
	chain audit.Writer

	// providers turns a tenant's provider rows into senders and remembers them, so a sweep
	// of four hundred sms rows dials the gateway four hundred times and builds its client
	// once.
	providers *senders

	// now is a field so a test can queue notices at a fixed second and assert the retry
	// ladder it got, rather than racing the clock.
	now func() time.Time
}

// NewService wires the reactor. secret is how a provider row's credentials_ref is read; nil
// means the environment under NOTIFY_SECRET_, which is the only place a credential belongs.
func NewService(pool *sqlx.DB, repo *Repository, accessSvc *access.Service,
	chain audit.Writer, secret Secret) *Service {
	return &Service{
		pool:      pool,
		repo:      repo,
		access:    accessSvc,
		chain:     chain,
		providers: newSenders(secret),
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// enabled is the module switch. A tenant that turned notifications off is owed nothing: the
// event still sits on the outbox for whoever else cares.
func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}
