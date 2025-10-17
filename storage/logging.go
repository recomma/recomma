package storage

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/terwey/recomma/storage/sqlcgen"
)

type loggingDB struct {
	inner  sqlcgen.DBTX
	logger *slog.Logger
}

func (l loggingDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := l.inner.ExecContext(ctx, query, args...)
	l.logger.Log(ctx, slog.LevelInfo, "sql exec",
		slog.String("query", query),
		slog.Any("args", args),
		slog.Duration("duration", time.Since(start)),
		slog.Any("err", err),
	)
	return res, err
}

func (l loggingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.inner.QueryContext(ctx, query, args...)
	l.logger.Log(ctx, slog.LevelInfo, "sql query",
		slog.String("query", query),
		slog.Any("args", args),
		slog.Duration("duration", time.Since(start)),
		slog.Any("err", err),
	)
	return rows, err
}

func (l loggingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	l.logger.Log(ctx, slog.LevelInfo, "sql query row",
		slog.String("query", query),
		slog.Any("args", args),
	)
	return l.inner.QueryRowContext(ctx, query, args...)
}

func (l loggingDB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	start := time.Now()
	stmt, err := l.inner.PrepareContext(ctx, query)
	l.logger.Log(ctx, slog.LevelInfo, "sql prepare",
		slog.String("query", query),
		slog.Duration("duration", time.Since(start)),
		slog.Any("err", err),
	)
	return stmt, err
}
