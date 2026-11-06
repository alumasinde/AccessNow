// Command devseed provisions one tenant you can actually log into: roles cloned from the
// templates, a site, a default gate, an admin and a guard, an enrolled gate device, and the
// starter pass number formats. Every step is idempotent, so re-run it after a schema reload.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"accessnow/internal/platform/config"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/logging"
)

type settings struct {
	slug          string
	name          string
	adminEmail    string
	adminPassword string
	guardPassword string
}

func main() {
	var s settings
	flag.StringVar(&s.slug, "slug", "demo", "tenant slug, also the username prefix")
	flag.StringVar(&s.name, "name", "Demo Gate", "tenant display name")
	flag.StringVar(&s.adminEmail, "admin-email", "admin@example.test", "login email for the seeded admin")
	flag.StringVar(&s.adminPassword, "admin-password", "DevAdmin2026!", "password for the seeded admin")
	flag.StringVar(&s.guardPassword, "guard-password", "DevGuard2026!", "password for the seeded gate guard")
	flag.Parse()

	if err := run(s); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run(s settings) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := config.Load(ctx)
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Environment)
	slog.SetDefault(logger)

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	out, err := provision(ctx, pool, s)
	if err != nil {
		return err
	}
	report(s, out)
	return nil
}

func report(s settings, out outcome) {
	fmt.Printf(`tenant  %d  %q  (slug %s)
site    %d  HQ
gate    %d  MAIN, offline capable

admin   username %s-admin  email %s  password %s
guard   username %s-guard  password %s   works gate %d
device  token %s
        send it as X-Device-Token to /api/v1/gate/*; it is shown once, only here.

approvals - three flows, one cover, all seeded for tenant %d:
        username %s-supervisor  password %s  holds gate_supervisor, answers the queue
        username %s-manager     password %s  holds site_manager, signs the expensive ones
        the supervisor is covered by %s-admin for the next week.

notifications - email and sms use the log provider, so a notice is written to the API log:
        GET /api/v1/notifications is the supervisor's inbox; goods releases are routed to the
        requester, and an overdue step-out also mails duty@%s.example.

next:
  go run ./cmd/api
  curl -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
       -d '{"tenant":"%s","identifier":"%s-admin","password":"%s"}'
`, out.TenantID, s.name, s.slug, out.SiteID, out.GateID,
		s.slug, s.adminEmail, s.adminPassword,
		s.slug, s.guardPassword, out.GateID, out.DeviceToken,
		out.TenantID, s.slug, s.guardPassword, s.slug, s.guardPassword, s.slug,
		s.slug, s.slug, s.slug, s.adminPassword)
}
