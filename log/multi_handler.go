package log

import (
	"context"
	"log/slog"
)

// MultiHandler fans out records to all child handlers.
//
// TODO: drop this helper once Go's native MultiHandler lands (CL 692237).
type MultiHandler struct {
	children []slog.Handler
}

// NewMultiHandler constructs a MultiHandler, skipping nil handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	pruned := make([]slog.Handler, 0, len(handlers))
	for _, h := range handlers {
		if h != nil {
			pruned = append(pruned, h)
		}
	}
	return &MultiHandler{children: pruned}
}

func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, child := range h.children {
		if child.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *MultiHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error
	for _, child := range h.children {
		if !child.Enabled(ctx, record.Level) {
			continue
		}
		if err := child.Handle(ctx, record); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newChildren := make([]slog.Handler, len(h.children))
	for i, child := range h.children {
		newChildren[i] = child.WithAttrs(attrs)
	}
	return &MultiHandler{children: newChildren}
}

func (h *MultiHandler) WithGroup(name string) slog.Handler {
	newChildren := make([]slog.Handler, len(h.children))
	for i, child := range h.children {
		newChildren[i] = child.WithGroup(name)
	}
	return &MultiHandler{children: newChildren}
}
