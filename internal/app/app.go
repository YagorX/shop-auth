package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	grpcapp "sso/internal/app/grpc"
	httpapp "sso/internal/app/http"
	"sso/internal/config"
	"sso/internal/observability"
	authsvc "sso/internal/services/auth"
	"sso/internal/storage/postgres"
	httpv1 "sso/internal/transport/http/v1"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

type App struct {
	logger *slog.Logger

	GRPCServer *grpcapp.App
	HTTPServer *httpapp.App

	db *sql.DB

	shutdownTracing func(context.Context) error
	errCh           chan error
}

type readinessChecker struct {
	db *sql.DB
}

func (c *readinessChecker) Check(ctx context.Context) error {
	if c == nil || c.db == nil {
		return errors.New("postgres is not initialized")
	}

	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres not ready: %w", err)
	}

	return nil
}

func New(ctx context.Context, cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	runtimeLogger := observability.NewLogger(observability.LoggerOptions{
		Service: cfg.ServiceName,
		Env:     cfg.Env,
		Version: cfg.Version,
		Level:   cfg.LogLevel,
	})
	observability.SetDefaultLogger(runtimeLogger.Logger)
	observability.MustMetrics()

	shutdownTracing, err := observability.InitTracing(
		ctx,
		cfg.ServiceName,
		cfg.Version,
		cfg.Env,
		cfg.OTLP.Endpoint,
	)
	if err != nil {
		runtimeLogger.Logger.Warn("tracing is disabled", slog.String("error", err.Error()))
		shutdownTracing = nil
	}

	db, err := sql.Open("pgx", cfg.PostgresDSN())
	if err != nil {
		_ = shutdownTracing(context.Background())
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		_ = shutdownTracing(context.Background())
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	storage, err := postgres.New(db, runtimeLogger.Logger)
	if err != nil {
		_ = db.Close()
		_ = shutdownTracing(context.Background())
		return nil, fmt.Errorf("create postgres storage: %w", err)
	}

	jwtAdapter := authsvc.NewJWTAdapter(storage)

	authService := authsvc.New(
		runtimeLogger.Logger,
		storage,
		storage,
		storage,
		jwtAdapter,
		cfg.TokenTTL,
		cfg.RefreshTTL,
		storage,
		storage,
		storage,
		storage,
	)

	grpcRuntime := grpcapp.New(
		runtimeLogger.Logger,
		authService,
		cfg.GRPC.Port,
		grpc.StatsHandler(observability.GRPCServerStatsHandler()),
	)

	httpRouter := httpv1.NewRouter(httpv1.RouterDeps{
		LogLevelController: runtimeLogger,
		ReadinessChecker:   &readinessChecker{db: db},
	})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:           otelhttp.NewHandler(httpRouter, "auth.http"),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.HTTP.Timeout,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	httpRuntime, err := httpapp.New(runtimeLogger.Logger, httpServer)
	if err != nil {
		_ = db.Close()
		_ = shutdownTracing(context.Background())
		return nil, fmt.Errorf("create http app: %w", err)
	}

	return &App{
		logger:          runtimeLogger.Logger,
		GRPCServer:      grpcRuntime,
		HTTPServer:      httpRuntime,
		db:              db,
		shutdownTracing: shutdownTracing,
		errCh:           make(chan error, 2),
	}, nil
}

func (a *App) Run() error {
	if a == nil {
		return errors.New("app is nil")
	}

	go func() {
		if err := a.GRPCServer.Run(); err != nil {
			a.errCh <- fmt.Errorf("grpc app failed: %w", err)
		}
	}()

	go func() {
		if err := a.HTTPServer.Run(); err != nil {
			a.errCh <- fmt.Errorf("http app failed: %w", err)
		}
	}()

	a.logger.Info(
		"auth service bootstrap completed",
		slog.String("grpc_addr", a.GRPCServer.Addr()),
		slog.String("http_addr", a.HTTPServer.Addr()),
	)

	return nil
}

func (a *App) Errors() <-chan error {
	if a == nil {
		return nil
	}

	return a.errCh
}

func (a *App) Shutdown(ctx context.Context) error {
	if a == nil {
		return nil
	}

	var shutdownErr error

	if a.GRPCServer != nil {
		a.GRPCServer.Stop()
	}

	if a.HTTPServer != nil {
		if err := a.HTTPServer.Stop(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("stop http app: %w", err))
		}
	}

	if a.db != nil {
		if err := a.db.Close(); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close postgres db: %w", err))
		}
	}

	if a.shutdownTracing != nil {
		if err := a.shutdownTracing(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown tracing: %w", err))
		}
	}

	a.logger.Info("auth service stopped")

	return shutdownErr
}
