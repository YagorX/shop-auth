package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"sso/internal/storage"
)

type stubRefreshSessionRevoker struct {
	err error
}

func (s stubRefreshSessionRevoker) RevokeRefreshSession(context.Context, string, string, int64) error {
	return s.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLogout_ReturnsInvalidTokenForEmptyRefreshToken(t *testing.T) {
	auth := New(
		testLogger(),
		nil,
		nil,
		nil,
		nil,
		0,
		0,
		nil,
		nil,
		nil,
		stubRefreshSessionRevoker{},
	)

	err := auth.Logout(context.Background(), "", 1, "device-1")
	if !errors.Is(err, storage.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestLogout_IsIdempotentWhenRefreshSessionAlreadyAbsent(t *testing.T) {
	auth := New(
		testLogger(),
		nil,
		nil,
		nil,
		nil,
		0,
		0,
		nil,
		nil,
		nil,
		stubRefreshSessionRevoker{err: storage.ErrRefreshSessionNotFound},
	)

	err := auth.Logout(context.Background(), "refresh-token", 1, "device-1")
	if err != nil {
		t.Fatalf("expected nil error for idempotent logout, got %v", err)
	}
}

func TestLogout_ReturnsInfrastructureErrors(t *testing.T) {
	wantErr := errors.New("db unavailable")
	auth := New(
		testLogger(),
		nil,
		nil,
		nil,
		nil,
		0,
		0,
		nil,
		nil,
		nil,
		stubRefreshSessionRevoker{err: wantErr},
	)

	err := auth.Logout(context.Background(), "refresh-token", 1, "device-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped infrastructure error, got %v", err)
	}
}
