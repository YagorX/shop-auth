package auth

import (
	"context"
	"errors"
	"net"
	"strings"

	authv1 "github.com/YagorX/shop-contracts/gen/go/proto/auth/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"sso/internal/storage"
)

const (
	ServiceName = "proto.auth.v1.AuthService"
	emptyValue  = 0
)

type Auth interface {
	Login(ctx context.Context, emailorname, password string, appID int, deviceID, ip, userAgent string) (refreshToken, accessToken string, err error)
	RegisterNewUser(ctx context.Context, username, email, password string) (uuid.UUID, error)
	IsAdminByUUID(ctx context.Context, userUUID uuid.UUID) (bool, error)
	ValidateToken(ctx context.Context, token string, appID int64) (uuid.UUID, error)
	Refresh(ctx context.Context, refreshToken string, appID int, deviceID, ip, userAgent string) (accessToken, newRefreshToken string, err error)
	Logout(ctx context.Context, refreshToken string, appID int64, deviceID string) error
}

type serverAPI struct {
	authv1.UnimplementedAuthServiceServer
	auth Auth
}

func Register(gRPC *grpc.Server, auth Auth) {
	authv1.RegisterAuthServiceServer(gRPC, &serverAPI{auth: auth})
}

func (s *serverAPI) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	if err := validateRegister(req); err != nil {
		return nil, err
	}

	userUUID, err := s.auth.RegisterNewUser(ctx, req.GetUsername(), req.GetEmail(), req.GetPassword())
	if err != nil {
		if errors.Is(err, storage.ErrUserExist) {
			return nil, status.Error(codes.AlreadyExists, "user already exists")
		}
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.RegisterResponse{UserUuid: userUUID.String()}, nil
}

func (s *serverAPI) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	if err := validateLogin(req); err != nil {
		return nil, err
	}

	ip := clientIP(ctx)
	ua := userAgent(ctx)

	refreshToken, accessToken, err := s.auth.Login(
		ctx,
		req.GetEmailOrName(),
		req.GetPassword(),
		int(req.GetAppId()),
		req.GetDeviceId(),
		ip,
		ua,
	)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidCredentials) {
			return nil, status.Error(codes.InvalidArgument, "invalid email or password")
		}
		if errors.Is(err, storage.ErrAppNotFound) {
			return nil, status.Error(codes.NotFound, "app not found")
		}
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *serverAPI) IsAdmin(ctx context.Context, req *authv1.IsAdminRequest) (*authv1.IsAdminResponse, error) {
	if err := validateIsAdmin(req); err != nil {
		return nil, err
	}

	userUUID, err := uuid.Parse(req.GetUserUuid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_uuid")
	}

	isAdmin, err := s.auth.IsAdminByUUID(ctx, userUUID)
	if err != nil {
		if errors.Is(err, storage.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.IsAdminResponse{IsAdmin: isAdmin}, nil
}

func (s *serverAPI) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest) (*authv1.ValidateTokenResponse, error) {
	if err := validateValidateToken(req); err != nil {
		return nil, err
	}

	userUUID, err := s.auth.ValidateToken(ctx, req.GetToken(), req.GetAppId())
	if err != nil {
		if errors.Is(err, storage.ErrInvalidToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		if errors.Is(err, storage.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		if errors.Is(err, storage.ErrAppNotFound) {
			return nil, status.Error(codes.NotFound, "app not found")
		}
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.ValidateTokenResponse{UserUuid: userUUID.String()}, nil
}

func (s *serverAPI) Refresh(ctx context.Context, req *authv1.RefreshRequest) (*authv1.RefreshResponse, error) {
	if err := validateRefresh(req); err != nil {
		return nil, err
	}

	ip := clientIP(ctx)
	ua := userAgent(ctx)

	access, refresh, err := s.auth.Refresh(
		ctx,
		req.GetRefreshToken(),
		int(req.GetAppId()),
		req.GetDeviceId(),
		ip,
		ua,
	)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		if errors.Is(err, storage.ErrAppNotFound) {
			return nil, status.Error(codes.NotFound, "app not found")
		}
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.RefreshResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

func (s *serverAPI) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	if err := validateLogout(req); err != nil {
		return nil, err
	}

	err := s.auth.Logout(ctx, req.GetRefreshToken(), req.GetAppId(), req.GetDeviceId())
	if err != nil {
		if errors.Is(err, storage.ErrInvalidToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.LogoutResponse{IsLogout: true}, nil
}

func validateRegister(req *authv1.RegisterRequest) error {
	if req.GetEmail() == "" {
		return status.Error(codes.InvalidArgument, "email is required")
	}
	if req.GetPassword() == "" {
		return status.Error(codes.InvalidArgument, "password is required")
	}
	if req.GetUsername() == "" {
		return status.Error(codes.InvalidArgument, "username is required")
	}
	return nil
}

func validateLogin(req *authv1.LoginRequest) error {
	if req.GetEmailOrName() == "" {
		return status.Error(codes.InvalidArgument, "email is required")
	}
	if req.GetPassword() == "" {
		return status.Error(codes.InvalidArgument, "password is required")
	}
	if req.GetAppId() == emptyValue {
		return status.Error(codes.InvalidArgument, "app_id is required")
	}
	if strings.TrimSpace(req.GetDeviceId()) == "" {
		return status.Error(codes.InvalidArgument, "device_id is required")
	}
	return nil
}

func validateIsAdmin(req *authv1.IsAdminRequest) error {
	if req.GetUserUuid() == "" {
		return status.Error(codes.InvalidArgument, "user_uuid is required")
	}
	return nil
}

func validateValidateToken(req *authv1.ValidateTokenRequest) error {
	if req.GetToken() == "" {
		return status.Error(codes.InvalidArgument, "token is required")
	}
	if req.GetAppId() == 0 {
		return status.Error(codes.InvalidArgument, "app_id is required")
	}
	return nil
}

func validateRefresh(req *authv1.RefreshRequest) error {
	if req.GetRefreshToken() == "" {
		return status.Error(codes.InvalidArgument, "refresh_token is required")
	}
	if req.GetAppId() == 0 {
		return status.Error(codes.InvalidArgument, "app_id is required")
	}
	if strings.TrimSpace(req.GetDeviceId()) == "" {
		return status.Error(codes.InvalidArgument, "device_id is required")
	}
	return nil
}

func validateLogout(req *authv1.LogoutRequest) error {
	if req.GetRefreshToken() == "" {
		return status.Error(codes.InvalidArgument, "refresh_token is required")
	}
	if req.GetAppId() == 0 {
		return status.Error(codes.InvalidArgument, "app_id is required")
	}
	if strings.TrimSpace(req.GetDeviceId()) == "" {
		return status.Error(codes.InvalidArgument, "device_id is required")
	}
	return nil
}

func userAgent(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	if v := md.Get("user-agent"); len(v) > 0 {
		return v[0]
	}
	if v := md.Get("x-user-agent"); len(v) > 0 {
		return v[0]
	}
	return ""
}

func clientIP(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("x-forwarded-for"); len(v) > 0 && v[0] != "" {
			parts := strings.Split(v[0], ",")
			return strings.TrimSpace(parts[0])
		}
		if v := md.Get("x-real-ip"); len(v) > 0 && v[0] != "" {
			return strings.TrimSpace(v[0])
		}
	}

	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}
