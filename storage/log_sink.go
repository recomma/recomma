package storage

import (
	"context"
	"encoding/json"

	"github.com/recomma/recomma/pkg/sqllogger"
	"github.com/recomma/recomma/storage/sqlcgen"
)

// LogInsertFunc returns a sqllogger.InsertFunc backed by the storage's primary
// sqlc query set. Callers can pass this to sqllogger.NewHandler via
// sqllogger.WithInsertFunc.
func (s *Storage) LogInsertFunc() sqllogger.InsertFunc {
	return func(ctx context.Context, entry sqllogger.InsertLogEntryParams) error {
		params := sqlcgen.InsertAppLogEntryParams{
			TimestampMillis: entry.TimestampMillis,
			LevelText:       entry.LevelText,
			Scope:           stringPtr(entry.Scope),
			Message:         entry.Message,
			AttrsJson:       json.RawMessage(entry.AttrsJSON),
			SourceFile:      stringPtr(entry.SourceFile),
			SourceLine:      nullableInt64(entry.SourceLine),
			SourceFunction:  stringPtr(entry.SourceFunction),
		}
		return s.queries.InsertAppLogEntry(ctx, params)
	}
}

func stringPtr(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

func nullableInt64(v int) *int64 {
	if v <= 0 {
		return nil
	}
	out := int64(v)
	return &out
}
