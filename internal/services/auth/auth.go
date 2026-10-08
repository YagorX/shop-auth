package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"sso/internal/domain/models"
	"sso/internal/lib/jwt"
	"sso/internal/lib/logger/sl"
	"sso/internal/observability"
	"sso/internal/storage"
)

type Auth struct {
	userSaver       UserSaver
	userProvider    UserProvider
	appProvider     AppProvider
	tokenManager    TokenManager
	refreshSaver    RefreshSessionSaver
	refreshProvider RefreshSessionProvider
	refreshRotator  RefreshSessionRotator
	refreshRevoker  RefreshSessionRevoker
	hasher          PasswordHasher
	log             *slog.Logger
	tokenTTL        time.Duration
	refreshTTL      time.Duration
}

type UserSaver interface {
	SaveUser(ctx context.Context, username, email string, passHash []byte) (uuid.UUID, error)
}

type UserProvider interface {
	User(ctx context.Context, email string) (models.User, error)
	UserByUUID(ctx context.Context, userUUID uuid.UUID) (models.User, error)
	IsAdminByUUID(ctx context.Context, userUUID uuid.UUID) (bool, error)
}

type TokenManager interface {
	CreateToken(userUUID uuid.UUID, appID int64, ttl time.Duration) (string, error)
	ParseToken(token string, appID int64) (uuid.UUID, error)
}

type AppProvider interface {
	App(ctx context.Context, appID int64) (models.App, error)
}

type RefreshSessionSaver interface {
	SaveRefreshSession(ctx context.Context, userUUID uuid.UUID, refreshHash string, expiresAt time.Time, ip, userAgent, deviceID string, appID int64) error
}

type RefreshSessionProvider interface {
	RefreshSessionByHash(ctx context.Context, hash string, deviceID string, appID int64) (models.RefreshSession, error)
}

type RefreshSessionRotator interface {
	RotateRefreshSession(ctx context.Context, oldHash, newHash string, newExpiresAt time.Time, ip, userAgent, deviceID string, appID int64) error
}

type RefreshSessionRevoker interface {
	RevokeRefreshSession(ctx context.Context, hash string, deviceID string, appID int64) error
}

func New(
	log *slog.Logger,
	userSaver UserSaver,
	userProvider UserProvider,
	appProvider AppProvider,
	tokenManager TokenManager,
	tokenTTL time.Duration,
	refreshTTL time.Duration,
	refreshSaver RefreshSessionSaver,
	refreshProvider RefreshSessionProvider,
	refreshRotator RefreshSessionRotator,
	refreshRevoker RefreshSessionRevoker,
	hasher PasswordHasher,
) *Auth {
	return &Auth{
		userSaver:       userSaver,
		userProvider:    userProvider,
		appProvider:     appProvider,
		tokenManager:    tokenManager,
		refreshSaver:    refreshSaver,
		refreshProvider: refreshProvider,
		refreshRotator:  refreshRotator,
		refreshRevoker:  refreshRevoker,
		log:             log,
		tokenTTL:        tokenTTL,
		refreshTTL:      refreshTTL,
		hasher:          hasher,
	}
}

