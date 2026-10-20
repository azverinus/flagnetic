package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// Metrics are the edge's request metrics.
type Metrics struct {
	requests  *prometheus.CounterVec
	durations *prometheus.HistogramVec
}

// NewMetrics registers the request metrics on reg. Their labels are the
// method, bounded to the known ones, and the route pattern, never the path,
// so their cardinality stays bounded whatever clients send.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "http", Name: "requests_total",
			Help: "Requests served, by method, route and status code.",
		}, []string{"method", "route", "code"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Subsystem: "http", Name: "request_duration_seconds",
			Help:    "Time to serve a request, by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	reg.MustRegister(m.requests, m.durations)
	return m
}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

func (m *Metrics) observe(method, route string, status int, took time.Duration) {
	if m == nil {
		return
	}
	if !knownMethods[method] {
		method = "other"
	}
	code := strconv.Itoa(status)
	if status == 0 {
		code = "aborted"
	}
	m.requests.WithLabelValues(method, route, code).Inc()
	m.durations.WithLabelValues(method, route).Observe(took.Seconds())
}
