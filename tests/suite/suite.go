package suite

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	authv1 "github.com/YagorX/shop-contracts/gen/go/proto/auth/v1"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"sso/internal/config"
	"sso/internal/storage/postgres"
)

type Suite struct {
	T          *testing.T
	Cfg        *config.Config
	AuthClient authv1.AuthServiceClient
	DB         *sql.DB

	AppID     int64
	AppSecret string
	DeviceID  string
}

const (
	grpcHost          = "localhost"
	defaultConfigPath = "../config/local_tests.yaml"

	testAppName   = "test-app"
	testAppSecret = "test-secret"
)

func New(t *testing.T) (context.Context, *Suite) {
	t.Helper()
	t.Parallel()

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	cfg := config.MustLoadByPath(cfgPath)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.GRPC.Timeout)
	t.Cleanup(cancel)

	postgresConfig := postgres.ConnConfig{
		Host:     cfg.Postgres.Host,
		Port:     cfg.Postgres.Port,
		DBName:   cfg.Postgres.DBName,
		User:     cfg.Postgres.User,
		Password: cfg.Postgres.Password,
		SSLMode:  cfg.Postgres.SSLMode,

		AppName:          cfg.ServiceName,
		StatementTimeout: cfg.Postgres.StatementTimeout,

		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Postgres.ConnMaxIdleTime,

		ConnectTimeout: cfg.Postgres.ConnectTimeout,
		ConnectRetries: 1,
		RetryBackoff:   time.Second,
		MaxBackoff:     10 * time.Second,
	}

	// DB
	db, err := postgres.Connect(ctx, &postgresConfig, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	// Seed role "user" (нужно для SaveUser, если ты выставляешь role_id через SELECT)
	ensureRole(t, ctx, db, "user", "default user role")

	// Seed app "test-app" (upsert по name -> не конфликтует с apps_name_key)
	ensureAppByName(t, ctx, db, testAppName, testAppSecret)

	// Read app id + secret from DB
	appID, appSecret := mustGetAppByName(t, ctx, db, testAppName)

	// gRPC client
	cc, err := grpc.DialContext(
		ctx,
		grpcAddress(cfg),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc server connection failed: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	return ctx, &Suite{
		T:          t,
		Cfg:        cfg,
		AuthClient: authv1.NewAuthServiceClient(cc),
		DB:         db,

		AppID:     appID,
		AppSecret: appSecret,
		DeviceID:  "test-device-1",
	}
}

func grpcAddress(cfg *config.Config) string {
	return net.JoinHostPort(grpcHost, strconv.Itoa(cfg.GRPC.Port))
}

func ensureRole(t *testing.T, ctx context.Context, db *sql.DB, name, desc string) {
	t.Helper()

	_, err := db.ExecContext(ctx, `
		INSERT INTO roles (name, description)
		VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE
		SET description = EXCLUDED.description
	`, name, desc)
	if err != nil {
		t.Fatalf("seed role %q failed: %v", name, err)
	}
}

func ensureAppByName(t *testing.T, ctx context.Context, db *sql.DB, name, secret string) {
	t.Helper()

	_, err := db.ExecContext(ctx, `
		INSERT INTO apps (name, secret)
		VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE
		SET secret = EXCLUDED.secret
	`, name, secret)
	if err != nil {
		t.Fatalf("seed app %q failed: %v", name, err)
	}
}

func mustGetAppByName(t *testing.T, ctx context.Context, db *sql.DB, name string) (id int64, secret string) {
	t.Helper()

	err := db.QueryRowContext(ctx, `
		SELECT id, secret
		FROM apps
		WHERE name = $1
		LIMIT 1
	`, name).Scan(&id, &secret)
	if err != nil {
		t.Fatalf("fetch app %q failed: %v", name, err)
	}

	return id, secret
}
