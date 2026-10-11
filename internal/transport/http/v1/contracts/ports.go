package contracts

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

type LogLevelController interface {
	SetLevel(level string) error
	Level() slog.Level
	GetSlog() *slog.Logger
}

type ReadinessChecker interface {
	Check(ctx context.Context) error
}

type AdminAuthorizer interface {
	ValidateToken(ctx context.Context, token string, appID int64) (uuid.UUID, error)
	IsAdminByUUID(ctx context.Context, userUUID uuid.UUID) (bool, error)
}
