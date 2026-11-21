package outbound_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/raven-clown/logbook/internal/obs"
	"github.com/raven-clown/logbook/internal/outbound"
	"go.opentelemetry.io/otel/trace"
)

func TestApply(t *testing.T) {
	const jwt = "eyJhbGciOiJSUzI1NiJ9.e30.sig"
	for _, tt := range []struct {
		name    string
		token   string
		apiKey  string
		forward bool
		want    string
	}{
		{"forward off sends the api key", jwt, "sk-own", false, "Bearer sk-own"},
		{"forward on sends the caller's token", jwt, "sk-own", true, "Bearer " + jwt},
		{"a logbook key is never forwarded", "lbk_secret", "sk-own", true, "Bearer sk-own"},
		{"a logbook key with no api key sends nothing", "lbk_secret", "", true, ""},
		{"nothing to send", "", "", false, ""},
		{"forward on without a caller falls back", "", "sk-own", true, "Bearer sk-own"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			outbound.Apply(outbound.WithToken(context.Background(), tt.token), h, tt.apiKey, tt.forward)
			if got := h.Get("Authorization"); got != tt.want {
				t.Fatalf("Authorization = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyCarriesRequestAndTrace(t *testing.T) {
	var ctx context.Context
	h := obs.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "req-42")
	h.ServeHTTP(httptest.NewRecorder(), req)
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	out := http.Header{}
	outbound.Apply(ctx, out, "", false)
	if out.Get("X-Request-ID") != "req-42" {
		t.Fatalf("X-Request-ID = %q", out.Get("X-Request-ID"))
	}
	if got := out.Get("traceparent"); got != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("traceparent = %q", got)
	}
}
