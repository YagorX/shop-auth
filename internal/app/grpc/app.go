package grpcapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	"sso/internal/observability"
	authgrpc "sso/internal/transport/grpc/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	health "google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
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
	timeout time.Duration,
	opts ...grpc.ServerOption,
) *App {
	baseOpts := []grpc.ServerOption{
		// Именно Chain: grpc.UnaryInterceptor можно задать лишь однажды, повторный вызов паникует при старте.
		// Порядок: первый — самый внешний. recovery идёт последним, вплотную к обработчику,
		// чтобы паника стала обычной ошибкой ДО того, как её увидят метрики: иначе она пролетает
		// сквозь metricsInterceptor и в auth_grpc_requests_total не попадает вообще.
		grpc.ChainUnaryInterceptor(
			deadlineInterceptor(timeout),
			metricsInterceptor(),
			recoveryInterceptor(log),
		),
		// Стримы отдельной цепочкой: unary-интерцепторы их не покрывают,
		// а стримовые методы у нас есть — reflection и health.Watch.
		grpc.ChainStreamInterceptor(
			streamRecoveryInterceptor(log),
		),
		grpc.MaxConcurrentStreams(1000),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              30 * time.Second,
			Timeout:           10 * time.Second,
		}),
		// MinTime должен быть меньше клиентского keepalive Time, иначе сервер рвёт своих же клиентов.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: false,
		}),
	}

	gRPCServer := grpc.NewServer(append(baseOpts, opts...)...)

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

func metricsInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()

		resp, err := handler(ctx, req)

		observability.ObserveGRPCRequest(info.FullMethod, status.Code(err).String(), started)

		return resp, err
	}
}

// grpc-go не перехватывает паники обработчиков: без этого интерцептора любая паника
// в любом методе роняет весь процесс вместе с исправными запросами других клиентов.
func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	// resp и err именованные: подменить возвращаемое значение из defer можно только так.
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = recoveredError(log, info.FullMethod, r)
			}
		}()

		return handler(ctx, req)
	}
}

func streamRecoveryInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = recoveredError(log, info.FullMethod, r)
			}
		}()

		return handler(srv, ss)
	}
}

// Текст паники наружу не отдаётся: он может содержать данные запроса. Клиент получает Internal,
// подробности со стеком остаются в логе.
func recoveredError(log *slog.Logger, method string, recovered any) error {
	log.Error("panic in grpc handler",
		slog.String("method", method),
		slog.Any("panic", recovered),
		slog.String("stack", string(debug.Stack())),
	)

	return status.Error(codes.Internal, "internal error")
}

func deadlineInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); ok {
			return handler(ctx, req)
		}

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		return handler(ctx, req)
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
