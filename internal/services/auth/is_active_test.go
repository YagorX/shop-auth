package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"sso/internal/domain/models"
	"sso/internal/storage"
)

// Деактивированный пользователь не возвращается SQL-запросами (условие is_active живёт в них),
// поэтому для сервиса он неотличим от удалённого: провайдер отдаёт ErrUserNotFound.
// Эти тесты фиксируют, во что такой ответ превращается в каждом методе,
// и отдельно — что отказ базы под ту же ветку не попадает.

var errDBDown = errors.New("db unavailable")

type stubUserProvider struct {
	user models.User
	err  error
}

func (s stubUserProvider) User(context.Context, string) (models.User, error) {
	return s.user, s.err
}

func (s stubUserProvider) UserByUUID(context.Context, uuid.UUID) (models.User, error) {
	return s.user, s.err
}

func (s stubUserProvider) IsAdminByUUID(context.Context, uuid.UUID) (bool, error) {
	return false, s.err
}

type stubAppProvider struct{}

func (stubAppProvider) App(_ context.Context, appID int64) (models.App, error) {
	return models.App{ID: appID, Name: "test-app", Secret: "test-secret"}, nil
}

type stubRefreshSessionProvider struct {
	session models.RefreshSession
	err     error
}

func (s stubRefreshSessionProvider) RefreshSessionByHash(context.Context, string, string, int64) (models.RefreshSession, error) {
	return s.session, s.err
}

type stubTokenManager struct {
	userUUID uuid.UUID
	err      error
}

func (s stubTokenManager) CreateToken(uuid.UUID, int64, time.Duration) (string, error) {
	return "", s.err
}

func (s stubTokenManager) ParseToken(string, int64) (uuid.UUID, error) {
	return s.userUUID, s.err
}

func activeSession(userUUID uuid.UUID) models.RefreshSession {
	return models.RefreshSession{
		UserUUID:  userUUID,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestLoginWithDeactivatedUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		providerErr error
		wantErr     error
	}{
		{
			name:        "deactivated user is indistinguishable from wrong credentials",
			providerErr: storage.ErrUserNotFound,
			wantErr:     storage.ErrInvalidCredentials,
		},
		{
			name:        "database failure is not reported as bad credentials",
			providerErr: errDBDown,
			wantErr:     errDBDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := New(
				testLogger(),
				nil,
				stubUserProvider{err: tt.providerErr},
				stubAppProvider{},
				nil,
				time.Hour,
				time.Hour,
				nil,
				nil,
				nil,
				nil,
				stubPasswordHasher{err: bcrypt.ErrMismatchedHashAndPassword},
				&fakeLoginLimiter{},
			)

			_, _, err := auth.Login(context.Background(), "user@example.com", "password", 1, "device-1", "", "")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRefreshWithDeactivatedUser(t *testing.T) {
	t.Parallel()

	userUUID := uuid.New()

	tests := []struct {
		name        string
		providerErr error
		wantErr     error
	}{
		{
			// Unauthenticated, а не Internal: клиент должен сбросить токены и пойти на вход.
			name:        "deactivated user invalidates the session",
			providerErr: storage.ErrUserNotFound,
			wantErr:     storage.ErrInvalidToken,
		},
		{
			name:        "database failure is not reported as invalid token",
			providerErr: errDBDown,
			wantErr:     errDBDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := New(
				testLogger(),
				nil,
				stubUserProvider{err: tt.providerErr},
				stubAppProvider{},
				nil,
				time.Hour,
				time.Hour,
				nil,
				stubRefreshSessionProvider{session: activeSession(userUUID)},
				nil,
				nil,
				nil,
				nil,
			)

			_, _, err := auth.Refresh(context.Background(), "refresh-token", 1, "device-1", "", "")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTokenWithDeactivatedUser(t *testing.T) {
	t.Parallel()

	userUUID := uuid.New()

	tests := []struct {
		name        string
		providerErr error
		wantErr     error
	}{
		{
			name:        "deactivated user fails validation",
			providerErr: storage.ErrUserNotFound,
			wantErr:     storage.ErrUserNotFound,
		},
		{
			// Иначе авария в Postgres выглядит как массовая невалидность токенов.
			name:        "database failure is not reported as missing user",
			providerErr: errDBDown,
			wantErr:     errDBDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := New(
				testLogger(),
				nil,
				stubUserProvider{err: tt.providerErr},
				stubAppProvider{},
				stubTokenManager{userUUID: userUUID},
				time.Hour,
				time.Hour,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
			)

			_, err := auth.ValidateToken(context.Background(), "access-token", 1)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
