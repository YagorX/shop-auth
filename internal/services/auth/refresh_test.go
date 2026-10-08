package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"sso/internal/domain/models"
	"sso/internal/storage"
)

// Unauthenticated на Refresh заставляет клиента стереть токены и отправить пользователя на вход.
// Поэтому ErrInvalidToken — только когда точно известно, что токен плохой. Отказ базы, включая
// истёкшее ожидание соединения из пула, должен уйти наружу как есть: иначе failover Patroni
// разлогинивает всех, кто в этот момент обновлялся, и превращается в шторм логинов на bcrypt.

type stubRefreshSessionRotator struct {
	err error
}

func (s stubRefreshSessionRotator) RotateRefreshSession(context.Context, string, string, time.Time, string, string, string, int64) error {
	return s.err
}

func newRefreshTestAuth(session models.RefreshSession, lookupErr, rotateErr error) *Auth {
	userUUID := session.UserUUID

	return New(
		testLogger(),
		nil,
		stubUserProvider{user: models.User{UUID: userUUID, Username: "user"}},
		stubAppProvider{},
		nil,
		time.Hour,
		time.Hour,
		nil,
		stubRefreshSessionProvider{session: session, err: lookupErr},
		stubRefreshSessionRotator{err: rotateErr},
		nil,
	)
}

func TestRefreshSessionLookup(t *testing.T) {
	t.Parallel()

	revokedAt := time.Now().Add(-time.Minute)

	revoked := activeSession(uuid.New())
	revoked.RevokedAt = &revokedAt

	expired := activeSession(uuid.New())
	expired.ExpiresAt = time.Now().Add(-time.Minute)

	tests := []struct {
		name      string
		session   models.RefreshSession
		lookupErr error
		wantErr   error
	}{
		{
			name:      "unknown token is invalid",
			session:   activeSession(uuid.New()),
			lookupErr: fmt.Errorf("storage.postgres.RefreshSessionByHash: %w", storage.ErrRefreshSessionNotFound),
			wantErr:   storage.ErrInvalidToken,
		},
		{
			name:    "revoked session is invalid",
			session: revoked,
			wantErr: storage.ErrInvalidToken,
		},
		{
			name:    "expired session is invalid",
			session: expired,
			wantErr: storage.ErrInvalidToken,
		},
		{
			name:      "database failure is not reported as invalid token",
			session:   activeSession(uuid.New()),
			lookupErr: errDBDown,
			wantErr:   errDBDown,
		},
		{
			// Полный пул не даёт ошибку сразу: запрос ждёт соединение до конца ctx.
			name:      "pool wait past deadline is not reported as invalid token",
			session:   activeSession(uuid.New()),
			lookupErr: fmt.Errorf("storage.postgres.RefreshSessionByHash: %w", context.DeadlineExceeded),
			wantErr:   context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := newRefreshTestAuth(tt.session, tt.lookupErr, nil)

			_, _, err := auth.Refresh(context.Background(), "refresh-token", 1, "device-1", "", "")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != storage.ErrInvalidToken && errors.Is(err, storage.ErrInvalidToken) {
				t.Fatalf("err = %v: инфраструктурная ошибка выдана за невалидный токен", err)
			}
		})
	}
}

func TestRefreshRotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rotateErr error
		wantErr   error
	}{
		{
			// Два параллельных refresh с одним токеном: второй находит сессию уже отозванной.
			name:      "lost rotation race is invalid token",
			rotateErr: fmt.Errorf("storage.postgres.RotateRefreshSession: %w", storage.ErrRefreshSessionNotFound),
			wantErr:   storage.ErrInvalidToken,
		},
		{
			name:      "database failure is not reported as invalid token",
			rotateErr: errDBDown,
			wantErr:   errDBDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := newRefreshTestAuth(activeSession(uuid.New()), nil, tt.rotateErr)

			_, _, err := auth.Refresh(context.Background(), "refresh-token", 1, "device-1", "", "")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRefreshSuccess(t *testing.T) {
	t.Parallel()

	auth := newRefreshTestAuth(activeSession(uuid.New()), nil, nil)

	access, refresh, err := auth.Refresh(context.Background(), "refresh-token", 1, "device-1", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if access == "" || refresh == "" {
		t.Fatalf("пустые токены: access=%q refresh=%q", access, refresh)
	}
	if refresh == "refresh-token" {
		t.Fatal("refresh-токен не сменился при ротации")
	}
}
