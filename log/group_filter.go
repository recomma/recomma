package log

import (
	"context"
	"log/slog"
	"strings"
)

// GroupFilterHandler filters records by slog group name.
type GroupFilterHandler struct {
	next    slog.Handler
	allowed map[string]struct{}
	groups  []string
}

// NewGroupFilterHandler wraps the provided handler with filtering logic. When
// allowedGroups is empty, the original handler is returned unchanged.
func NewGroupFilterHandler(next slog.Handler, allowedGroups []string) slog.Handler {
	if next == nil || len(allowedGroups) == 0 {
		return next
	}
	allowed := make(map[string]struct{}, len(allowedGroups))
	for _, group := range allowedGroups {
		if trimmed := strings.TrimSpace(strings.ToLower(group)); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return next
	}
	return &GroupFilterHandler{
		next:    next,
		allowed: allowed,
	}
}

func (h *GroupFilterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h == nil || h.next == nil {
		return false
	}
	return h.next.Enabled(ctx, level)
}

func (h *GroupFilterHandler) Handle(ctx context.Context, record slog.Record) error {
	if !h.shouldEmit() {
		return nil
	}
	return h.next.Handle(ctx, record)
}

func (h *GroupFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &GroupFilterHandler{
		next:    h.next.WithAttrs(attrs),
		allowed: h.allowed,
		groups:  append([]string{}, h.groups...),
	}
}

func (h *GroupFilterHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := &GroupFilterHandler{
		next:    h.next.WithGroup(name),
		allowed: h.allowed,
		groups:  append([]string{}, h.groups...),
	}
	clone.groups = append(clone.groups, strings.ToLower(name))
	return clone
}

func (h *GroupFilterHandler) shouldEmit() bool {
	if len(h.allowed) == 0 {
		return true
	}
	for _, grp := range h.groups {
		if _, ok := h.allowed[grp]; ok {
			return true
		}
	}
	return false
}
