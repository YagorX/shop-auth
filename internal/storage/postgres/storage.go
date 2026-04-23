package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"log/slog"
	"sso/internal/domain/models"
	"sso/internal/storage"
)

type Storage struct {
	db  *sql.DB
	log *slog.Logger
}

func New(db *sql.DB, log *slog.Logger) (*Storage, error) {
	const op = "storage.postgres.New"

	if db == nil {
		return nil, fmt.Errorf("%s : %w", op, errors.New("*db is nil"))
	}
	if log == nil {
		return nil, fmt.Errorf("%s : %w", op, errors.New("log is nil"))
	}

	return &Storage{db: db, log: log}, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	// fallback: когда ошибка обёрнута так, что PgError не достаётся
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}

func (s *Storage) SaveUser(ctx context.Context, username, email string, passHash []byte) (uuid.UUID, error) {
	const op = "storage.postgres.SaveUser"

	query := `
		INSERT INTO users (username, email, password_hash, role_id)
		VALUES ($1, $2, $3, (SELECT id FROM roles WHERE name = 'user'))
		RETURNING uuid
	`

	var userUUID uuid.UUID
	err := s.db.QueryRowContext(ctx, query, username, email, string(passHash)).Scan(&userUUID)
	if err != nil {
		if isUniqueViolation(err) {
			return uuid.Nil, fmt.Errorf("%s: %w", op, storage.ErrUserExist)
		}
		return uuid.Nil, fmt.Errorf("%s: %w", op, err)
	}

	return userUUID, nil
}

func (s *Storage) User(ctx context.Context, email string) (models.User, error) {
	const op = "storage.postgres.User"

	q := `
		SELECT id, uuid, username, email, password_hash, role_id, is_active
		FROM users
		WHERE email = $1 or username = $1
		LIMIT 1
	`

	var u models.User
	var passHash string
	var roleID sql.NullInt64
	var emailNS sql.NullString

	err := s.db.QueryRowContext(ctx, q, email).Scan(
		&u.ID,
		&u.UUID,
		&u.Username,
		&emailNS,
		&passHash,
		&roleID,
		&u.IsActive,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.User{}, fmt.Errorf("%s: %w", op, storage.ErrUserNotFound)
		}
		return models.User{}, fmt.Errorf("%s: %w", op, err)
	}

	u.PasswordHash = []byte(passHash)
	if emailNS.Valid {
		u.Email = &emailNS.String
	}
	if roleID.Valid {
		u.RoleID = &roleID.Int64
	}

	return u, nil
}

func (s *Storage) UserByID(ctx context.Context, userID int64) (models.User, error) {
	const op = "storage.postgres.UserByID"

	q := `
		SELECT id, uuid, username, email, password_hash, role_id, is_active
		FROM users
		WHERE id = $1
		LIMIT 1
	`

	var u models.User
	var passHash string
	var roleID sql.NullInt64
	var emailNS sql.NullString

	err := s.db.QueryRowContext(ctx, q, userID).Scan(
		&u.ID,
		&u.UUID,
		&u.Username,
		&emailNS,
		&passHash,
		&roleID,
		&u.IsActive,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.User{}, fmt.Errorf("%s: %w", op, storage.ErrUserNotFound)
		}
		return models.User{}, fmt.Errorf("%s: %w", op, err)
	}

	u.PasswordHash = []byte(passHash)
	if emailNS.Valid {
		u.Email = &emailNS.String
	}
	if roleID.Valid {
		u.RoleID = &roleID.Int64
	}

	return u, nil
}

func (s *Storage) UserByUUID(ctx context.Context, userUUID uuid.UUID) (models.User, error) {
	const op = "storage.postgres.UserByUUID"

	q := `
		SELECT id, uuid, username, email, password_hash, role_id, is_active
		FROM users
		WHERE uuid = $1
		LIMIT 1
	`

	var u models.User
	var passHash string
	var roleID sql.NullInt64
	var emailNS sql.NullString

	err := s.db.QueryRowContext(ctx, q, userUUID).Scan(
		&u.ID,
		&u.UUID,
		&u.Username,
		&emailNS,
		&passHash,
		&roleID,
		&u.IsActive,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.User{}, fmt.Errorf("%s: %w", op, storage.ErrUserNotFound)
		}
		return models.User{}, fmt.Errorf("%s: %w", op, err)
	}

	u.PasswordHash = []byte(passHash)
	if emailNS.Valid {
		u.Email = &emailNS.String
	}
	if roleID.Valid {
		u.RoleID = &roleID.Int64
	}

	return u, nil
}

func (s *Storage) IsAdminByUUID(ctx context.Context, userUUID uuid.UUID) (bool, error) {
	const op = "storage.postgres.IsAdminByUUID"

	q := `
		SELECT COALESCE(r.name = 'admin', FALSE)
		FROM users u
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE u.uuid = $1
	`

	var isAdmin bool
	err := s.db.QueryRowContext(ctx, q, userUUID).Scan(&isAdmin)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, storage.ErrUserNotFound
		}
		return false, err
	}

	return isAdmin, nil
}

