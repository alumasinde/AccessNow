package logging

import (
	"context"
	"log/slog"
	"os"
)

type loggerKey struct{}

func New(environment string) *slog.Logger {
	level := slog.LevelInfo
	if environment != "production" {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if environment == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}

// With puts a logger into the request context so a handler, service and repository
// all write to the same record.
func With(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

func Add(ctx context.Context, attrs ...slog.Attr) context.Context {
	values := make([]any, len(attrs))
	for i, attr := range attrs {
		values[i] = attr
	}
	return With(ctx, From(ctx).With(values...))
}

func Tenant(id int64) slog.Attr   { return slog.Int64("tenant_id", id) }
func User(id int64) slog.Attr     { return slog.Int64("user_id", id) }
func Gate(id int64) slog.Attr     { return slog.Int64("gate_id", id) }
func Pass(id int64) slog.Attr     { return slog.Int64("pass_id", id) }
func Request(id string) slog.Attr { return slog.String("request_id", id) }
