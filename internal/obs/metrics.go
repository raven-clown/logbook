package obs

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics are the Prometheus metrics of one server.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics returns metrics on their own registry, with the Go and process collectors.
func NewMetrics() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "logbook_http_requests_total", Help: "HTTP requests by method, route and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "logbook_http_request_duration_seconds", Help: "HTTP request duration by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	m.reg.MustRegister(m.requests, m.duration, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Wrap counts and times requests. The route label comes from Routes, or from
// the request itself when Wrap sits directly outside the ServeMux.
func (m *Metrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		r = withRoute(r)
		next.ServeHTTP(sw, r)
		rt := route(r)
		m.requests.WithLabelValues(method(r), rt, strconv.Itoa(sw.code())).Inc()
		m.duration.WithLabelValues(method(r), rt).Observe(time.Since(start).Seconds())
	})
}

// Handler serves the metrics in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
