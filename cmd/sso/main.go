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

	// Контекст отменяется по SIGTERM/SIGINT уже на старте: app.New прерывает ретраи подключения к БД.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	application, err := app.New(ctx, cfg)
	if err != nil {
		slog.Error("failed to initialize application", slog.String("error", err.Error()))
		os.Exit(1)
	}

	slog.Info("starting application")

	if err := application.Run(); err != nil {
		slog.Error("failed to run application", slog.String("error", err.Error()))
		os.Exit(1)
	}

	select {
	case <-ctx.Done():
		slog.Info("stopping application: shutdown signal received")
	case err := <-application.Errors():
		if err != nil {
			slog.Error("application stopped with error", slog.String("error", err.Error()))
		}
	}

	// Возвращаем дефолтную обработку сигналов: повторный Ctrl+C убьёт процесс, не дожидаясь graceful shutdown.
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown application gracefully", slog.String("error", err.Error()))
		os.Exit(1)
	}

	slog.Info("application stopped")
}
