// Package obs holds logbook's logging, request IDs, metrics and traces.
package obs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel/trace"
)

type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestIDFrom(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h ctxHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return ctxHandler{h.Handler.WithAttrs(as)}
}

func (h ctxHandler) WithGroup(name string) slog.Handler {
	return ctxHandler{h.Handler.WithGroup(name)}
}

// NewLogger returns a JSON logger that adds the request ID and trace ID from
// the context of each record.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(ctxHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})})
}

type requestIDKey struct{}

func validRequestID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// RequestID keeps a valid incoming X-Request-ID or makes a new one, puts it in
// the request context and echoes it on the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = ulid.Make().String()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom returns the request ID in ctx, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// statusWriter records the status code and keeps Flush working for streamed responses.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// method returns the request method, or OTHER for one net/http does not
// define, so a client can't create unbounded metric labels.
func method(r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return r.Method
	}
	return "OTHER"
}

type routeKey struct{}

type routeHolder struct{ pattern string }

// withRoute makes sure r carries a holder for the matched pattern.
func withRoute(r *http.Request) *http.Request {
	if _, ok := r.Context().Value(routeKey{}).(*routeHolder); ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), routeKey{}, &routeHolder{}))
}

// Routes wraps the ServeMux and records the pattern it matched, so the
// middleware outside sees it even when a handler in between copied the request.
func Routes(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if h, ok := r.Context().Value(routeKey{}).(*routeHolder); ok {
			h.pattern = r.Pattern
		}
	})
}

func route(r *http.Request) string {
	p := r.Pattern
	if h, ok := r.Context().Value(routeKey{}).(*routeHolder); ok && h.pattern != "" {
		p = h.pattern
	}
	if p == "" {
		return "unmatched"
	}
	return p
}

// AccessLog writes one line per request.
func AccessLog(l *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		r = withRoute(r)
		next.ServeHTTP(sw, r)
		l.InfoContext(r.Context(), "request", "method", method(r), "route", route(r), "status", sw.code(),
			"duration_ms", time.Since(start).Milliseconds())
	})
}
