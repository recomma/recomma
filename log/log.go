package log

import (
	"context"
	"log/slog"
)

type ctxLoggerKey struct{}

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxLoggerKey{}, logger)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxLoggerKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}
