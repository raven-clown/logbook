package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/internal/obs"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func get(t *testing.T, h http.Handler, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequestIDKept(t *testing.T) {
	var seen string
	h := obs.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = obs.RequestIDFrom(r.Context()) }))
	rec := get(t, h, "/", http.Header{"X-Request-Id": {"abc-123"}})
	if seen != "abc-123" || rec.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("seen %q, echoed %q", seen, rec.Header().Get("X-Request-ID"))
	}
}

func TestRequestIDReplaced(t *testing.T) {
	for _, in := range []string{"", strings.Repeat("a", 129), "a\nb", "a b", "caf\u00e9"} {
		var seen string
		h := obs.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = obs.RequestIDFrom(r.Context()) }))
		hdr := http.Header{}
		if in != "" {
			hdr["X-Request-Id"] = []string{in}
		}
		rec := get(t, h, "/", hdr)
		if seen == "" || seen == in || len(seen) != 26 || rec.Header().Get("X-Request-ID") != seen {
			t.Fatalf("input %q gave %q, echoed %q", in, seen, rec.Header().Get("X-Request-ID"))
		}
	}
	if obs.RequestIDFrom(context.Background()) != "" {
		t.Fatal("empty context has a request id")
	}
}

func TestLoggerAddsIDs(t *testing.T) {
	var buf bytes.Buffer
	l := obs.NewLogger(&buf, slog.LevelInfo)
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}))
	h := obs.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		l.InfoContext(trace.ContextWithSpanContext(r.Context(), trace.SpanContextFromContext(ctx)), "hello", "n", 1)
	}))
	get(t, h, "/", http.Header{"X-Request-Id": {"req-1"}})
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line %q: %v", buf.String(), err)
	}
	if line["msg"] != "hello" || line["request_id"] != "req-1" || line["trace_id"] != tid.String() || line["n"] != float64(1) {
		t.Fatalf("log line = %v", line)
	}
	buf.Reset()
	l.Info("plain")
	if strings.Contains(buf.String(), "request_id") || strings.Contains(buf.String(), "trace_id") {
		t.Fatalf("ids without a context: %s", buf.String())
	}
}

func TestMetricsCount(t *testing.T) {
	m := obs.NewMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/memories/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	h := m.Wrap(mux)
	get(t, h, "/v1/memories/mem_1", nil)
	get(t, h, "/v1/memories/mem_2", nil)
	get(t, h, "/nowhere", nil)
	body := get(t, m.Handler(), "/metrics", nil).Body.String()
	for _, want := range []string{
		`logbook_http_requests_total{code="404",method="GET",route="GET /v1/memories/{id}"} 2`,
		`logbook_http_requests_total{code="404",method="GET",route="unmatched"} 1`,
		`logbook_http_request_duration_seconds_count{method="GET",route="GET /v1/memories/{id}"} 2`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics lack %q:\n%s", want, body)
		}
	}
}

func TestWrappedWriterFlushes(t *testing.T) {
	m := obs.NewMetrics()
	var flushed bool
	h := obs.Trace(m.Wrap(obs.AccessLog(slog.New(slog.NewJSONHandler(io.Discard, nil)), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, flushed = w.(http.Flusher)
	}))))
	get(t, h, "/", nil)
	if !flushed {
		t.Fatal("the wrapped writer is not an http.Flusher, so streamed MCP responses break")
	}
}

func TestTraceparentContinues(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(old) })
	if _, err := obs.SetupTracing(context.Background(), "", "test"); err != nil {
		t.Fatal(err)
	}
	otel.SetTracerProvider(tp)
	mux := http.NewServeMux()
	var inner trace.SpanContext
	mux.HandleFunc("GET /healthz", func(_ http.ResponseWriter, r *http.Request) { inner = trace.SpanContextFromContext(r.Context()) })
	get(t, obs.Trace(mux), "/healthz", http.Header{"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}})
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans", len(spans))
	}
	s := spans[0]
	if s.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || s.Parent().SpanID().String() != "00f067aa0ba902b7" || s.SpanKind() != trace.SpanKindServer {
		t.Fatalf("span = %+v", s)
	}
	if s.Name() != "GET /healthz" || inner.SpanID() != s.SpanContext().SpanID() {
		t.Fatalf("span name %q, inner %v", s.Name(), inner.SpanID())
	}
}

func TestSetupTracingNoEndpoint(t *testing.T) {
	shutdown, err := obs.SetupTracing(context.Background(), "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := obs.SetupTracing(context.Background(), "::not a url", "test"); err == nil {
		t.Fatal("a bad endpoint was accepted")
	}
}

func TestUnknownMethodsShareALabel(t *testing.T) {
	m := obs.NewMetrics()
	h := m.Wrap(http.NewServeMux())
	for _, method := range []string{"BREW", "X-1", "X-2"} {
		req := httptest.NewRequestWithContext(context.Background(), method, "/", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	body := get(t, m.Handler(), "/metrics", nil).Body.String()
	if strings.Contains(body, "BREW") || !strings.Contains(body, `logbook_http_requests_total{code="404",method="OTHER",route="unmatched"} 3`) {
		t.Fatalf("unknown methods got their own labels:\n%s", body)
	}
}

type copyKey struct{}

func TestRoutesThroughCopies(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(old) })
	m := obs.NewMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/memories/{id}", func(http.ResponseWriter, *http.Request) {})
	copying := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), copyKey{}, 1)))
		})
	}
	h := obs.Trace(m.Wrap(copying(obs.Routes(mux))))
	get(t, h, "/v1/memories/mem_1", nil)
	body := get(t, m.Handler(), "/metrics", nil).Body.String()
	if !strings.Contains(body, `route="GET /v1/memories/{id}"`) {
		t.Fatalf("route lost behind a middleware that copies the request:\n%s", body)
	}
	if spans := rec.Ended(); len(spans) != 1 || spans[0].Name() != "GET /v1/memories/{id}" {
		t.Fatalf("span name lost: %v", spans)
	}
}
