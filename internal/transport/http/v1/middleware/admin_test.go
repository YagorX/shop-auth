package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"sso/internal/storage"
)

type stubAuthorizer struct {
	validateErr error
	isAdmin     bool
	isAdminErr  error
}

func (s stubAuthorizer) ValidateToken(context.Context, string, int64) (uuid.UUID, error) {
	return uuid.New(), s.validateErr
}

func (s stubAuthorizer) IsAdminByUUID(context.Context, uuid.UUID) (bool, error) {
	return s.isAdmin, s.isAdminErr
}

func adminRequest(token, appID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/admin/log-level?level=debug", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if appID != "" {
		req.Header.Set("X-App-Id", appID)
	}

	return req
}

func TestRequireAdmin(t *testing.T) {
	errDBDown := errors.New("db down")

	tests := []struct {
		name       string
		authorizer stubAuthorizer
		token      string
		appID      string
		want       int
	}{
		{name: "admin", authorizer: stubAuthorizer{isAdmin: true}, token: "t", appID: "1", want: http.StatusOK},
		{name: "not admin", authorizer: stubAuthorizer{}, token: "t", appID: "1", want: http.StatusForbidden},
		{name: "no token", authorizer: stubAuthorizer{isAdmin: true}, appID: "1", want: http.StatusUnauthorized},
		{name: "no app id", authorizer: stubAuthorizer{isAdmin: true}, token: "t", want: http.StatusBadRequest},
		{
			name:       "invalid token",
			authorizer: stubAuthorizer{validateErr: storage.ErrInvalidToken, isAdmin: true},
			token:      "t", appID: "1", want: http.StatusUnauthorized,
		},
		{
			name:       "deactivated user",
			authorizer: stubAuthorizer{validateErr: fmt.Errorf("auth.ValidateToken: %w", storage.ErrUserNotFound), isAdmin: true},
			token:      "t", appID: "1", want: http.StatusUnauthorized,
		},
		{
			// Отказ базы — не повод ни пускать, ни выдавать его за плохой токен.
			name:       "db down on token check",
			authorizer: stubAuthorizer{validateErr: errDBDown, isAdmin: true},
			token:      "t", appID: "1", want: http.StatusInternalServerError,
		},
		{
			name:       "db down on role check",
			authorizer: stubAuthorizer{isAdminErr: errDBDown},
			token:      "t", appID: "1", want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			rec := httptest.NewRecorder()
			RequireAdmin(logger, tt.authorizer)(next).ServeHTTP(rec, adminRequest(tt.token, tt.appID))

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body.String())
			}
			if called != (tt.want == http.StatusOK) {
				t.Fatalf("handler called = %v, want %v", called, tt.want == http.StatusOK)
			}
		})
	}
}

// Роутер без Admin в зависимостях не должен открывать админ-эндпоинт.
func TestRequireAdminWithoutAuthorizerFailsClosed(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	RequireAdmin(nil, nil)(next).ServeHTTP(rec, adminRequest("t", "1"))

	if rec.Code == http.StatusOK {
		t.Fatal("admin endpoint open without an authorizer")
	}
}
