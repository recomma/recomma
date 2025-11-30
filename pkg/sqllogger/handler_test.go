package sqllogger

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestHandlerHandlePersists(t *testing.T) {
	t.Parallel()

	entries := make(chan InsertLogEntryParams, 1)
	handler, err := NewHandler(
		WithInsertFunc(func(ctx context.Context, params InsertLogEntryParams) error {
			entries <- params
			return nil
		}),
		WithQueueSize(2),
		WithMinLevel(slog.LevelDebug),
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() {
		_ = handler.Close(context.Background())
	})

	record := slog.NewRecord(time.Unix(0, 0), slog.LevelInfo, "hello world", 0)
	record.AddAttrs(slog.Int("count", 42))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	select {
	case entry := <-entries:
		if entry.Message != "hello world" {
			t.Fatalf("unexpected message %q", entry.Message)
		}
		if entry.Scope != "" {
			t.Fatalf("expected empty scope, got %q", entry.Scope)
		}
		var attrs map[string]any
		if err := json.Unmarshal(entry.AttrsJSON, &attrs); err != nil {
			t.Fatalf("unmarshal attrs: %v", err)
		}
		if got := attrs["count"]; got != float64(42) {
			t.Fatalf("expected count 42, got %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for log entry")
	}
}

func TestHandlerGroupsAndAttrs(t *testing.T) {
	t.Parallel()

	entries := make(chan InsertLogEntryParams, 1)
	handler, err := NewHandler(
		WithInsertFunc(func(ctx context.Context, params InsertLogEntryParams) error {
			entries <- params
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() {
		_ = handler.Close(context.Background())
	})

	grouped := handler.WithGroup("engine").WithGroup("filltracker").WithAttrs([]slog.Attr{slog.String("static", "value")})
	child, ok := grouped.(*Handler)
	if !ok {
		t.Fatalf("expected *Handler clone")
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "grouped", 0)
	record.AddAttrs(slog.Group("nested", slog.String("k", "v")))
	if err := child.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	entry := <-entries
	if entry.Scope != "engine.filltracker" {
		t.Fatalf("expected scope engine.filltracker, got %q", entry.Scope)
	}

	var attrs map[string]any
	if err := json.Unmarshal(entry.AttrsJSON, &attrs); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}

	engine, ok := attrs["engine"].(map[string]any)
	if !ok {
		t.Fatalf("expected engine group in attrs: %v", attrs)
	}
	fillTracker, ok := engine["filltracker"].(map[string]any)
	if !ok {
		t.Fatalf("expected filltracker group in attrs: %v", engine)
	}
	if fillTracker["static"] != "value" {
		t.Fatalf("expected static attr, got %v", fillTracker["static"])
	}
	nested, ok := fillTracker["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested group in attrs: %v", fillTracker)
	}
	if nested["k"] != "v" {
		t.Fatalf("expected nested.k attr, got %v", nested["k"])
	}
}

func TestHandlerQueueFull(t *testing.T) {
	blockCh := make(chan struct{})
	handler, err := NewHandler(
		WithInsertFunc(func(ctx context.Context, params InsertLogEntryParams) error {
			<-blockCh
			return nil
		}),
		WithQueueSize(2),
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer close(blockCh)
	t.Cleanup(func() {
		_ = handler.Close(context.Background())
	})

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "first", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle first: %v", err)
	}

	second := slog.NewRecord(time.Now(), slog.LevelInfo, "second", 0)
	if err := handler.Handle(context.Background(), second); err != nil {
		t.Fatalf("Handle second: %v", err)
	}

	third := slog.NewRecord(time.Now(), slog.LevelInfo, "third", 0)
	err = handler.Handle(context.Background(), third)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

func TestHandlerCloseFlushes(t *testing.T) {
	var (
		mu       sync.Mutex
		messages []string
	)
	handler, err := NewHandler(
		WithInsertFunc(func(ctx context.Context, params InsertLogEntryParams) error {
			mu.Lock()
			messages = append(messages, params.Message)
			mu.Unlock()
			return nil
		}),
		WithQueueSize(4),
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	for _, msg := range []string{"one", "two"} {
		rec := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
		if err := handler.Handle(context.Background(), rec); err != nil {
			t.Fatalf("Handle %q: %v", msg, err)
		}
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
}

func TestHandleAfterClose(t *testing.T) {
	handler, err := NewHandler(
		WithInsertFunc(func(ctx context.Context, params InsertLogEntryParams) error {
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	_ = handler.Close(context.Background())
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "late", 0)
	err = handler.Handle(context.Background(), rec)
	if !errors.Is(err, ErrHandlerClosed) {
		t.Fatalf("expected ErrHandlerClosed, got %v", err)
	}
}
