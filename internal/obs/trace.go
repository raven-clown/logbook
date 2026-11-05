package obs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/raven-clown/logbook"

// traceURL adds the OTLP traces path to an endpoint given without one.
func traceURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("telemetry.otlp_endpoint must be an http or https URL, not %q", endpoint)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return u.String(), nil
}

// SetupTracing installs the W3C trace context propagator and, when endpoint is
// set, an OTLP over HTTP exporter. Without an endpoint incoming trace IDs are
// still carried through to logs and outgoing calls.
func SetupTracing(ctx context.Context, endpoint, version string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	u, err := traceURL(endpoint)
	if err != nil {
		return nil, err
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(u))
	if err != nil {
		return nil, fmt.Errorf("starting the trace exporter: %w", err)
	}
	res := resource.NewSchemaless(semconv.ServiceName("logbook"), semconv.ServiceVersion(version))
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Trace continues the caller's trace from traceparent and tracestate and
// records one server span per request.
func Trace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer(tracerName).Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		sw := &statusWriter{ResponseWriter: w}
		r = r.WithContext(ctx)
		next.ServeHTTP(sw, r)
		if r.Pattern != "" {
			span.SetName(r.Pattern)
		}
		span.SetAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("http.route", route(r)),
			attribute.Int("http.response.status_code", sw.code()),
		)
	})
}
