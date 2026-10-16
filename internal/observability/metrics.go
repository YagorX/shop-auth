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
	AuthPasswordHashDuration   *prometheus.HistogramVec
	AuthPasswordHashSlots      prometheus.Gauge
	AuthPasswordHashInflight   prometheus.Gauge
	AuthPasswordHashWait       prometheus.Histogram
	AuthLoginFailuresTotal     *prometheus.CounterVec
	AuthLoginLockoutsTotal     prometheus.Counter
	AuthLoginLimiterKeys       prometheus.Gauge
}

var (
	metricsInstance *Metrics
	metricsOnce     sync.Once
)

// От 100 мкс: ValidateToken отвечает за доли миллисекунды и в DefBuckets (от 5 мс)
// целиком лёг бы в первый бакет. 5 с — серверный дедлайн; бакеты выше него — сигнализация:
// сюда попадает только то, что игнорирует ctx. Без них такой запрос лёг бы в +Inf,
// и histogram_quantile показал бы ровно 5 с — неотличимо от «упёрлись в дедлайн».
var latencyBuckets = []float64{
	0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005,
	0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 7.5, 10,
}

// bcrypt при DefaultCost — 50–100 мс; сетка гуще именно там:
// в общей latencyBuckets на этот диапазон приходится всего две границы.
var passwordHashBuckets = []float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.5, 1}

// Ожидание слота семафора bcrypt: от «сразу» до max_wait (150 мс по умолчанию) и чуть выше.
// Сетка гуще у верхней границы — туда ложатся отказы по перегрузке.
var passwordHashWaitBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.2, 0.5}

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

func ObservePasswordHash(op string, started time.Time) {
	MustMetrics().AuthPasswordHashDuration.WithLabelValues(op).Observe(time.Since(started).Seconds())
}

func SetPasswordHashSlots(slots int64) {
	MustMetrics().AuthPasswordHashSlots.Set(float64(slots))
}

func ObservePasswordHashWait(started time.Time) {
	MustMetrics().AuthPasswordHashWait.Observe(time.Since(started).Seconds())
}

func PasswordHashSlotAcquired() {
	MustMetrics().AuthPasswordHashInflight.Inc()
}

func PasswordHashSlotReleased() {
	MustMetrics().AuthPasswordHashInflight.Dec()
}

// reason — invalid_credentials или locked.
func LoginFailed(reason string) {
	MustMetrics().AuthLoginFailuresTotal.WithLabelValues(reason).Inc()
}

func LoginLocked() {
	MustMetrics().AuthLoginLockoutsTotal.Inc()
}

func SetLoginLimiterKeys(count int) {
	MustMetrics().AuthLoginLimiterKeys.Set(float64(count))
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
				Buckets:   latencyBuckets,
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
				Buckets:   latencyBuckets,
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
				Buckets:   latencyBuckets,
			},
			[]string{"method"},
		),
		AuthPasswordHashDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "auth",
				Subsystem: "password",
				Name:      "hash_duration_seconds",
				Help:      "Duration of password hashing in seconds.",
				Buckets:   passwordHashBuckets,
			},
			[]string{"op"},
		),
		// Меток нет: семафор один на compare и hash.
		AuthPasswordHashSlots: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "auth",
				Subsystem: "password",
				Name:      "hash_slots",
				Help:      "Capacity of the password hashing semaphore.",
			},
		),
		AuthPasswordHashInflight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "auth",
				Subsystem: "password",
				Name:      "hash_inflight",
				Help:      "Password hashing semaphore slots currently in use.",
			},
		),
		AuthPasswordHashWait: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "auth",
				Subsystem: "password",
				Name:      "hash_wait_seconds",
				Help:      "Time spent waiting for a password hashing slot in seconds, including rejected waits.",
				Buckets:   passwordHashWaitBuckets,
			},
		),
		AuthLoginFailuresTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "auth",
				Subsystem: "login",
				Name:      "failures_total",
				Help:      "Total number of failed login attempts.",
			},
			// Только причина: метка с логином дала бы ряд на каждый email из перебора и персональные данные в Prometheus.
			[]string{"reason"},
		),
		AuthLoginLockoutsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "auth",
				Subsystem: "login",
				Name:      "lockouts_total",
				Help:      "Total number of login lockouts due to too many failed attempts.",
			},
		),
		AuthLoginLimiterKeys: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "auth",
				Subsystem: "login",
				Name:      "limiter_keys",
				Help:      "Current number of keys in the login limiter.",
			},
		),
	}

	prometheus.MustRegister(
		m.AuthServiceRequestsTotal,
		m.AuthServiceRequestDuration,
		m.AuthHTTPRequestsTotal,
		m.AuthHTTPRequestDuration,
		m.AuthGRPCRequestsTotal,
		m.AuthGRPCRequestDuration,
		m.AuthPasswordHashDuration,
		m.AuthPasswordHashSlots,
		m.AuthPasswordHashInflight,
		m.AuthPasswordHashWait,
		m.AuthLoginFailuresTotal,
		m.AuthLoginLockoutsTotal,
		m.AuthLoginLimiterKeys,
	)

	// Ряды с нулём видны сразу: rate() и алерты работают и до первой неудачи.
	for _, reason := range []string{"invalid_credentials", "locked"} {
		m.AuthLoginFailuresTotal.WithLabelValues(reason)
	}

	return m
}
