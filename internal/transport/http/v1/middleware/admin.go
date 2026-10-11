package middleware

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"sso/internal/storage"
	"sso/internal/transport/http/v1/contracts"
)

const (
	bearerPrefix = "Bearer "
	appIDHeader  = "X-App-Id"
)

// RequireAdmin пускает к админ-эндпоинтам только администраторов. Auth сам выпускает токены,
// поэтому проверяет их напрямую через свой сервис, без сетевого вызова.
// Без этой проверки любой, кто достучался до порта 8082, мог включить debug-логи под нагрузкой.
func RequireAdmin(logger *slog.Logger, authorizer contracts.AdminAuthorizer) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log := logger.With(
				slog.String("op", "http.middleware.RequireAdmin"),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)

			// Не сконфигурировано — отказываем, а не пропускаем.
			if authorizer == nil {
				log.Error("admin authorizer is nil")
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}

			token, ok := strings.CutPrefix(strings.TrimSpace(r.Header.Get("Authorization")), bearerPrefix)
			token = strings.TrimSpace(token)
			if !ok || token == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "bearer token is required")
				return
			}

			appID, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get(appIDHeader)), 10, 64)
			if err != nil || appID <= 0 {
				writeError(w, http.StatusBadRequest, "bad_request", "valid x-app-id header is required")
				return
			}

			userUUID, err := authorizer.ValidateToken(r.Context(), token, appID)
			if err != nil {
				if errors.Is(err, storage.ErrInvalidToken) || errors.Is(err, storage.ErrUserNotFound) {
					log.Warn("admin token rejected", slog.String("error", err.Error()))
					writeError(w, http.StatusUnauthorized, "unauthorized", "token validation failed")
					return
				}
				log.Error("admin token validation failed", slog.String("error", err.Error()))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}

			isAdmin, err := authorizer.IsAdminByUUID(r.Context(), userUUID)
			if err != nil {
				if errors.Is(err, storage.ErrUserNotFound) {
					writeError(w, http.StatusUnauthorized, "unauthorized", "token validation failed")
					return
				}
				log.Error("admin role check failed", slog.String("error", err.Error()))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}

			if !isAdmin {
				log.Warn("user is not admin", slog.String("user_uuid", userUUID.String()))
				writeError(w, http.StatusForbidden, "forbidden", "admin access required")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Тот же формат, что у Recovery: {"error":{"code":...,"message":...}}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
