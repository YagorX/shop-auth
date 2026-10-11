package v1

import (
	"net/http"
	"time"

	"sso/internal/observability"
	"sso/internal/transport/http/v1/contracts"
	"sso/internal/transport/http/v1/handlers"
	"sso/internal/transport/http/v1/middleware"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type RouterDeps struct {
	LogLevelController contracts.LogLevelController
	ReadinessChecker   contracts.ReadinessChecker
	// Проверка токена и роли для /admin/*. Если не передан, админ-эндпоинты отказывают всем.
	Admin contracts.AdminAuthorizer
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()
	logger := deps.LogLevelController.GetSlog()

	healthHandler := handlers.NewHealthHandler(deps.ReadinessChecker)
	adminHandler := handlers.NewAdminHandler(deps.LogLevelController)

	mux.HandleFunc("/health", healthHandler.Health)
	mux.HandleFunc("/ready", healthHandler.Ready)
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/admin/log-level", middleware.RequireAdmin(logger, deps.Admin)(http.HandlerFunc(adminHandler.LogLevel)))

	return middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		mux.ServeHTTP(recorder, r)

		// Шаблон маршрута, а не сырой путь: иначе каждый случайный URL от сканера — новые ряды
		// в Prometheus, и память растёт без предела. ServeMux пишет Pattern в этот же *Request.
		path := r.Pattern
		if path == "" {
			path = "unmatched"
		}
		observability.ObserveHTTPRequest(r.Method, path, recorder.status, started)
	}),
		middleware.Recovery(logger))
}
