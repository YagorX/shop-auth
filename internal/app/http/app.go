package httpapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

type App struct {
	log    *slog.Logger
	server *http.Server
}

func New(log *slog.Logger, server *http.Server) (*App, error) {
	if log == nil {
		return nil, fmt.Errorf("logger is nil")
	}
	if server == nil {
		return nil, fmt.Errorf("http server is nil")
	}
	if server.Addr == "" {
		return nil, fmt.Errorf("http server addr is empty")
	}

	return &App{
		log:    log,
		server: server,
	}, nil
}

func (a *App) Run() error {
	const op = "httpapp.Run"

	log := a.log.With(
		slog.String("op", op),
		slog.String("addr", a.server.Addr),
	)

	log.Info("http server started")

	if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (a *App) Stop(ctx context.Context) error {
	const op = "httpapp.Stop"

	a.log.With(
		slog.String("op", op),
		slog.String("addr", a.server.Addr),
	).Info("stopping http server")

	if err := a.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (a *App) Addr() string {
	if a == nil || a.server == nil {
		return ""
	}
	return a.server.Addr
}
