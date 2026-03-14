package grpcapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"sso/internal/observability"
	authgrpc "sso/internal/transport/grpc/v1"

	"google.golang.org/grpc"
	health "google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

type App struct {
	log          *slog.Logger
	gRPCServer   *grpc.Server
	healthServer *health.Server
	port         int
}

func New(
	log *slog.Logger,
	authService authgrpc.Auth,
	port int,
	opts ...grpc.ServerOption,
) *App {
	metricsInterceptor := grpc.UnaryInterceptor(func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		started := time.Now()

		resp, err := handler(ctx, req)

		observability.ObserveGRPCRequest(info.FullMethod, status.Code(err).String(), started)

		return resp, err
	})

	serverOpts := append([]grpc.ServerOption{metricsInterceptor}, opts...)
	gRPCServer := grpc.NewServer(serverOpts...)

	authgrpc.Register(gRPCServer, authService)

	healthServer := health.NewServer()
	healthServer.SetServingStatus(authgrpc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(gRPCServer, healthServer)

	reflection.Register(gRPCServer)

	return &App{
		log:          log,
		gRPCServer:   gRPCServer,
		healthServer: healthServer,
		port:         port,
	}
}

func (a *App) MustRun() {
	if err := a.Run(); err != nil {
		panic(err)
	}
}

func (a *App) Run() error {
	const op = "grpcapp.Run"

	log := a.log.With(
		slog.String("op", op),
		slog.Int("port", a.port),
	)

	l, err := net.Listen("tcp", a.Addr())
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	log.Info("grpc server started", slog.String("addr", l.Addr().String()))

	if err := a.gRPCServer.Serve(l); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (a *App) Stop() {
	const op = "grpcapp.Stop"

	log := a.log.With(slog.String("op", op))
	log.Info("stopping gRPC server", slog.Int("port", a.port))

	if a.healthServer != nil {
		a.healthServer.SetServingStatus(authgrpc.ServiceName, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		a.healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}

	a.gRPCServer.GracefulStop()
}

func (a *App) Addr() string {
	return fmt.Sprintf(":%d", a.port)
}
