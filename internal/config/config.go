package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	ServiceName     string         `yaml:"service_name" env-default:"auth-service"`
	Env             string         `yaml:"env" env-default:"local"`
	Version         string         `yaml:"version" env-default:"dev"`
	LogLevel        string         `yaml:"log_level" env-default:"info"`
	ShutdownTimeout time.Duration  `yaml:"shutdown_timeout" env-default:"10s"`
	TokenTTL        time.Duration  `yaml:"token_ttl" env-default:"1h"`
	RefreshTTL      time.Duration  `yaml:"refresh_ttl" env-default:"168h"`
	GRPC            GRPCConfig     `yaml:"grpc"`
	HTTP            HTTPConfig     `yaml:"http"`
	OTLP            OTLPConfig     `yaml:"otlp"`
	Postgres        PostgresConfig `yaml:"postgres"`
	TLS             TLSConfig      `yaml:"tls"`
}

type GRPCConfig struct {
	Port    int           `yaml:"port" env-default:"44044"`
	Timeout time.Duration `yaml:"timeout" env-default:"5s"`
}

type TLSConfig struct {
	Enabled           bool   `yaml:"enabled" env-default:"false"`
	CertFile          string `yaml:"cert_file" env-default:""`
	KeyFile           string `yaml:"key_file" env-default:""`
	CAFile            string `yaml:"ca_file" env-default:""`
	ClientCAFile      string `yaml:"client_ca_file" env-default:""`
	RequireClientCert bool   `yaml:"require_client_cert" env-default:"false"`
}

type HTTPConfig struct {
	Port    int           `yaml:"port" env-default:"8082"`
	Timeout time.Duration `yaml:"timeout" env-default:"5s"`
}

type OTLPConfig struct {
	Endpoint string `yaml:"endpoint" env-default:"localhost:4317"`
}

type PostgresConfig struct {
	Host     string `yaml:"host" env-default:"localhost"`
	Port     int    `yaml:"port" env-default:"5432"`
	DBName   string `yaml:"db_name" env-default:"authdb"`
	User     string `yaml:"user" env-default:"postgres"`
	Password string `yaml:"password" env-default:"postgres"`
	SSLMode  string `yaml:"ssl_mode" env-default:"disable"`
}

func MustLoad() *Config {
	path := fetchConfigPath()
	if path == "" {
		panic("config path is empty")
	}

	return mustLoadByPath(path)
}

func MustLoadByPath(path string) *Config {
	return mustLoadByPath(path)
}

func (c *Config) PostgresDSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.Postgres.User,
		c.Postgres.Password,
		c.Postgres.Host,
		c.Postgres.Port,
		c.Postgres.DBName,
		c.Postgres.SSLMode,
	)
}

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}

	if strings.TrimSpace(c.ServiceName) == "" {
		return fmt.Errorf("service_name is required")
	}
	if strings.TrimSpace(c.Env) == "" {
		return fmt.Errorf("env is required")
	}
	if strings.TrimSpace(c.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if err := validateLogLevel(c.LogLevel); err != nil {
		return err
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown_timeout must be > 0")
	}
	if c.TokenTTL <= 0 {
		return fmt.Errorf("token_ttl must be > 0")
	}
	if c.RefreshTTL <= 0 {
		return fmt.Errorf("refresh_ttl must be > 0")
	}
	if err := validatePort("grpc.port", c.GRPC.Port); err != nil {
		return err
	}
	if c.GRPC.Timeout <= 0 {
		return fmt.Errorf("grpc.timeout must be > 0")
	}
	if err := validatePort("http.port", c.HTTP.Port); err != nil {
		return err
	}
	if c.HTTP.Timeout <= 0 {
		return fmt.Errorf("http.timeout must be > 0")
	}
	if strings.TrimSpace(c.OTLP.Endpoint) == "" {
		return fmt.Errorf("otlp.endpoint is required")
	}
	if strings.TrimSpace(c.Postgres.Host) == "" {
		return fmt.Errorf("postgres.host is required")
	}
	if err := validatePort("postgres.port", c.Postgres.Port); err != nil {
		return err
	}
	if strings.TrimSpace(c.Postgres.DBName) == "" {
		return fmt.Errorf("postgres.db_name is required")
	}
	if strings.TrimSpace(c.Postgres.User) == "" {
		return fmt.Errorf("postgres.user is required")
	}
	if strings.TrimSpace(c.Postgres.Password) == "" {
		return fmt.Errorf("postgres.password is required")
	}
	if err := validateSSLMode(c.Postgres.SSLMode); err != nil {
		return err
	}
	if c.TLS.Enabled {
		if c.TLS.CertFile == "" {
			return fmt.Errorf("tls.cert_file is required when tls.enabled=true")
		}
		if c.TLS.KeyFile == "" {
			return fmt.Errorf("tls.key_file is required when tls.enabled=true")
		}
		if c.TLS.RequireClientCert && c.TLS.ClientCAFile == "" {
			return fmt.Errorf("tls.client_ca_file is required when tls.require_client_cert=true")
		}
	}

	return nil
}

func mustLoadByPath(path string) *Config {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		panic("config file does not exist: " + path)
	}

	var cfg Config
	if err := cleanenv.ReadConfig(path, &cfg); err != nil {
		panic("failed to read config: " + err.Error())
	}
	if err := cfg.Validate(); err != nil {
		panic("invalid config: " + err.Error())
	}

	return &cfg
}

func fetchConfigPath() string {
	var path string

	flag.StringVar(&path, "config", "", "path to config file")
	flag.Parse()

	if path == "" {
		path = os.Getenv("CONFIG_PATH")
	}

	return path
}

func validatePort(field string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", field)
	}

	return nil
}

func validateLogLevel(level string) error {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "warning", "error":
		return nil
	default:
		return fmt.Errorf("log_level must be one of: debug, info, warn, error")
	}
}

func validateSSLMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return nil
	default:
		return fmt.Errorf("postgres.ssl_mode has unsupported value %q", mode)
	}
}
