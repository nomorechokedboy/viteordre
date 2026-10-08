package middleware

import (
	"encoding/binary"
	"hash/fnv"

	"encore.app/internal/otel"
	"encore.dev"
	"encore.dev/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

//encore:middleware global target=all
func TracingMiddleware(req middleware.Request, next middleware.Next) middleware.Response {
	encoreReq := encore.CurrentRequest()

	if encoreReq.Type != encore.APICall || encoreReq.Trace == nil {
		return next(req)
	}

	tp := otel.GetTracerProvider()
	if tp == nil {
		return next(req)
	}

	tracer := tp.Tracer("encore-bridge")

	traceID := encoreTraceIDToOTel(encoreReq.Trace.TraceID)
	spanID := encoreSpanIDToOTel(encoreReq.Trace.SpanID)

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     false,
	})

	ctx := trace.ContextWithSpanContext(req.Context(), spanCtx)

	spanName := encoreReq.Service + "." + encoreReq.Endpoint
	ctx, span := tracer.Start(
		ctx,
		spanName,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("encore.service", encoreReq.Service),
			attribute.String("encore.endpoint", encoreReq.Endpoint),
			attribute.String("encore.trace_id", encoreReq.Trace.TraceID),
			attribute.String("encore.span_id", encoreReq.Trace.SpanID),
			attribute.String("http.method", encoreReq.Method),
			attribute.String("http.route", encoreReq.Path),
		),
	)
	defer span.End()

	if encoreReq.Trace.ParentTraceID != "" {
		span.SetAttributes(
			attribute.String("encore.parent_trace_id", encoreReq.Trace.ParentTraceID),
		)
	}

	if encoreReq.Trace.ParentSpanID != "" {
		span.SetAttributes(
			attribute.String("encore.parent_span_id", encoreReq.Trace.ParentSpanID),
		)
	}

	req = req.WithContext(ctx)

	resp := next(req)

	if resp.Err != nil {
		span.RecordError(resp.Err)
		span.SetAttributes(attribute.Bool("error", true))
	}

	return resp
}

// encoreTraceIDToOTel converts Encore's trace ID to OpenTelemetry's TraceID format
// Uses FNV-1a hash for deterministic conversion
func encoreTraceIDToOTel(encoreTraceID string) trace.TraceID {
	// OpenTelemetry TraceID must be exactly 16 bytes (128 bits)
	var traceID trace.TraceID

	// Use FNV-1a hash for deterministic conversion
	h := fnv.New128a()
	h.Write([]byte(encoreTraceID))
	sum := h.Sum(nil)

	copy(traceID[:], sum)
	return traceID
}

// encoreSpanIDToOTel converts Encore's span ID to OpenTelemetry's SpanID format
// Uses FNV-1a hash for deterministic conversion
func encoreSpanIDToOTel(encoreSpanID string) trace.SpanID {
	// OpenTelemetry SpanID must be exactly 8 bytes (64 bits)
	var spanID trace.SpanID

	// Use FNV-1a hash for deterministic conversion
	h := fnv.New64a()
	h.Write([]byte(encoreSpanID))
	sum := h.Sum64()

	binary.BigEndian.PutUint64(spanID[:], sum)
	return spanID
}
