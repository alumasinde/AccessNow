package approval

import (
	"context"

	"accessnow/internal/platform/events"
)

// OnPassIssued is the reaction that starts a chain, and the only reason this module hears
// about a pass at all.
//
// internal/server registers it against "pass.issued", which Core publishes inside the
// issuer's own transaction. So visitor, goods, vehicle and staff each get the tenant's
// workflow without any of them naming this module, and a flow a tenant writes this afternoon
// applies to the pass filed tomorrow morning without a deploy.
func (s *Service) OnPassIssued(ctx context.Context, e events.Event) error {
	if e.PassID == 0 {
		return nil
	}
	return s.Start(ctx, e.TenantID, e.PassID)
}
