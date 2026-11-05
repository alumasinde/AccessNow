package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sethvargo/go-envconfig"
)

type Database struct {
	Host            string        `env:"HOST,overwrite,default=127.0.0.1"`
	Port            int           `env:"PORT,overwrite,default=3306"`
	User            string        `env:"USER,overwrite"`
	Password        string        `env:"PASSWORD,overwrite"`
	Name            string        `env:"NAME,overwrite"`
	MaxOpenConns    int           `env:"MAX_OPEN_CONNS,overwrite,default=32"`
	MaxIdleConns    int           `env:"MAX_IDLE_CONNS,overwrite,default=8"`
	ConnMaxLifetime time.Duration `env:"CONN_MAX_LIFETIME,overwrite,default=30m"`
}

type Config struct {
	Environment    string        `env:"APP_ENV,overwrite,default=development"`
	HTTPAddr       string        `env:"HTTP_ADDR,overwrite,default=:8080"`
	ReadTimeout    time.Duration `env:"HTTP_READ_TIMEOUT,overwrite,default=15s"`
	WriteTimeout   time.Duration `env:"HTTP_WRITE_TIMEOUT,overwrite,default=45s"`
	HandlerTimeout time.Duration `env:"HTTP_HANDLER_TIMEOUT,overwrite,default=20s"`
	SessionTTL     time.Duration `env:"SESSION_TTL,overwrite,default=12h"`
	CookieSecure   bool          `env:"COOKIE_SECURE,overwrite,default=false"`
	AllowedOrigins []string      `env:"ALLOWED_ORIGINS,overwrite,delimiter=\\,"`
	LoginAttempts  int           `env:"LOGIN_ATTEMPTS_PER_MINUTE,overwrite,default=10"`
	ScanRate       int           `env:"SCAN_REQUESTS_PER_MINUTE,overwrite,default=300"`
	Database       Database      `env:",prefix=DB_,overwrite"`
}

func Load(ctx context.Context) (Config, error) {
	var c Config
	if err := loadDotenv(".env"); err != nil {
		return c, err
	}
	if err := envconfig.Process(ctx, &c); err != nil {
		return c, fmt.Errorf("read environment: %w", err)
	}
	if err := c.check(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) Production() bool { return c.Environment == "production" }

func (c Config) check() error {
	var missing []string
	if strings.TrimSpace(c.Database.User) == "" {
		missing = append(missing, "DB_USER")
	}
	if strings.TrimSpace(c.Database.Name) == "" {
		missing = append(missing, "DB_NAME")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}
