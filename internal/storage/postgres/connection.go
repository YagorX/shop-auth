package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type ConnConfig struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
	SSLMode  string

	AppName          string        // видно в pg_stat_activity
	StatementTimeout time.Duration // серверный лимит на любой запрос

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	ConnectTimeout time.Duration // на одну попытку
	ConnectRetries int
	RetryBackoff   time.Duration // первая пауза, дальше удваивается
	MaxBackoff     time.Duration
}

func DSN(cfg ConnConfig) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:   "/" + cfg.DBName,
	}
	q := u.Query()
	q.Set("sslmode", cfg.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

func Connect(ctx context.Context, cfg *ConnConfig, log *slog.Logger) (*sql.DB, error) {
	const op = "storage.postgres.Connect"

	// разбор DSN
	pgxCfg, err := pgx.ParseConfig(DSN(*cfg))
	if err != nil {
		return nil, fmt.Errorf("%s: parse config: %w", op, err)
	}

	pgxCfg.ConnectTimeout = cfg.ConnectTimeout
	pgxCfg.RuntimeParams["application_name"] = cfg.AppName
	if cfg.StatementTimeout > 0 {
		pgxCfg.RuntimeParams["statement_timeout"] = strconv.Itoa(int(cfg.StatementTimeout.Milliseconds()))
	}

	db := stdlib.OpenDB(*pgxCfg)

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if err := pingWithRetry(ctx, db, *cfg, log); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%s: ping: %w", op, err)
	}

	if err := prometheus.Register(collectors.NewDBStatsCollector(db, cfg.DBName)); err != nil {
		var are prometheus.AlreadyRegisteredError
		if !errors.As(err, &are) {
			_ = db.Close()
			return nil, fmt.Errorf("%s: register db metrics: %w", op, err)
		}
	}

	log.Info("postgres connected",
		slog.String("host", cfg.Host),
		slog.String("db", cfg.DBName),
		slog.Int("max_open_conns", cfg.MaxOpenConns),
	)
	return db, nil
}

func pingWithRetry(ctx context.Context, db *sql.DB, cfg ConnConfig, log *slog.Logger) error {
	backoff := cfg.RetryBackoff
	var lastErr error

	for attempt := 1; attempt <= cfg.ConnectRetries; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
		lastErr = db.PingContext(pingCtx)
		cancel()

		if lastErr == nil {
			return nil
		}
		if attempt == cfg.ConnectRetries {
			break
		}

		log.Warn("postgres not ready, retrying",
			slog.Int("attempt", attempt),
			slog.Duration("backoff", backoff),
			slog.String("error", lastErr.Error()),
		)

		// select, а не time.Sleep: SIGTERM во время ожидания должен прерывать старт.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, cfg.MaxBackoff)
	}

	return fmt.Errorf("ping failed after %d attempts: %w", cfg.ConnectRetries, lastErr)
}

type HealthChecker struct {
	db      *sql.DB
	timeout time.Duration
}

func NewHealthChecker(db *sql.DB, timeout time.Duration) *HealthChecker {
	return &HealthChecker{
		db:      db,
		timeout: timeout,
	}
}

func (h *HealthChecker) Check(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	if err := h.db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("postgres ping failed: %w", err)
	}

	return nil
}
