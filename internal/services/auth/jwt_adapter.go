package auth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"sso/internal/domain/models"
	"sso/internal/lib/jwt"
	"sso/internal/storage/postgres"
)

type JWTAdapter struct {
	storage  *postgres.Storage
	appCache map[int64]models.App
	cacheMux sync.RWMutex
}

func NewJWTAdapter(storage *postgres.Storage) *JWTAdapter {
	return &JWTAdapter{
		storage:  storage,
		appCache: make(map[int64]models.App),
	}
}

func (j *JWTAdapter) CreateToken(userUUID uuid.UUID, appID int64, ttl time.Duration) (string, error) {
	app, err := j.getApp(context.Background(), appID)
	if err != nil {
		return "", err
	}

	// jwt.NewToken должен класть sub = user.UUID
	user := models.User{UUID: userUUID}

	return jwt.NewToken(user, app, ttl)
}

func (j *JWTAdapter) ParseToken(tokenString string, appID int64) (uuid.UUID, error) {
	app, err := j.getApp(context.Background(), appID)
	if err != nil {
		return uuid.Nil, err
	}

	return jwt.ParseTokenUUID(tokenString, app) // ниже поясню
}

func (j *JWTAdapter) getApp(ctx context.Context, appID int64) (models.App, error) {
	j.cacheMux.RLock()
	if app, exists := j.appCache[appID]; exists {
		j.cacheMux.RUnlock()
		return app, nil
	}
	j.cacheMux.RUnlock()

	app, err := j.storage.App(ctx, appID)
	if err != nil {
		return models.App{}, err
	}

	j.cacheMux.Lock()
	j.appCache[appID] = app
	j.cacheMux.Unlock()

	return app, nil
}
