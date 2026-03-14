package observability

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type LoggerOptions struct {
	Service string
	Env     string
	Version string
	Level   string
}

type RuntimeLogger struct {
	Logger *slog.Logger
	level  *slog.LevelVar
}

func NewLogger(opts LoggerOptions) *RuntimeLogger {
	levelVar := &slog.LevelVar{}
	levelVar.Set(parseLevel(opts.Level))

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: levelVar,
	})

	l := slog.New(handler).With(
		slog.String("service", opts.Service),
		slog.String("env", opts.Env),
		slog.String("version", opts.Version),
	)

	return &RuntimeLogger{
		Logger: l,
		level:  levelVar,
	}
}

func (r *RuntimeLogger) SetLevel(level string) error {
	if r == nil || r.level == nil {
		return fmt.Errorf("logger is not initialized")
	}

	r.level.Set(parseLevel(level))
	return nil
}

func (r *RuntimeLogger) Level() slog.Level {
	if r == nil || r.level == nil {
		return slog.LevelInfo
	}
	return r.level.Level()
}

func SetDefaultLogger(l *slog.Logger) {
	if l != nil {
		slog.SetDefault(l)
	}
}

func parseLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
