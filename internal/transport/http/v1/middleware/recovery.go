package middleware

import (
	"log/slog"
	"net/http"
)

func Recovery(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error(
						"http panic recovered",
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.Any("panic", rec),
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal server error"}}`))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
