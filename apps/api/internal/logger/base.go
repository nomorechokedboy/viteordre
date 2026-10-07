package logger

import (
	"context"
	"log/slog"
)

type Logger interface {
	Debug(string, ...any)
	DebugContext(context.Context, string, ...any)
	Error(string, ...any)
	ErrorContext(context.Context, string, ...any)
	Info(string, ...any)
	InfoContext(context.Context, string, ...any)
	Log(context.Context, slog.Leveler, string, ...any)
	LogAttrs(context.Context, slog.Leveler, string, ...any)
	Warn(string, ...any)
	WarnContext(context.Context, string, ...any)
}

type LoggerType int

const (
	Default LoggerType = iota
	File
	FileAndCentralized
	Otel
	RlogAndOtel
)
