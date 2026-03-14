package observability

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	AuthServiceRequestsTotal   *prometheus.CounterVec
	AuthServiceRequestDuration *prometheus.HistogramVec
	AuthHTTPRequestsTotal      *prometheus.CounterVec
	AuthHTTPRequestDuration    *prometheus.HistogramVec
	AuthGRPCRequestsTotal      *prometheus.CounterVec
	AuthGRPCRequestDuration    *prometheus.HistogramVec
}

var (
	metricsInstance *Metrics
	metricsOnce     sync.Once
)

func MustMetrics() *Metrics {
	metricsOnce.Do(func() {
		metricsInstance = newMetrics()
	})
	return metricsInstance
}

func ObserveServiceRequest(method string, started time.Time, err error) {
	status := "ok"
	if err != nil {
		status = "error"
	}

	m := MustMetrics()
	m.AuthServiceRequestsTotal.WithLabelValues(method, status).Inc()
	m.AuthServiceRequestDuration.WithLabelValues(method).Observe(time.Since(started).Seconds())
}

func ObserveHTTPRequest(method, path string, status int, started time.Time) {
	m := MustMetrics()
	m.AuthHTTPRequestsTotal.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
	m.AuthHTTPRequestDuration.WithLabelValues(method, path).Observe(time.Since(started).Seconds())
}

func ObserveGRPCRequest(method, code string, started time.Time) {
	m := MustMetrics()
	m.AuthGRPCRequestsTotal.WithLabelValues(method, code).Inc()
	m.AuthGRPCRequestDuration.WithLabelValues(method).Observe(time.Since(started).Seconds())
}

func newMetrics() *Metrics {
	m := &Metrics{
		AuthServiceRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "auth",
				Subsystem: "service",
				Name:      "requests_total",
				Help:      "Total number of auth service requests.",
			},
			[]string{"method", "status"},
		),
		AuthServiceRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "auth",
				Subsystem: "service",
				Name:      "request_duration_seconds",
				Help:      "Auth service request duration in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method"},
		),
		AuthHTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "auth",
				Subsystem: "http",
				Name:      "requests_total",
				Help:      "Total number of HTTP requests handled by auth service.",
			},
			[]string{"method", "path", "status"},
		),
		AuthHTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "auth",
				Subsystem: "http",
				Name:      "request_duration_seconds",
				Help:      "HTTP request duration in seconds for auth service.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		AuthGRPCRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "auth",
				Subsystem: "grpc",
				Name:      "requests_total",
				Help:      "Total number of gRPC requests handled by auth service.",
			},
			[]string{"method", "code"},
		),
		AuthGRPCRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "auth",
				Subsystem: "grpc",
				Name:      "request_duration_seconds",
				Help:      "gRPC request duration in seconds for auth service.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method"},
		),
	}

	prometheus.MustRegister(
		m.AuthServiceRequestsTotal,
		m.AuthServiceRequestDuration,
		m.AuthHTTPRequestsTotal,
		m.AuthHTTPRequestDuration,
		m.AuthGRPCRequestsTotal,
		m.AuthGRPCRequestDuration,
	)

	return m
}
