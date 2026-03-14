package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sso/internal/app"
	"sso/internal/config"
	"syscall"
)

func main() {
	cfg := config.MustLoad()

	application, err := app.New(context.Background(), cfg)
	if err != nil {
		slog.Error("failed to initialize application", slog.String("error", err.Error()))
		os.Exit(1)
	}

	slog.Info("starting application")

	if err := application.Run(); err != nil {
		slog.Error("failed to run application", slog.String("error", err.Error()))
		os.Exit(1)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sign := <-stop:
		slog.Info("stopping application", slog.String("signal", sign.String()))
	case err := <-application.Errors():
		if err != nil {
			slog.Error("application stopped with error", slog.String("error", err.Error()))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown application gracefully", slog.String("error", err.Error()))
		os.Exit(1)
	}

	slog.Info("application stopped")
}