func (a *Auth) Login(ctx context.Context, emailorname, password string, appID int, deviceID, ip, userAgent string) (refreshToken, accessToken string, err error) {
	const op = "auth.Login"
	started := time.Now()
	defer func() {
		observability.ObserveServiceRequest(op, started, err)
	}()

	log := a.log.With(
		slog.String("op", op),
		slog.String("emailorname", emailorname),
		// password либо не логируем, либо логируем в замаскированном виде
	)

	log.Info("attempting to login user")

	user, err := a.userProvider.User(ctx, emailorname)
	if err != nil {
		if errors.Is(err, storage.ErrUserNotFound) {
			a.log.Warn("user not found", sl.Err(err))
			return "", "", fmt.Errorf("%s: %w", op, storage.ErrInvalidCredentials)
		}
		a.log.Error("failed to get user", sl.Err(err))
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	// hashStarted := time.Now()
	// err = bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password))
	// observability.ObservePasswordHash("compare", hashStarted)
	// if err != nil {
	// 	a.log.Info("invalid credentials")
	// 	return "", "", fmt.Errorf("%s: %w", op, storage.ErrInvalidCredentials)
	// }

	err = a.hasher.Compare(ctx, user.PasswordHash, password)
	if err != nil {
		if errors.Is(err, storage.ErrOverloaded) {
			a.log.Warn("password comparison failed due to overload", sl.Err(err))
		} else {
			a.log.Info("invalid credentials")
		}
		return "", "", fmt.Errorf("%s: %w", op, storage.ErrInvalidCredentials)
	}

	app, err := a.appProvider.App(ctx, int64(appID))
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	log.Info("user logged in successfully")

	access, err := jwt.NewToken(user, app, a.tokenTTL)
	if err != nil {
		a.log.Error("failed to generate access token", sl.Err(err))
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	refreshRaw, err := newRefreshToken(64)
	if err != nil {
		a.log.Error("failed to generate refresh token", sl.Err(err))
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	refreshHash := sha256Hex(refreshRaw)
	refreshExp := time.Now().Add(a.refreshTTL)

	if err := a.refreshSaver.SaveRefreshSession(ctx, user.UUID, refreshHash, refreshExp, ip, userAgent, deviceID, int64(appID)); err != nil {
		a.log.Error("save refresh failed", sl.Err(err))
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	return refreshRaw, access, nil
}

func (a *Auth) RegisterNewUser(ctx context.Context, username, email, password string) (userUUID uuid.UUID, err error) {
	const op = "auth.RegisterNewUser"
	started := time.Now()
	defer func() {
		observability.ObserveServiceRequest(op, started, err)
	}()

	log := a.log.With(
		slog.String("op", op),
		slog.String("email", email),
		slog.String("username", username),
	)

	log.Info("registering user")

	// hashStarted := time.Now()
	// passHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	// observability.ObservePasswordHash("hash", hashStarted)
	// if err != nil {
	// 	log.Error("failed to generate password hash", sl.Err(err))
	// 	return uuid.Nil, fmt.Errorf("%s: %w", op, err)
	// }

	passHash, err := a.hasher.Hash(ctx, password)
	if err != nil {
		log.Error("failed to generate password hash", sl.Err(err))
		return uuid.Nil, fmt.Errorf("%s: %w", op, err)
	}

	userUUID, err = a.userSaver.SaveUser(ctx, username, email, passHash)
	if err != nil {
		log.Error("failed to save user", sl.Err(err))
		if errors.Is(err, storage.ErrUserExist) {
			return uuid.Nil, fmt.Errorf("%s: %w", op, storage.ErrUserExist)
		}
		return uuid.Nil, fmt.Errorf("%s: %w", op, err)
	}

	return userUUID, nil
}

func (a *Auth) IsAdminByUUID(ctx context.Context, userUUID uuid.UUID) (isAdmin bool, err error) {
	const op = "auth.IsAdminByUUID"
	started := time.Now()
	defer func() {
		observability.ObserveServiceRequest(op, started, err)
	}()

	log := a.log.With(
		slog.String("op", op),
		slog.String("userUUID", userUUID.String()),
	)

	log.Info("checking if user is admin")

	if userUUID == uuid.Nil {
		return false, storage.ErrUserNotFound
	}

	isAdmin, err = a.userProvider.IsAdminByUUID(ctx, userUUID)
	if err != nil {
		return false, fmt.Errorf("%s: %w", op, err)
	}

	log.Info("checked if user is admin", slog.Bool("is_admin", isAdmin))

	return isAdmin, nil
}

func (a *Auth) ValidateToken(ctx context.Context, token string, appID int64) (userUUID uuid.UUID, err error) {
	const op = "auth.ValidateToken"
	started := time.Now()
	defer func() {
		observability.ObserveServiceRequest(op, started, err)
	}()

	userUUID, err = a.tokenManager.ParseToken(token, appID)
	if err != nil {
		return uuid.Nil, storage.ErrInvalidToken
	}

	log := a.log.With(
		slog.String("op", op),
	)

	log.Info("checking token")

	// Деактивированный пользователь не возвращается запросом, это ErrUserNotFound.
	// Отказ базы под ту же ошибку подводить нельзя: иначе авария в Postgres выглядит
	// как невалидные токены и разлогинивает всех разом.
	if _, err = a.userProvider.UserByUUID(ctx, userUUID); err != nil {
		if errors.Is(err, storage.ErrUserNotFound) {
			return uuid.Nil, storage.ErrUserNotFound
		}

		a.log.Error("failed to get user", sl.Err(err))

		return uuid.Nil, fmt.Errorf("%s: %w", op, err)
	}

	return userUUID, nil
}

func (a *Auth) Refresh(ctx context.Context, refreshToken string, appID int, deviceID, ip, userAgent string) (accessToken, RefreshToken string, err error) {
	const op = "auth.Refresh"
	started := time.Now()
	defer func() {
		observability.ObserveServiceRequest(op, started, err)
	}()

	if refreshToken == "" {
		return "", "", storage.ErrInvalidToken
	}

	log := a.log.With(
		slog.String("op", op),
	)

	log.Info("refresh tokens")

	oldHash := sha256Hex(refreshToken)

	sess, err := a.refreshProvider.RefreshSessionByHash(ctx, oldHash, deviceID, int64(appID))
	if errors.Is(err, storage.ErrRefreshSessionNotFound) {
		return "", "", storage.ErrInvalidToken
	}
	if err != nil {
		log.Error("failed to get refresh session", sl.Err(err))
		return "", "", fmt.Errorf("%s: %w", op, err)
	}
	if sess.RevokedAt != nil || time.Now().After(sess.ExpiresAt) {
		return "", "", storage.ErrInvalidToken
	}

	app, err := a.appProvider.App(ctx, int64(appID))
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	user, err := a.userProvider.UserByUUID(ctx, sess.UserUUID)
	if err != nil {
		// Пользователь удалён или деактивирован — сессия больше не действительна.
		// Клиенту нужен Unauthenticated, чтобы он сбросил токены и отправил на вход,
		// а не Internal, как было бы при ErrUserNotFound без ветки в транспорте.
		if errors.Is(err, storage.ErrUserNotFound) {
			return "", "", storage.ErrInvalidToken
		}

		a.log.Error("failed to get user", sl.Err(err))

		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	access, err := jwt.NewToken(user, app, a.tokenTTL)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	refreshRaw, err := newRefreshToken(64)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	newHash := sha256Hex(refreshRaw)
	newExp := time.Now().Add(a.refreshTTL)

	err = a.refreshRotator.RotateRefreshSession(ctx, oldHash, newHash, newExp, ip, userAgent, deviceID, int64(appID))
	if errors.Is(err, storage.ErrRefreshSessionNotFound) {
		return "", "", storage.ErrInvalidToken
	}
	if err != nil {
		log.Error("failed to rotate refresh session", sl.Err(err))
		return "", "", fmt.Errorf("%s: %w", op, err)
	}

	return access, refreshRaw, nil
}

func (a *Auth) Logout(ctx context.Context, refreshToken string, appID int64, deviceID string) (err error) {
	const op = "auth.Logout"
	started := time.Now()
	defer func() {
		observability.ObserveServiceRequest(op, started, err)
	}()

	log := a.log.With(
		slog.String("op", op),
	)

	log.Info("logout")

	if refreshToken == "" {
		return storage.ErrInvalidToken
	}

	hash := sha256Hex(refreshToken)
	err = a.refreshRevoker.RevokeRefreshSession(ctx, hash, deviceID, appID)
	if err != nil {
		if errors.Is(err, storage.ErrRefreshSessionNotFound) {
			log.Info("refresh session already absent during logout")
			return nil
		}
		log.Error("failed to revoke refresh session", sl.Err(err))
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}