func (s *Storage) App(ctx context.Context, appID int64) (models.App, error) {
	const op = "storage.postgres.App"

	q := `
		SELECT id, name, secret
		FROM apps
		WHERE id = $1
		LIMIT 1
	`

	var app models.App
	err := s.db.QueryRowContext(ctx, q, appID).Scan(&app.ID, &app.Name, &app.Secret)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.App{}, fmt.Errorf("%s: %w", op, storage.ErrAppNotFound)
		}
		return models.App{}, fmt.Errorf("%s: %w", op, err)
	}

	return app, nil
}

func (s *Storage) SaveRefreshSession(
	ctx context.Context,
	userUUID uuid.UUID,
	refreshHash string,
	expiresAt time.Time,
	ip string,
	userAgent string,
	deviceID string,
	appID int64,
) error {
	const op = "storage.postgres.SaveRefreshSession"

	q := `
		INSERT INTO user_sessions
			(user_uuid, refresh_token_hash, expires_at, ip_address, user_agent, device_id, app_id)
		VALUES ($1, $2, $3, NULLIF($4,'' )::inet, NULLIF($5,''), NULLIF($6,''), $7)
	`

	_, err := s.db.ExecContext(ctx, q, userUUID, refreshHash, expiresAt, ip, userAgent, deviceID, appID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionExists)
		}
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *Storage) RefreshSessionByHash(ctx context.Context, hash string, deviceID string, appID int64) (models.RefreshSession, error) {
	const op = "storage.postgres.RefreshSessionByHash"

	q := `
		SELECT id, user_uuid, refresh_token_hash, expires_at, created_at, revoked_at, replaced_by_hash,
		       ip_address, user_agent, device_id, app_id
		FROM user_sessions
		WHERE refresh_token_hash = $1
		  AND device_id = $2
		  AND app_id = $3
		LIMIT 1
	`

	var sess models.RefreshSession
	var revokedAt sql.NullTime
	var replaced sql.NullString

	// если у тебя в модели есть эти поля — заполни; если нет, можно не сканить ip/ua/device/app
	var ip sql.NullString
	var ua sql.NullString
	var dev sql.NullString
	var app sql.NullInt64

	err := s.db.QueryRowContext(ctx, q, hash, deviceID, appID).Scan(
		&sess.ID,
		&sess.UserUUID,
		&sess.RefreshTokenHash,
		&sess.ExpiresAt,
		&sess.CreatedAt,
		&revokedAt,
		&replaced,
		&ip, &ua, &dev, &app,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.RefreshSession{}, fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
		}
		return models.RefreshSession{}, fmt.Errorf("%s: %w", op, err)
	}

	if revokedAt.Valid {
		t := revokedAt.Time
		sess.RevokedAt = &t
	}
	if replaced.Valid {
		v := replaced.String
		sess.ReplacedByHash = &v
	}

	// опционально, если поля есть в модели:
	// if ip.Valid { sess.IPAddress = &ip.String } ...
	return sess, nil
}

func (s *Storage) RotateRefreshSession(
	ctx context.Context,
	oldHash, newHash string,
	newExpiresAt time.Time,
	ip string,
	userAgent string,
	deviceID string,
	appID int64,
) error {
	const op = "storage.postgres.RotateRefreshSession"

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = tx.Rollback() }()

	// читаем старую сессию строго по (hash + device + app)
	var userUUID uuid.UUID
	var expiresAt time.Time
	var revokedAt sql.NullTime

	err = tx.QueryRowContext(ctx, `
		SELECT user_uuid, expires_at, revoked_at
		FROM user_sessions
		WHERE refresh_token_hash = $1
		  AND device_id = $2
		  AND app_id = $3
		LIMIT 1
	`, oldHash, deviceID, appID).Scan(&userUUID, &expiresAt, &revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
		}
		return fmt.Errorf("%s: %w", op, err)
	}

	if revokedAt.Valid || time.Now().After(expiresAt) {
		return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
	}

	// вставляем новую (поля берём из текущего запроса)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_sessions
			(user_uuid, refresh_token_hash, expires_at, ip_address, user_agent, device_id, app_id)
		VALUES ($1, $2, $3, NULLIF($4,'' )::inet, NULLIF($5,''), $6, $7)
	`, userUUID, newHash, newExpiresAt, ip, userAgent, deviceID, appID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionExists)
		}
		return fmt.Errorf("%s: %w", op, err)
	}

	// ревокаем старую и ставим replaced_by_hash
	res, err := tx.ExecContext(ctx, `
		UPDATE user_sessions
		SET revoked_at = NOW(), replaced_by_hash = $2
		WHERE refresh_token_hash = $1
		  AND device_id = $3
		  AND app_id = $4
		  AND revoked_at IS NULL
	`, oldHash, newHash, deviceID, appID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *Storage) RevokeRefreshSession(ctx context.Context, hash string, deviceID string, appID int64) error {
	const op = "storage.postgres.RevokeRefreshSession"

	res, err := s.db.ExecContext(ctx, `
		UPDATE user_sessions
		SET revoked_at = NOW()
		WHERE refresh_token_hash = $1
		  AND device_id = $2
		  AND app_id = $3
		  AND revoked_at IS NULL
	`, hash, deviceID, appID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
	}

	return nil
}
