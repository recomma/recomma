package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/recomma/recomma/pkg/sqllogger"
	"github.com/stretchr/testify/require"
)

func TestStorageLogInsertFuncPersistsRecords(t *testing.T) {
	t.Parallel()

	store := newTestStorage(t)

	handler, err := sqllogger.NewHandler(
		sqllogger.WithInsertFunc(store.LogInsertFunc()),
		sqllogger.WithQueueSize(8),
		sqllogger.WithMinLevel(slog.LevelDebug),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, handler.Close(context.Background()))
	})

	ctx := context.Background()
	logger := handler.WithGroup("engine").WithGroup("filltracker").WithAttrs([]slog.Attr{slog.String("static", "value")})
	record := slog.NewRecord(time.Unix(1_700_000_000, 0), slog.LevelInfo, "persist to storage", 0)
	record.AddAttrs(slog.Float64("ratio", 1.5))
	require.NoError(t, logger.Handle(ctx, record))
	require.NoError(t, handler.Close(ctx))

	var (
		level, scope, message string
		attrsJSON             []byte
	)
	err = store.db.QueryRowContext(ctx, `SELECT level, scope, message, attrs FROM app_logs ORDER BY id DESC LIMIT 1`).
		Scan(&level, &scope, &message, &attrsJSON)
	require.NoError(t, err)
	require.Equal(t, "INFO", level)
	require.Equal(t, "engine.filltracker", scope)
	require.Equal(t, "persist to storage", message)

	var attrs map[string]any
	require.NoError(t, json.Unmarshal(attrsJSON, &attrs))
	engine, ok := attrs["engine"].(map[string]any)
	require.True(t, ok, "expected engine group in attrs")
	filltracker, ok := engine["filltracker"].(map[string]any)
	require.True(t, ok, "expected filltracker group in attrs")
	require.Equal(t, "value", filltracker["static"])
	require.Equal(t, 1.5, filltracker["ratio"])
}

func TestStorageLogInsertFuncCapturesSourceMetadata(t *testing.T) {
	t.Parallel()

	store := newTestStorage(t)
	handler, err := sqllogger.NewHandler(
		sqllogger.WithInsertFunc(store.LogInsertFunc()),
		sqllogger.WithQueueSize(4),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, handler.Close(context.Background()))
	})

	ctx := context.Background()
	record := slog.NewRecord(time.Now(), slog.LevelWarn, "with source metadata", callerPC())
	require.NoError(t, handler.Handle(ctx, record))
	require.NoError(t, handler.Close(ctx))

	var (
		sourceFile sql.NullString
		sourceLine sql.NullInt64
		sourceFunc sql.NullString
	)
	err = store.db.QueryRowContext(ctx, `SELECT source_file, source_line, source_func FROM app_logs ORDER BY id DESC LIMIT 1`).
		Scan(&sourceFile, &sourceLine, &sourceFunc)
	require.NoError(t, err)
	require.True(t, sourceFile.Valid && sourceFile.String != "", "expected source file to be stored")
	require.True(t, sourceLine.Valid && sourceLine.Int64 > 0, "expected positive source line")
	require.True(t, sourceFunc.Valid && sourceFunc.String != "", "expected source function to be stored")
}

func callerPC() uintptr {
	var pcs [1]uintptr
	n := runtime.Callers(2, pcs[:])
	if n == 0 {
		return 0
	}
	return pcs[0]
}
