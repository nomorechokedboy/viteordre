package otel

import (
	"context"
	"fmt"
	"os"

	"encore.app/internal/config"
	"encore.app/internal/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	oteltrace "go.opentelemetry.io/otel/sdk/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

type (
	OtelConfig interface {
		Shutdown(context.Context) error
		GetLoggerProvider() *sdklog.LoggerProvider
		GetTracerProvider() *sdktrace.TracerProvider
	}
	otelConfig struct {
		logExp  *otlploghttp.Exporter
		lp      *sdklog.LoggerProvider
		spanExp oteltrace.SpanExporter
		tp      *sdktrace.TracerProvider
	}
)

var (
	tracer       trace.Tracer
	otlpEndpoint string
	globalConfig *otelConfig
	_            OtelConfig = (*otelConfig)(nil)
)

func (o *otelConfig) GetLoggerProvider() *sdklog.LoggerProvider {
	return o.lp
}

func (o *otelConfig) GetTracerProvider() *sdktrace.TracerProvider {
	return o.tp
}

func (o *otelConfig) Shutdown(ctx context.Context) error {
	if err := o.logExp.Shutdown(ctx); err != nil {
		return err
	}
	if err := o.lp.Shutdown(ctx); err != nil {
		return err
	}
	if err := o.spanExp.Shutdown(ctx); err != nil {
		return err
	}
	if err := o.tp.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

func init() {
	otlpEndpoint = config.GetConfig().OtelConfig.Endpoint
	if otlpEndpoint == "" {
		// Don't panic in init, let the service initialization handle it
		logger.Warn("OTLP_ENDPOINT not set, OTEL will not be initialized")
	}

	ctx := context.Background()
	cfg, err := New(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Initialize otlp error", "err", err)
		panic("Initialize otlp error")
	}

	globalConfig = cfg
}

func newTraceExporter(ctx context.Context) (oteltrace.SpanExporter, error) {
	// Change default HTTPS -> HTTP
	insecureOpt := otlptracehttp.WithInsecure()
	// Update default OTLP receiver endpoint
	endpointOpt := otlptracehttp.WithEndpoint(otlpEndpoint)
	return otlptracehttp.New(ctx, insecureOpt, endpointOpt)
}

func newStdoutExporter(ctx context.Context) (oteltrace.SpanExporter, error) {
	return stdouttrace.New(stdouttrace.WithPrettyPrint(), stdouttrace.WithWriter(os.Stdout))
}

func newTraceProvider(exp sdktrace.SpanExporter) *sdktrace.TracerProvider {
	// Ensure default SDK resources and the required service name are set.
	// The service name is schemaless on purpose: resource.Default() carries
	// the SDK's own schema URL, and Merge fails when two non-empty schema
	// URLs differ, which happened every time the SDK moved past our pinned
	// semconv version.
	r, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName("sms-api")),
	)
	if err != nil {
		panic(err)
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(r),
	)
}

func newLogExporter(ctx context.Context) *otlploghttp.Exporter {
	logExporter, err := otlploghttp.New(
		ctx,
		otlploghttp.WithEndpoint(otlpEndpoint),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		panic("failed to initialize exporter")
	}
	return logExporter
}

func newLogProvider(exp sdklog.Exporter) *sdklog.LoggerProvider {
	// Ensure default SDK resources and the required service name are set.
	// The service name is schemaless on purpose: resource.Default() carries
	// the SDK's own schema URL, and Merge fails when two non-empty schema
	// URLs differ, which happened every time the SDK moved past our pinned
	// semconv version.
	r, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName("sms-api")),
	)
	if err != nil {
		panic(err)
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(r),
	)
}

func New(ctx context.Context) (*otelConfig, error) {
	if otlpEndpoint == "" {
		return nil, fmt.Errorf("OTLP_ENDPOINT not set")
	}

	// Trace exporter (for Jaeger)
	spanExp, err := newTraceExporter(ctx)
	if err != nil {
		return nil, err
	}

	// Log exporter (for logs)
	logExp := newLogExporter(ctx)

	// Create providers
	tp, lp := newTraceProvider(spanExp), newLogProvider(logExp)

	// Register globally
	otel.SetTracerProvider(tp)
	global.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	tracer = tp.Tracer("sms-api")
	return &otelConfig{logExp, lp, spanExp, tp}, nil
}

func TracerStart(
	ctx context.Context,
	spanName string,
	opts ...trace.SpanStartOption,
) (context.Context, trace.Span) {
	return tracer.Start(ctx, spanName, opts...)
}

func SetTracerProvider(tp trace.TracerProvider) {
	otel.SetTracerProvider(tp)
}

// GetConfig returns the global OTEL configuration
// This can be used by other packages (like logger) to access the OTEL providers
func GetConfig() *otelConfig {
	return globalConfig
}

// GetLoggerProvider returns the global OTEL logger provider
func GetLoggerProvider() *sdklog.LoggerProvider {
	if globalConfig != nil {
		return globalConfig.lp
	}
	return nil
}

// GetTracerProvider returns the global OTEL tracer provider
func GetTracerProvider() *sdktrace.TracerProvider {
	if globalConfig != nil {
		return globalConfig.tp
	}
	return nil
}
