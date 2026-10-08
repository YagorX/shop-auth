package grpcapp

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sso/internal/observability"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestRecoveryInterceptor(t *testing.T) {
	t.Parallel()

	okResponse := "ok"

	tests := []struct {
		name     string
		handler  grpc.UnaryHandler
		wantResp any
		wantCode codes.Code
	}{
		{
			name:     "panic becomes Internal",
			handler:  func(context.Context, any) (any, error) { panic("boom") },
			wantResp: nil,
			wantCode: codes.Internal,
		},
		{
			name: "runtime panic becomes Internal",
			handler: func(context.Context, any) (any, error) {
				var m map[string]string
				m["key"] = "value" // запись в nil-map

				return nil, nil
			},
			wantResp: nil,
			wantCode: codes.Internal,
		},
		{
			name:     "successful call passes through",
			handler:  func(context.Context, any) (any, error) { return okResponse, nil },
			wantResp: okResponse,
			wantCode: codes.OK,
		},
		{
			name:     "handler error is not swallowed",
			handler:  func(context.Context, any) (any, error) { return nil, status.Error(codes.NotFound, "no user") },
			wantResp: nil,
			wantCode: codes.NotFound,
		},
	}

	interceptor := recoveryInterceptor(discardLogger())
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/Login"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := interceptor(context.Background(), nil, info, tt.handler)

			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}
			if resp != tt.wantResp {
				t.Fatalf("resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestRecoveryInterceptorHidesPanicText(t *testing.T) {
	t.Parallel()

	interceptor := recoveryInterceptor(discardLogger())
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/Login"}

	_, err := interceptor(context.Background(), nil, info, func(context.Context, any) (any, error) {
		panic("password=hunter2")
	})

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err is not a status error: %v", err)
	}
	if st.Message() != "internal error" {
		t.Fatalf("message = %q, want %q: детали паники не должны уходить клиенту", st.Message(), "internal error")
	}
}

