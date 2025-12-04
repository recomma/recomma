package log

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

type recordingHandler struct {
	count int
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(context.Context, slog.Record) error {
	h.count++
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func TestGroupFilterAllowsConfiguredGroups(t *testing.T) {
	rec := &recordingHandler{}
	handler := NewGroupFilterHandler(rec, []string{"storage"})
	if handler == rec {
		t.Fatal("expected wrapper handler")
	}
	filter, ok := handler.(*GroupFilterHandler)
	if !ok {
		t.Fatalf("unexpected handler type %T", handler)
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	if err := filter.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if rec.count != 0 {
		t.Fatal("expected record to be filtered without group")
	}

	allow := filter.WithGroup("storage")
	if err := allow.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle with group: %v", err)
	}
	if rec.count != 1 {
		t.Fatalf("expected record to pass after group, got %d", rec.count)
	}
}

func TestGroupFilterPassthroughWhenNoAllowlist(t *testing.T) {
	rec := &recordingHandler{}
	handler := NewGroupFilterHandler(rec, nil)
	if handler != rec {
		t.Fatal("expected original handler")
	}
}
