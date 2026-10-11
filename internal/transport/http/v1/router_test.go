package v1

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"sso/internal/observability"
)

type stubLogLevel struct{}

func (stubLogLevel) SetLevel(string) error { return nil }
func (stubLogLevel) Level() slog.Level     { return slog.LevelInfo }
func (stubLogLevel) GetSlog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type stubReadiness struct{}

func (stubReadiness) Check(context.Context) error { return nil }

func requestsWithPath(path, code string) float64 {
	return testutil.ToFloat64(observability.MustMetrics().AuthHTTPRequestsTotal.WithLabelValues(http.MethodGet, path, code))
}

// Метка path — шаблон маршрута: сколько бы случайных URL ни пришло, рядов в Prometheus не прибавляется.
func TestRouterLabelsMetricsByRoutePattern(t *testing.T) {
	router := NewRouter(RouterDeps{LogLevelController: stubLogLevel{}, ReadinessChecker: stubReadiness{}})

	healthBefore := requestsWithPath("/health", "200")
	unmatchedBefore := requestsWithPath("unmatched", "404")

	for _, path := range []string{"/health", "/wp-admin", "/.env", "/random-1"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	if got := requestsWithPath("/health", "200") - healthBefore; got != 1 {
		t.Fatalf("/health counted %v times, want 1", got)
	}
	if got := requestsWithPath("unmatched", "404") - unmatchedBefore; got != 3 {
		t.Fatalf("unknown paths counted %v times under \"unmatched\", want 3", got)
	}
	if got := requestsWithPath("/wp-admin", "404"); got != 0 {
		t.Fatalf("raw path leaked into the label: /wp-admin counted %v times", got)
	}
}
