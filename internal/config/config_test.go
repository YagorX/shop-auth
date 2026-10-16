package config

import (
	"testing"
	"time"
)

// Конфиги из репозитория обязаны проходить Validate: иначе сервис падает на старте уже в compose.
func TestShippedConfigsAreValid(t *testing.T) {
	for _, path := range []string{"../../config/local.yaml", "../../config/config.docker.yaml", "../../config/local_tests.yaml"} {
		t.Run(path, func(t *testing.T) {
			cfg := MustLoadByPath(path)

			if cfg.LoginLimiter.MaxFailures != 5 || cfg.LoginLimiter.Window != 15*time.Minute || cfg.LoginLimiter.Lockout != 5*time.Minute {
				t.Fatalf("login_limiter = %+v, want 5 failures / 15m window / 5m lockout", cfg.LoginLimiter)
			}
		})
	}
}

// Нулевой порог или окно — не «выключено», а лимитер, блокирующий с первой неудачи или не блокирующий никогда.
func TestValidateRejectsNonPositiveLoginLimiter(t *testing.T) {
	tests := map[string]func(*LoginLimiterConfig){
		"max_failures": func(c *LoginLimiterConfig) { c.MaxFailures = 0 },
		"window":       func(c *LoginLimiterConfig) { c.Window = 0 },
		"lockout":      func(c *LoginLimiterConfig) { c.Lockout = -time.Second },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := MustLoadByPath("../../config/local.yaml")
			mutate(&cfg.LoginLimiter)

			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted non-positive login_limiter.%s", name)
			}
		})
	}
}
