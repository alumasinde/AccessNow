// Command migrate applies the SQL in migrations/ to the configured database.
//
// The migrations are embedded, so the binary that ships is the binary that knows the
// schema; there is no folder to forget on the server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"accessnow/internal/platform/config"
	"accessnow/internal/platform/db"
	"accessnow/migrations"
)

// lockName is a MySQL advisory lock, so two CI jobs or two operators cannot interleave
// half a migration.
const lockName = "accessnow:migrate"

// migrationsTable is where golang-migrate records how far the schema got.
const migrationsTable = "schema_migrations"

type options struct {
	direction        string
	steps            int
	force            bool
	allowDestructive bool
	timeout          time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.direction, "direction", "up", "up or down")
	flag.IntVar(&o.steps, "steps", 0, "how many migrations to move; 0 for up means all of them")
	flag.BoolVar(&o.force, "force", false, "forget the recorded version and start over from migration 0")
	flag.BoolVar(&o.allowDestructive, "allow-destructive", false, "required before any down run")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up after this long")
	flag.Parse()

	if err := run(o); err != nil {
		log.Fatal(err)
	}
}

func run(o options) error {
	if o.direction != "up" && o.direction != "down" {
		return fmt.Errorf("direction must be up or down, got %q", o.direction)
	}
	if o.force && o.direction == "down" {
		return errors.New("-force only applies to an up run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()

	cfg, err := config.Load(ctx)
	if err != nil {
		return err
	}
	sqlDB, err := db.OpenMigration(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	release, err := lock(ctx, sqlDB)
	if err != nil {
		return err
	}
	defer release()

	if o.force {
		if err := clearVersion(ctx, sqlDB); err != nil {
			return err
		}
	}

	m, err := newMigrate(sqlDB, cfg.Database.Name)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := apply(m, o); err != nil {
		return err
	}
	report(m)
	return nil
}

// clearVersion drops the recorded version. A migration file that failed halfway leaves a
// dirty row even though the schema never changed, and golang-migrate will not move past it.
func clearVersion(ctx context.Context, sqlDB *sql.DB) error {
	_, err := sqlDB.ExecContext(ctx, "DELETE FROM `"+migrationsTable+"`")
	if err != nil {
		return fmt.Errorf("clear %s: %w", migrationsTable, err)
	}
	fmt.Printf("%s cleared; this up run starts again from migration 0\n", migrationsTable)
	return nil
}

func newMigrate(sqlDB *sql.DB, database string) (*migrate.Migrate, error) {
	source, err := iofs.New(migrations.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	target, err := mysql.WithInstance(sqlDB, &mysql.Config{
		MigrationsTable: migrationsTable,
		DatabaseName:    database,
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "mysql", target)
	if err != nil {
		return nil, fmt.Errorf("build migrate: %w", err)
	}
	return m, nil
}

// apply runs the moves. Down is refused unless it is asked for on purpose: a rollback
// drops module tables, and a table with rows in it does not come back.
func apply(m *migrate.Migrate, o options) error {
	if o.direction == "down" {
		if !o.allowDestructive {
			return errors.New("refusing to roll back without -allow-destructive")
		}
		if o.steps <= 0 {
			return errors.New("a down run needs -steps n; rolling back everything is not offered")
		}
		return move(m.Steps(-o.steps))
	}
	if o.steps > 0 {
		return move(m.Steps(o.steps))
	}
	return move(m.Up())
}

func move(err error) error {
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("nothing to do, schema is already at the target version")
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func report(m *migrate.Migrate) {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("no migrations have been applied yet")
		return
	}
	if err != nil {
		log.Printf("could not read the schema version: %v", err)
		return
	}
	if dirty {
		fmt.Printf("schema is DIRTY at version %d; fix the statement, then run up again\n", version)
		return
	}
	fmt.Printf("schema is at version %d\n", version)
}

func lock(ctx context.Context, sqlDB *sql.DB) (func(), error) {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("take a migration connection: %w", err)
	}
	var granted sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", lockName).Scan(&granted); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ask for the migration lock: %w", err)
	}
	if granted.Int64 != 1 {
		_ = conn.Close()
		return nil, errors.New("another migration holds the lock, try again")
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName)
		_ = conn.Close()
	}, nil
}
