package auth

import (
	"context"
	"errors"
	"testing"

	authv1 "github.com/YagorX/shop-contracts/gen/go/proto/auth/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"sso/internal/storage"
)

type stubAuth struct {
	logoutErr error
}

func (s stubAuth) Login(context.Context, string, string, int, string, string, string) (string, string, error) {
	return "", "", nil
}

func (s stubAuth) RegisterNewUser(context.Context, string, string, string) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (s stubAuth) IsAdminByUUID(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

func (s stubAuth) ValidateToken(context.Context, string, int64) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (s stubAuth) Refresh(context.Context, string, int, string, string, string) (string, string, error) {
	return "", "", nil
}

func (s stubAuth) Logout(context.Context, string, int64, string) error {
	return s.logoutErr
}

func TestServerLogout_ReturnsSuccess(t *testing.T) {
	srv := &serverAPI{auth: stubAuth{}}

	resp, err := srv.Logout(context.Background(), &authv1.LogoutRequest{
		RefreshToken: "refresh-token",
		AppId:        1,
		DeviceId:     "device-1",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if resp == nil || !resp.GetIsLogout() {
		t.Fatalf("expected successful logout response, got %#v", resp)
	}
}

func TestServerLogout_MapsInvalidTokenToUnauthenticated(t *testing.T) {
	srv := &serverAPI{auth: stubAuth{logoutErr: storage.ErrInvalidToken}}

	_, err := srv.Logout(context.Background(), &authv1.LogoutRequest{
		RefreshToken: "refresh-token",
		AppId:        1,
		DeviceId:     "device-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestServerLogout_MapsUnexpectedErrorsToInternal(t *testing.T) {
	srv := &serverAPI{auth: stubAuth{logoutErr: errors.New("db unavailable")}}

	_, err := srv.Logout(context.Background(), &authv1.LogoutRequest{
		RefreshToken: "refresh-token",
		AppId:        1,
		DeviceId:     "device-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.Internal {
		t.Fatalf("expected Internal, got %s", st.Code())
	}
}
