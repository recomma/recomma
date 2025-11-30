package sqllogger

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultQueueSize = 256
)

var (
	ErrQueueFull     = errors.New("sqllogger: queue full")
	ErrHandlerClosed = errors.New("sqllogger: handler closed")
)

type InsertLogEntryParams struct {
	TimestampMillis int64
	LevelText       string
	Scope           string
	Message         string
	AttrsJSON       []byte
	SourceFile      string
	SourceLine      int
	SourceFunction  string
}

type InsertFunc func(context.Context, InsertLogEntryParams) error

type Option func(*handlerConfig)

type handlerConfig struct {
	minLevel  slog.Level
	queueSize int
	insertFn  InsertFunc
}

func WithMinLevel(level slog.Level) Option {
	return func(cfg *handlerConfig) {
		cfg.minLevel = level
	}
}

func WithQueueSize(size int) Option {
	return func(cfg *handlerConfig) {
		if size > 0 {
			cfg.queueSize = size
		}
	}
}

func WithInsertFunc(fn InsertFunc) Option {
	return func(cfg *handlerConfig) {
		cfg.insertFn = fn
	}
}

type Handler struct {
	core   *handlerCore
	attrs  []slog.Attr
	groups []string
}

type handlerCore struct {
	insertFn InsertFunc
	minLevel slog.Level

	queue  chan queuedEntry
	ctx    context.Context
	cancel context.CancelFunc

	wg       sync.WaitGroup
	handleWG sync.WaitGroup
	closed   atomic.Bool
}

type queuedEntry struct {
	ctx    context.Context
	params InsertLogEntryParams
}

func NewHandler(opts ...Option) (*Handler, error) {
	cfg := handlerConfig{
		minLevel:  slog.LevelInfo,
		queueSize: defaultQueueSize,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.insertFn == nil {
		return nil, errors.New("sqllogger: insert function is required")
	}

	coreCtx, cancel := context.WithCancel(context.Background())
	core := &handlerCore{
		insertFn: cfg.insertFn,
		minLevel: cfg.minLevel,
		queue:    make(chan queuedEntry, cfg.queueSize),
		ctx:      coreCtx,
		cancel:   cancel,
	}
	core.wg.Add(1)
	go core.run()

	return &Handler{core: core}, nil
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	if h == nil || h.core == nil {
		return false
	}
	return level >= h.core.minLevel
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if h == nil || h.core == nil {
		return errors.New("sqllogger: handler not initialized")
	}

	if !h.Enabled(ctx, record.Level) {
		return nil
	}

	if h.core.closed.Load() {
		return ErrHandlerClosed
	}

	h.core.handleWG.Add(1)
	defer h.core.handleWG.Done()

	if h.core.closed.Load() {
		return ErrHandlerClosed
	}

	params := h.buildParams(record)
	return h.core.enqueue(ctx, params)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := &Handler{
		core:   h.core,
		attrs:  append([]slog.Attr{}, h.attrs...),
		groups: append([]string{}, h.groups...),
	}
	clone.attrs = append(clone.attrs, attrs...)
	return clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := &Handler{
		core:   h.core,
		attrs:  append([]slog.Attr{}, h.attrs...),
		groups: append([]string{}, h.groups...),
	}
	clone.groups = append(clone.groups, name)
	return clone
}

func (h *Handler) Close(ctx context.Context) error {
	if h == nil || h.core == nil {
		return nil
	}
	return h.core.close(ctx)
}

func (c *handlerCore) enqueue(ctx context.Context, params InsertLogEntryParams) error {
	if ctx == nil {
		ctx = context.Background()
	}
	entry := queuedEntry{ctx: ctx, params: params}
	select {
	case c.queue <- entry:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrQueueFull
	}
}

func (c *handlerCore) run() {
	defer c.wg.Done()
	for {
		select {
		case entry := <-c.queue:
			c.process(entry)
		case <-c.ctx.Done():
			c.drainQueue()
			return
		}
	}
}

func (c *handlerCore) process(entry queuedEntry) {
	ctx := entry.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	_ = c.insertFn(ctx, entry.params)
}

func (c *handlerCore) drainQueue() {
	for {
		select {
		case entry := <-c.queue:
			c.process(entry)
		default:
			return
		}
	}
}

func (c *handlerCore) close(ctx context.Context) error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.handleWG.Wait()
	c.cancel()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) buildParams(record slog.Record) InsertLogEntryParams {
	ts := record.Time.UTC().UnixMilli()
	if ts == 0 {
		ts = time.Now().UTC().UnixMilli()
	}

	params := InsertLogEntryParams{
		TimestampMillis: ts,
		LevelText:       record.Level.String(),
		Message:         record.Message,
		Scope:           joinScope(h.groups),
	}

	if frame := record.Source(); frame != nil {
		params.SourceFile = frame.File
		params.SourceLine = frame.Line
		params.SourceFunction = frame.Function
	}

	attrs := h.collectAttrs(record)
	params.AttrsJSON = encodeAttrsJSON(h.groups, attrs)

	return params
}

