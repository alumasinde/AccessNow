package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/config"
)

// Conn is satisfied by *sqlx.DB and *sqlx.Tx alike, so a repository can be bound to
// a pool or to the transaction that is already in flight without a second constructor.
type Conn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
	QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
	NamedExecContext(ctx context.Context, query string, arg any) (sql.Result, error)
}

func Open(ctx context.Context, c config.Database) (*sqlx.DB, error) {
	sqlDB, err := sql.Open("mysql", DSN(c))
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDB.SetMaxOpenConns(c.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(c.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return sqlx.NewDb(sqlDB, "mysql"), nil
}

// OpenMigration returns a single-connection pool for cmd/migrate only. golang-migrate
// submits each file as one statement, so multiStatements has to be on here; the API pool
// deliberately leaves it off, because a request path that can chain statements is an
// injection waiting to happen.
func OpenMigration(ctx context.Context, c config.Database) (*sql.DB, error) {
	cfg := mysqlConfig(c)
	cfg.Params["multiStatements"] = "true"

	sqlDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql for migrations: %w", err)
	}
	// Two, not one: the advisory lock in cmd/migrate holds a connection for the whole run
	// and golang-migrate needs another. A pool of one would wait on itself forever.
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(c.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping mysql for migrations: %w", err)
	}
	return sqlDB, nil
}

// Transact runs fn on a read-committed transaction and rolls back on any error.
// Keep the work inside fn short: pass_no allocation holds a row lock for its length.
func Transact(ctx context.Context, db *sqlx.DB, fn func(Conn) error) error {
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// IsDuplicate reports a unique-key violation. Every register in this system is protected by
// a UNIQUE key rather than by a read-then-write in the service, so a caller that lost the
// race gets a conflict naming the row, not a 500 with an error number in it.
func IsDuplicate(err error) bool {
	var myerr *mysql.MySQLError
	if !errors.As(err, &myerr) {
		return false
	}
	return myerr.Number == 1062
}

func DSN(c config.Database) string {
	return mysqlConfig(c).FormatDSN()
}

// RowsChanged reports whether a compare-and-set update took the row. Zero rows means the
// row had already moved, which is how a writer tells its own first attempt from a second
// delivery of the same thing - and how an allocator notices a number it no longer owns.
func RowsChanged(result sql.Result) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read rows affected: %w", err)
	}
	return affected > 0, nil
}

func mysqlConfig(c config.Database) *mysql.Config {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", c.Host, c.Port)
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.DBName = c.Name
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 10 * time.Second
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"time_zone": "'+00:00'",
	}
	return cfg
}