func TestDeadlineInterceptorSetsDeadlineWhenMissing(t *testing.T) {
	t.Parallel()

	const timeout = 5 * time.Second

	var (
		gotDeadline time.Time
		hasDeadline bool
	)

	interceptor := deadlineInterceptor(timeout)
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/ValidateToken"}

	before := time.Now()

	_, err := interceptor(context.Background(), nil, info, func(ctx context.Context, _ any) (any, error) {
		gotDeadline, hasDeadline = ctx.Deadline()

		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasDeadline {
		t.Fatal("обработчик получил контекст без дедлайна")
	}

	// Дедлайн отсчитывается от момента внутри интерцептора, то есть чуть позже before.
	const slack = 100 * time.Millisecond

	if left := gotDeadline.Sub(before); left <= 0 || left > timeout+slack {
		t.Fatalf("до дедлайна %v, ожидалось около %v", left, timeout)
	}
}

func TestDeadlineInterceptorKeepsClientDeadline(t *testing.T) {
	t.Parallel()

	clientCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	t.Cleanup(cancel)

	clientDeadline, ok := clientCtx.Deadline()
	if !ok {
		t.Fatal("контекст теста должен иметь дедлайн")
	}

	var gotDeadline time.Time

	// Серверный таймаут намеренно велик: если интерцептор перезапишет дедлайн клиента, это будет видно.
	interceptor := deadlineInterceptor(time.Hour)
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/Login"}

	_, err := interceptor(clientCtx, nil, info, func(ctx context.Context, _ any) (any, error) {
		gotDeadline, _ = ctx.Deadline()

		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !gotDeadline.Equal(clientDeadline) {
		t.Fatalf("дедлайн = %v, want %v: дедлайн клиента перезаписан", gotDeadline, clientDeadline)
	}
}

func TestDeadlineInterceptorCancelsHandlerContext(t *testing.T) {
	t.Parallel()

	interceptor := deadlineInterceptor(20 * time.Millisecond)
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/Login"}

	_, err := interceptor(context.Background(), nil, info, func(ctx context.Context, _ any) (any, error) {
		<-ctx.Done()

		return nil, ctx.Err()
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want %v", err, context.DeadlineExceeded)
	}
}

// Клиент (k6, gateway) присылает 10 с; сервер не должен ждать дольше своего таймаута.
func TestDeadlineInterceptorCapsLongClientDeadline(t *testing.T) {
	t.Parallel()

	clientCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
	t.Cleanup(cancel)

	const timeout = 20 * time.Millisecond

	var (
		gotDeadline time.Time
		hasDeadline bool
	)

	interceptor := deadlineInterceptor(timeout)
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/Login"}

	before := time.Now()

	_, err := interceptor(clientCtx, nil, info, func(ctx context.Context, _ any) (any, error) {
		gotDeadline, hasDeadline = ctx.Deadline()

		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasDeadline {
		t.Fatal("обработчик получил контекст без дедлайна")
	}

	const slack = 100 * time.Millisecond

	if left := gotDeadline.Sub(before); left > timeout+slack {
		t.Fatalf("до дедлайна %v, ожидалось не больше %v: дедлайн клиента не урезан", left, timeout)
	}
}

func TestContextErrorInterceptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		ctxTimeout time.Duration // 0 — контекст без дедлайна, живой
		handlerErr error
		want       codes.Code
	}{
		{
			name:       "истёкший дедлайн вместо Internal",
			ctxTimeout: 10 * time.Millisecond,
			handlerErr: status.Error(codes.Internal, "internal error"),
			want:       codes.DeadlineExceeded,
		},
		{
			name:       "истёкший дедлайн вместо голой ошибки",
			ctxTimeout: 10 * time.Millisecond,
			handlerErr: errors.New("pgx: timeout"),
			want:       codes.DeadlineExceeded,
		},
		{
			name:       "осмысленный код при истёкшем дедлайне не трогаем",
			ctxTimeout: 10 * time.Millisecond,
			handlerErr: status.Error(codes.InvalidArgument, "invalid email or password"),
			want:       codes.InvalidArgument,
		},
		{
			// Настоящий отказ базы при живом контексте не должен маскироваться под таймаут.
			name:       "Internal при живом контексте остаётся Internal",
			handlerErr: status.Error(codes.Internal, "internal error"),
			want:       codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tt.ctxTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.ctxTimeout)
				t.Cleanup(cancel)
			}

			interceptor := contextErrorInterceptor()
			info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/Login"}

			_, err := interceptor(ctx, nil, info, func(ctx context.Context, _ any) (any, error) {
				if tt.ctxTimeout > 0 {
					<-ctx.Done()
				}

				return nil, tt.handlerErr
			})

			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %v, want %v (err = %v)", got, tt.want, err)
			}
		})
	}
}

func TestContextErrorInterceptorKeepsSuccess(t *testing.T) {
	t.Parallel()

	// Ответ успел, а дедлайн истёк сразу после: успех остаётся успехом.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	interceptor := contextErrorInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.auth.v1.AuthService/ValidateToken"}

	resp, err := interceptor(ctx, nil, info, func(context.Context, any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("resp = %v, want ok", resp)
	}
}

func TestStreamRecoveryInterceptor(t *testing.T) {
	t.Parallel()

	interceptor := streamRecoveryInterceptor(discardLogger())
	info := &grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"}

	t.Run("panic becomes Internal", func(t *testing.T) {
		t.Parallel()

		err := interceptor(nil, &fakeServerStream{ctx: context.Background()}, info, func(any, grpc.ServerStream) error {
			panic("boom")
		})

		if got := status.Code(err); got != codes.Internal {
			t.Fatalf("code = %v, want %v", got, codes.Internal)
		}
	})

	t.Run("handler error passes through", func(t *testing.T) {
		t.Parallel()

		want := status.Error(codes.Unavailable, "not serving")

		err := interceptor(nil, &fakeServerStream{ctx: context.Background()}, info, func(any, grpc.ServerStream) error {
			return want
		})

		if got := status.Code(err); got != codes.Unavailable {
			t.Fatalf("code = %v, want %v", got, codes.Unavailable)
		}
	})
}

type fakeServerStream struct {
	ctx context.Context
}

func (s *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)       {}
func (s *fakeServerStream) Context() context.Context     { return s.ctx }
func (s *fakeServerStream) SendMsg(any) error            { return nil }
func (s *fakeServerStream) RecvMsg(any) error            { return nil }

// Закрепляет порядок цепочки: recovery стоит вплотную к обработчику, поэтому паника успевает
// стать ошибкой Internal и попасть в метрики. Если поменять порядок местами — тест упадёт.
func TestPanicIsCountedInMetrics(t *testing.T) {
	// Без t.Parallel: счётчик метрик глобальный, параллельные тесты изменили бы его между замерами.
	const method = "/proto.auth.v1.AuthService/Login"

	counter := observability.MustMetrics().AuthGRPCRequestsTotal.WithLabelValues(method, codes.Internal.String())
	before := testutil.ToFloat64(counter)

	var (
		metrics  = metricsInterceptor()
		recovery = recoveryInterceptor(discardLogger())
		info     = &grpc.UnaryServerInfo{FullMethod: method}
	)

	_, err := metrics(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		return recovery(ctx, req, info, func(context.Context, any) (any, error) {
			panic("boom")
		})
	})

	if got := status.Code(err); got != codes.Internal {
		t.Fatalf("code = %v, want %v", got, codes.Internal)
	}

	if got := testutil.ToFloat64(counter) - before; got != 1 {
		t.Fatalf("прирост счётчика = %v, want 1: паника не попала в метрики", got)
	}
}
