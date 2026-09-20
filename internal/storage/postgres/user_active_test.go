package postgres

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"sso/internal/storage"
)

// Тест ходит в настоящий Postgres: проверяется SQL, а не код вокруг него.
// Адрес берётся из TEST_POSTGRES_DSN, например:
//
//	TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5454/authdb?sslmode=disable' go test ./internal/storage/postgres/
//
// Без переменной тест пропускается, чтобы go test ./... оставался запускаемым без стенда.
func testStorage(t *testing.T) *Storage {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN не задан: пропускаем интеграционный тест")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	s, err := New(db, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}

	return s
}

type testUser struct {
	uuid     uuid.UUID
	username string
	email    string
}

func createUser(t *testing.T, s *Storage, role string, isActive bool) testUser {
	t.Helper()

	// Суффикс уникален на каждый вызов: username и email в схеме UNIQUE,
	// а тесты идут параллельно и на общей базе.
	suffix := uuid.NewString()
	u := testUser{
		username: "test-user-" + suffix,
		email:    "test-user-" + suffix + "@example.com",
	}

	q := `
		INSERT INTO users (username, email, password_hash, role_id, is_active)
		VALUES ($1, $2, 'hash', (SELECT id FROM roles WHERE name = $3), $4)
		RETURNING uuid
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.db.QueryRowContext(ctx, q, u.username, u.email, role, isActive).Scan(&u.uuid); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()

		if _, err := s.db.ExecContext(cleanupCtx, `DELETE FROM users WHERE uuid = $1`, u.uuid); err != nil {
			t.Errorf("cleanup user %s: %v", u.uuid, err)
		}
	})

	return u
}

// Регрессия на приоритет операторов: условие в User() записано как
// WHERE (email = $1 OR username = $1) AND is_active.
// Без скобок SQL читался бы как email = $1 OR (username = $1 AND is_active),
// и деактивированный пользователь продолжал бы входить по email.
// Поэтому поиск проверяется по ОБОИМ полям — иначе тест бесполезен.
func TestUserSkipsDeactivatedByEitherField(t *testing.T) {
	t.Parallel()

	s := testStorage(t)
	deactivated := createUser(t, s, "user", false)

	lookups := map[string]string{
		"by email":    deactivated.email,
		"by username": deactivated.username,
	}

	for name, login := range lookups {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := s.User(ctx, login)
			if !errors.Is(err, storage.ErrUserNotFound) {
				t.Fatalf("err = %v, want %v: деактивированный пользователь найден", err, storage.ErrUserNotFound)
			}
		})
	}
}

func TestUserFindsActiveByEitherField(t *testing.T) {
	t.Parallel()

	s := testStorage(t)
	active := createUser(t, s, "user", true)

	lookups := map[string]string{
		"by email":    active.email,
		"by username": active.username,
	}

	for name, login := range lookups {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			got, err := s.User(ctx, login)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.UUID != active.uuid {
				t.Fatalf("uuid = %v, want %v", got.UUID, active.uuid)
			}
			if !got.IsActive {
				t.Fatal("IsActive = false у активного пользователя")
			}
		})
	}
}

func TestUserByUUIDSkipsDeactivated(t *testing.T) {
	t.Parallel()

	s := testStorage(t)

	active := createUser(t, s, "user", true)
	deactivated := createUser(t, s, "user", false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := s.UserByUUID(ctx, deactivated.uuid); !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("err = %v, want %v", err, storage.ErrUserNotFound)
	}

	if _, err := s.UserByUUID(ctx, active.uuid); err != nil {
		t.Fatalf("активный пользователь не найден: %v", err)
	}
}

// IsAdminByUUID ходит своим запросом с JOIN на roles, мимо UserByUUID,
// поэтому условие is_active в нём легко забыть: деактивированный админ остался бы админом.
func TestIsAdminByUUIDSkipsDeactivated(t *testing.T) {
	t.Parallel()

	s := testStorage(t)

	activeAdmin := createUser(t, s, "admin", true)
	deactivatedAdmin := createUser(t, s, "admin", false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := s.IsAdminByUUID(ctx, deactivatedAdmin.uuid); !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("err = %v, want %v: деактивированный админ прошёл проверку прав", err, storage.ErrUserNotFound)
	}

	isAdmin, err := s.IsAdminByUUID(ctx, activeAdmin.uuid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isAdmin {
		t.Fatal("активный админ не распознан как админ")
	}
}
