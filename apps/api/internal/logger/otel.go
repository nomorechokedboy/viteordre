package logger

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"encore.dev"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type (
	// Option configures a [ContextHandler].
	Option interface {
		apply(*config) *config
	}
	optFunc func(*config) *config

	// ContextHandler enriches records with context fields and Encore/OTel
	// trace info, then forwards them to the embedded stdout handler and to
	// an otelslog bridge handler that exports via OTLP.
	ContextHandler struct {
		slog.Handler // JSON handler on stdout
		otel         slog.Handler
	}
)

func (f optFunc) apply(c *config) *config { return f(c) }

// WithVersion sets the instrumentation version of the OTel logger.
func WithVersion(version string) Option {
	return optFunc(func(c *config) *config {
		c.version = version
		return c
	})
}

// WithSchemaURL sets the semantic convention schema URL of the OTel logger.
func WithSchemaURL(schemaURL string) Option {
	return optFunc(func(c *config) *config {
		c.schemaURL = schemaURL
		return c
	})
}

// WithLoggerProvider sets the [log.LoggerProvider] used by the otelslog
// handler. If unset, otelslog uses the global LoggerProvider.
func WithLoggerProvider(provider log.LoggerProvider) Option {
	return optFunc(func(c *config) *config {
		c.provider = provider
		return c
	})
}

func WithLevel(level slog.Level) Option {
	return optFunc(func(c *config) *config {
		c.Level = level
		return c
	})
}

func WithHandleOptions(opts *slog.HandlerOptions) Option {
	return optFunc(func(c *config) *config {
		c.HandlerOptions = opts
		return c
	})
}

type config struct {
	provider  log.LoggerProvider
	version   string
	schemaURL string
	*slog.HandlerOptions
}

func newConfig(options []Option) *config {
	c := &config{
		HandlerOptions: &slog.HandlerOptions{
			AddSource:   true,
			ReplaceAttr: replaceAttr, // defined in slog_logger.go
		},
	}
	for _, opt := range options {
		c = opt.apply(c)
	}
	return c
}

// NewContextHandler builds a ContextHandler. name is the OTel instrumentation
// scope name.
func NewContextHandler(name string, options ...Option) *ContextHandler {
	return newContextHandler(name, newConfig(options))
}

func newContextHandler(name string, c *config) *ContextHandler {
	opts := []otelslog.Option{otelslog.WithSource(c.AddSource)}
	if c.provider != nil {
		opts = append(opts, otelslog.WithLoggerProvider(c.provider))
	}
	if c.version != "" {
		opts = append(opts, otelslog.WithVersion(c.version))
	}
	if c.schemaURL != "" {
		opts = append(opts, otelslog.WithSchemaURL(c.schemaURL))
	}

	return &ContextHandler{
		Handler: slog.NewJSONHandler(os.Stdout, c.HandlerOptions),
		otel:    otelslog.NewHandler(name, opts...),
	}
}

// Enabled reports whether either underlying handler wants the record.
func (h *ContextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.Handler.Enabled(ctx, l) || h.otel.Enabled(ctx, l)
}

// Handle adds context fields plus Encore and OpenTelemetry trace info to the
// record, then forwards it to both handlers.
//
// Note: like the previous implementation, this does not gate on Enabled.
// SlogLogger.log calls Handler().Handle directly, so level filtering never
// happens on this path.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(slogFields).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}

	h.addEncoreAttrs(&r)

	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		h.addOTelAttrs(&r, span)
	}

	// Clone so the handlers never share the record's attribute storage.
	return errors.Join(
		h.otel.Handle(ctx, r.Clone()),
		h.Handler.Handle(ctx, r.Clone()),
	)
}

// WithAttrs returns a handler whose records carry attrs on both outputs.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{
		Handler: h.Handler.WithAttrs(attrs),
		otel:    h.otel.WithAttrs(attrs),
	}
}

// WithGroup returns a handler that nests subsequent attrs under name on both
// outputs.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{
		Handler: h.Handler.WithGroup(name),
		otel:    h.otel.WithGroup(name),
	}
}

func (h *ContextHandler) addEncoreAttrs(r *slog.Record) {
	req := encore.CurrentRequest()
	if req.Type != encore.APICall || req.Trace == nil {
		return
	}

	attrs := make([]slog.Attr, 0, 4)
	if req.Trace.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", req.Trace.TraceID))
	}
	if req.Trace.SpanID != "" {
		attrs = append(attrs, slog.String("span_id", req.Trace.SpanID))
	}
	if req.Service != "" {
		attrs = append(attrs, slog.String("service", req.Service))
	}
	if req.Endpoint != "" {
		attrs = append(attrs, slog.String("endpoint", req.Endpoint))
	}
	r.AddAttrs(attrs...)
}

func (h *ContextHandler) addOTelAttrs(r *slog.Record, span trace.Span) {
	sc := span.SpanContext()
	attrs := make([]slog.Attr, 0, 2)
	if sc.HasTraceID() {
		attrs = append(attrs, slog.String("otel_trace_id", sc.TraceID().String()))
	}
	if sc.HasSpanID() {
		attrs = append(attrs, slog.String("otel_span_id", sc.SpanID().String()))
	}
	r.AddAttrs(attrs...)
}
