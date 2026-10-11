package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	authv1 "github.com/YagorX/shop-contracts/gen/go/proto/auth/v1"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"sso/internal/storage"
)

type stubAuth struct {
	loginErr    error
	registerErr error
	logoutErr   error
}

func (s stubAuth) Login(context.Context, string, string, int, string, string, string) (string, string, error) {
	return "", "", s.loginErr
}

func (s stubAuth) RegisterNewUser(context.Context, string, string, string) (uuid.UUID, error) {
	return uuid.Nil, s.registerErr
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

func validLoginRequest() *authv1.LoginRequest {
	return &authv1.LoginRequest{
		EmailOrName: "user",
		Password:    "secret",
		AppId:       1,
		DeviceId:    "device-1",
	}
}

func validRegisterRequest() *authv1.RegisterRequest {
	return &authv1.RegisterRequest{
		Username: "user",
		Email:    "user@example.com",
		Password: "secret",
	}
}

// Перегрузка хешера — честный ResourceExhausted с подсказкой паузы, а не Internal:
// клиент должен понять, что повторить можно и когда.
func TestServerMapsOverloadToResourceExhausted(t *testing.T) {
	t.Parallel()

	overloaded := fmt.Errorf("auth.Login: %w", storage.ErrOverloaded)

	calls := map[string]func(*serverAPI) error{
		"Login": func(srv *serverAPI) error {
			_, err := srv.Login(context.Background(), validLoginRequest())
			return err
		},
		"Register": func(srv *serverAPI) error {
			_, err := srv.Register(context.Background(), validRegisterRequest())
			return err
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := &serverAPI{auth: stubAuth{loginErr: overloaded, registerErr: overloaded}}

			st, ok := status.FromError(call(srv))
			if !ok {
				t.Fatal("expected grpc status error")
			}
			if st.Code() != codes.ResourceExhausted {
				t.Fatalf("code = %s, want ResourceExhausted", st.Code())
			}

			var retry *errdetails.RetryInfo
			for _, d := range st.Details() {
				if ri, ok := d.(*errdetails.RetryInfo); ok {
					retry = ri
				}
			}
			if retry == nil {
				t.Fatalf("в деталях статуса нет RetryInfo: %v", st.Details())
			}
			if retry.GetRetryDelay().AsDuration() <= 0 {
				t.Fatalf("RetryDelay = %v, want > 0", retry.GetRetryDelay().AsDuration())
			}
		})
	}
}

func TestServerLoginErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		loginErr error
		want     codes.Code
	}{
		{
			name:     "wrong password",
			loginErr: fmt.Errorf("auth.Login: %w", storage.ErrInvalidCredentials),
			want:     codes.Unauthenticated,
		},
		{
			// Таймаут клиента — не перегрузка: здесь он становится Internal,
			// а в DeadlineExceeded его переводит contextErrorInterceptor по ctx.Err().
			name:     "client timeout is not overload",
			loginErr: fmt.Errorf("auth.Login: %w", context.DeadlineExceeded),
			want:     codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := &serverAPI{auth: stubAuth{loginErr: tt.loginErr}}

			_, err := srv.Login(context.Background(), validLoginRequest())
			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %s, want %s", got, tt.want)
			}
		})
	}
}

// bcrypt учитывает только первые 72 байта: длиннее Register падал бы с Internal,
// а в Login к 72-байтному паролю можно было бы дописать что угодно.
func TestServerRejectsPasswordsOverBcryptLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		want     codes.Code
	}{
		{name: "72 bytes", password: strings.Repeat("a", 72), want: codes.OK},
		{name: "73 bytes", password: strings.Repeat("a", 73), want: codes.InvalidArgument},
		// 37 кириллических букв — 74 байта в UTF-8: предел в байтах, а не в символах.
		{name: "37 cyrillic letters", password: strings.Repeat("я", 37), want: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := &serverAPI{auth: stubAuth{}}

			register := validRegisterRequest()
			register.Password = tt.password
			_, err := srv.Register(context.Background(), register)
			if got := status.Code(err); got != tt.want {
				t.Fatalf("Register: code = %s, want %s", got, tt.want)
			}

			login := validLoginRequest()
			login.Password = tt.password
			_, err = srv.Login(context.Background(), login)
			if got := status.Code(err); got != tt.want {
				t.Fatalf("Login: code = %s, want %s", got, tt.want)
			}
		})
	}
}
