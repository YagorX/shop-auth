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
	mux.HandleFunc("/admin/log-level", adminHandler.LogLevel)

	return middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		mux.ServeHTTP(recorder, r)

		observability.ObserveHTTPRequest(r.Method, r.URL.Path, recorder.status, started)
	}),
		middleware.Recovery(logger))
}