func (h *Handler) collectAttrs(record slog.Record) []slog.Attr {
	total := len(h.attrs) + record.NumAttrs()
	attrs := make([]slog.Attr, 0, total)
	if len(h.attrs) > 0 {
		attrs = append(attrs, cloneAttrs(h.attrs)...)
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	return attrs
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	copy(out, attrs)
	return out
}

func joinScope(groups []string) string {
	filtered := make([]string, 0, len(groups))
	for _, g := range groups {
		if g != "" {
			filtered = append(filtered, g)
		}
	}
	return strings.Join(filtered, ".")
}

func encodeAttrsJSON(groups []string, attrs []slog.Attr) []byte {
	root := map[string]any{}
	for _, attr := range attrs {
		appendAttr(root, groups, attr)
	}
	if len(root) == 0 {
		return []byte("{}")
	}
	data, err := json.Marshal(root)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func appendAttr(dst map[string]any, groups []string, attr slog.Attr) {
	attr = resolveAttr(attr)
	target := ensureGroup(dst, groups)
	insertAttr(target, attr)
}

func resolveAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		resolved := make([]slog.Attr, len(children))
		for i, child := range children {
			resolved[i] = resolveAttr(child)
		}
		attr.Value = slog.GroupValue(resolved...)
	}
	return attr
}

func ensureGroup(dst map[string]any, groups []string) map[string]any {
	target := dst
	for _, name := range groups {
		if name == "" {
			continue
		}
		if next, ok := target[name]; ok {
			if m, ok := next.(map[string]any); ok {
				target = m
				continue
			}
		}
		child := make(map[string]any)
		target[name] = child
		target = child
	}
	return target
}

func insertAttr(dst map[string]any, attr slog.Attr) {
	if attr.Key == "" && attr.Value.Kind() != slog.KindGroup {
		return
	}

	switch attr.Value.Kind() {
	case slog.KindGroup:
		groupTarget := dst
		if attr.Key != "" {
			groupTarget = ensureGroup(dst, []string{attr.Key})
		}
		for _, child := range attr.Value.Group() {
			appendAttr(groupTarget, nil, child)
		}
	default:
		dst[attr.Key] = valueFromSlogValue(attr.Value)
	}
}

func valueFromSlogValue(val slog.Value) any {
	val = val.Resolve()
	switch val.Kind() {
	case slog.KindString:
		return val.String()
	case slog.KindInt64:
		return val.Int64()
	case slog.KindUint64:
		return val.Uint64()
	case slog.KindFloat64:
		return val.Float64()
	case slog.KindBool:
		return val.Bool()
	case slog.KindDuration:
		return val.Duration()
	case slog.KindTime:
		return val.Time()
	case slog.KindAny:
		return val.Any()
	default:
		return val.Any()
	}
}
