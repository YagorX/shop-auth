package contracts

import (
	"context"
	"log/slog"
)

type LogLevelController interface {
	SetLevel(level string) error
	Level() slog.Level
}

type ReadinessChecker interface {
	Check(ctx context.Context) error
}
