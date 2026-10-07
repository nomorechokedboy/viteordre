package logger

import (
	"context"
	"log/slog"
)

// CompositeHandler wraps multiple slog.Handler implementations and forwards
// log records to all of them. This allows simultaneous logging to multiple backends.
type CompositeHandler struct {
	handlers []slog.Handler
	opts     *slog.HandlerOptions
}

var _ slog.Handler = (*CompositeHandler)(nil)

// NewCompositeHandler creates a handler that writes to all provided handlers.
func NewCompositeHandler(handlers ...slog.Handler) *CompositeHandler {
	return &CompositeHandler{
		handlers: handlers,
	}
}

// Enabled returns true if any of the underlying handlers is enabled for the given level.
func (h *CompositeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle forwards the record to all underlying handlers.
func (h *CompositeHandler) Handle(ctx context.Context, r slog.Record) error {
	var lastErr error
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, r); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// WithAttrs returns a new CompositeHandler with attrs added to all underlying handlers.
func (h *CompositeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &CompositeHandler{
		handlers: newHandlers,
		opts:     h.opts,
	}
}

// WithGroup returns a new CompositeHandler with a group added to all underlying handlers.
func (h *CompositeHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &CompositeHandler{
		handlers: newHandlers,
		opts:     h.opts,
	}
}
