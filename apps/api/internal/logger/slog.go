package logger

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mdobak/go-xerrors"
)

var _ Logger = (*SlogLogger)(nil)

const (
	slogFields ctxKey = "slog_fields"
)

type (
	SlogLogger struct {
		logger *slog.Logger
	}
	ctxKey     string
	stackFrame struct {
		Func   string `json:"func"`
		Source string `json:"source"`
		Line   int    `json:"line"`
	}
)

// AppendCtx adds an slog attribute to the provided context so that it will be
// included in any Record created with such context
func AppendCtx(parent context.Context, attr slog.Attr) context.Context {
	if parent == nil {
		parent = context.Background()
	}

	if v, ok := parent.Value(slogFields).([]slog.Attr); ok {
		v = append(v, attr)
		return context.WithValue(parent, slogFields, v)
	}

	v := []slog.Attr{}
	v = append(v, attr)
	return context.WithValue(parent, slogFields, v)
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			a.Value = fmtErr(v)
		}
	}

	return a
}

// marshalStack extracts stack frames from the error
func marshalStack(err error) []stackFrame {
	trace := xerrors.StackTrace(err)

	if len(trace) == 0 {
		return nil
	}

	frames := trace.Frames()

	s := make([]stackFrame, len(frames))

	for i, v := range frames {
		f := stackFrame{
			Source: filepath.Join(
				filepath.Base(filepath.Dir(v.File)),
				filepath.Base(v.File),
			),
			Func: filepath.Base(v.Function),
			Line: v.Line,
		}

		s[i] = f
	}

	return s
}

// fmtErr returns a slog.Value with keys `msg` and `trace`. If the error
// does not implement interface { StackTrace() errors.StackTrace }, the `trace`
// key is omitted.
func fmtErr(err error) slog.Value {
	var groupValues []slog.Attr

	groupValues = append(groupValues, slog.String("msg", err.Error()))

	frames := marshalStack(err)

	if frames != nil {
		groupValues = append(groupValues,
			slog.Any("trace", frames),
		)
	}

	return slog.GroupValue(groupValues...)
}

func NewSlogLogger(name string, options ...Option) *SlogLogger {
	logger := slog.New(NewContextHandler(name, options...))
	slog.SetDefault(logger)
	return &SlogLogger{logger: logger}
}

func NewSlogLoggerWithRlog(name string, options ...Option) *SlogLogger {
	cfg := newConfig(options)
	logger := slog.New(NewRlogHandler(cfg.HandlerOptions))
	slog.SetDefault(logger)
	return &SlogLogger{logger: logger}
}

// NewSlogLoggerWithRlogAndOtel creates a logger that sends logs to both Rlog (Encore) and OpenTelemetry.
func NewSlogLoggerWithRlogAndOtel(name string, options ...Option) *SlogLogger {
	cfg := newConfig(options)
	rlogHandler := NewRlogHandler(cfg.HandlerOptions)
	otelHandler := newContextHandler(name, cfg)
	logger := slog.New(NewCompositeHandler(rlogHandler, otelHandler))
	slog.SetDefault(logger)
	return &SlogLogger{logger: logger}
}

func (l *SlogLogger) Debug(msg string, args ...any) {
	l.DebugContext(context.Background(), msg, args...)
}

func (l *SlogLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

func (l *SlogLogger) Error(msg string, args ...any) {
	l.ErrorContext(context.Background(), msg, args...)
}

func (l *SlogLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelError, msg, args...)
}

func (l *SlogLogger) Info(msg string, args ...any) {
	l.InfoContext(context.Background(), msg, args...)
}

func (l *SlogLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

func (l *SlogLogger) Log(ctx context.Context, level slog.Leveler, msg string, args ...any) {
	l.log(ctx, level.Level(), msg, args...)
}

func (l *SlogLogger) LogAttrs(ctx context.Context, level slog.Leveler, msg string, args ...any) {
	// Get caller information
	pc, _, _, _ := runtime.Caller(3) // Skip three frame to get the actual caller

	// Create a Record with the correct caller information
	r := slog.NewRecord(time.Now(), level.Level(), msg, pc)
	r.Add(args...)

	// Log the record
	_ = l.logger.Handler().Handle(ctx, r)
}

func (l *SlogLogger) Warn(msg string, args ...any) {
	l.WarnContext(context.Background(), msg, args...)
}

func (l *SlogLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

func (l *SlogLogger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	// Get caller information
	pc, _, _, _ := runtime.Caller(3) // Skip three frames to get the actual caller

	// Create a Record with the correct caller information
	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.Add(args...)

	// Log the record
	_ = l.logger.Handler().Handle(ctx, r)
}
