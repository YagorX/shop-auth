package sqlite

// import (
// 	"context"
// 	"database/sql"
// 	"errors"
// 	"fmt"
// 	"strconv"
// 	"time"

// 	"log/slog"
// 	"sso/internal/domain/models"
// 	"sso/internal/lib/logger/sl"
// 	"sso/internal/storage"

// 	"github.com/mattn/go-sqlite3"
// )

// type Storage struct {
// 	db  *sql.DB
// 	log *slog.Logger
// }

// // new creates a new instance of the sqlite storage
// func New(storagePath string, log *slog.Logger) (*Storage, error) {
// 	const op = "storage.sqlite.New"
// 	println("storagePath: ", storagePath)
// 	// path to file
// 	db, err := sql.Open("sqlite3", storagePath)
// 	if err != nil {
// 		return nil, fmt.Errorf("%s: %w", op, err)
// 	}

// 	return &Storage{
// 		db:  db,
// 		log: log,
// 	}, nil
// }

// func (s *Storage) SaveUser(ctx context.Context, email string, passHash []byte) (int64, error) {
// 	const op = "storage.sqlite.SaveUser"

// 	// example
// 	stmt, err := s.db.Prepare("INSERT INTO users(email, pass_hash) VALUES (?, ?)")
// 	if err != nil {
// 		return 0, fmt.Errorf("%s: %w", op, err)
// 	}

// 	defer stmt.Close()

// 	res, err := stmt.ExecContext(ctx, email, passHash)
// 	if err != nil {
// 		// ПРАВИЛЬНАЯ проверка UNIQUE constraint
// 		var sqliteErr sqlite3.Error
// 		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
// 			return 0, fmt.Errorf("%s: %w", op, storage.ErrUserExist)
// 		}

// 		return 0, fmt.Errorf("%s: %w", op, err)
// 	}

// 	id, err := res.LastInsertId()
// 	if err != nil {
// 		return 0, fmt.Errorf("%s: %w", op, err)
// 	}

// 	return id, nil
// }

// func (s *Storage) SaveRefreshSession(
// 	ctx context.Context,
// 	userID int64,
// 	refreshHash string,
// 	expiresAt time.Time,
// ) error {
// 	const op = "storage.sqlite.SaveRefreshSession"

// 	stmt, err := s.db.Prepare(`
// 		INSERT INTO refresh_sessions(user_id, token_hash, expires_at, created_at)
// 		VALUES (?, ?, ?, ?)
// 	`)
// 	if err != nil {
// 		return fmt.Errorf("%s: %w", op, err)
// 	}
// 	defer stmt.Close()

// 	now := time.Now().Unix()
// 	exp := expiresAt.Unix()

// 	_, err = stmt.ExecContext(ctx, userID, refreshHash, exp, now)
// 	if err != nil {
// 		// обработка unique constraint (если вдруг refreshHash совпал — редко, но пусть будет)
// 		var sqliteErr sqlite3.Error
// 		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
// 			return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionExists)
// 		}
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	return nil
// }

// func (s *Storage) RefreshSessionByHash(ctx context.Context, hash string) (models.RefreshSession, error) {
// 	const op = "storage.sqlite.GetRefreshSessionByHash"

// 	stmt, err := s.db.Prepare(`
// 		SELECT id, user_id, token_hash, expires_at, created_at, revoked_at, replaced_by_hash
// 		FROM refresh_sessions
// 		WHERE token_hash = ?
// 		LIMIT 1
// 	`)
// 	if err != nil {
// 		return models.RefreshSession{}, fmt.Errorf("%s: %w", op, err)
// 	}
// 	defer stmt.Close()

// 	var (
// 		sess           models.RefreshSession
// 		expiresAtUnix  int64
// 		createdAtUnix  int64
// 		revokedAtNull  sql.NullInt64
// 		replacedByNull sql.NullString
// 	)

// 	err = stmt.QueryRowContext(ctx, hash).Scan(
// 		&sess.ID,
// 		&sess.UserID,
// 		&sess.TokenHash,
// 		&expiresAtUnix,
// 		&createdAtUnix,
// 		&revokedAtNull,
// 		&replacedByNull,
// 	)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			return models.RefreshSession{}, fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
// 		}
// 		return models.RefreshSession{}, fmt.Errorf("%s: %w", op, err)
// 	}

// 	sess.ExpiresAt = time.Unix(expiresAtUnix, 0)
// 	sess.CreatedAt = time.Unix(createdAtUnix, 0)

// 	if revokedAtNull.Valid {
// 		t := time.Unix(revokedAtNull.Int64, 0)
// 		sess.RevokedAt = &t
// 	}
// 	if replacedByNull.Valid {
// 		v := replacedByNull.String
// 		sess.ReplacedByHash = &v
// 	}

// 	return sess, nil
// }

// // RotateRefreshSession делает “ротацию” refresh: старый пометить revoked + replaced_by_hash,
// // новый вставить. Всё атомарно в транзакции.
// func (s *Storage) RotateRefreshSession(ctx context.Context, oldHash, newHash string, newExpiresAt time.Time) error {
// 	const op = "storage.sqlite.RotateRefreshSession"

// 	tx, err := s.db.BeginTx(ctx, nil)
// 	if err != nil {
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	now := time.Now().Unix()
// 	newExp := newExpiresAt.Unix()

// 	// 1) достаём user_id старой сессии и проверяем что она жива (не revoked и не истекла)
// 	var (
// 		userID    int64
// 		expiresAt int64
// 		revokedAt sql.NullInt64
// 	)

// 	err = tx.QueryRowContext(ctx, `
// 		SELECT user_id, expires_at, revoked_at
// 		FROM refresh_sessions
// 		WHERE token_hash = ?
// 		LIMIT 1
// 	`, oldHash).Scan(&userID, &expiresAt, &revokedAt)

// 	if err != nil {
// 		_ = tx.Rollback()
// 		if errors.Is(err, sql.ErrNoRows) {
// 			return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
// 		}
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	if revokedAt.Valid || now >= expiresAt {
// 		_ = tx.Rollback()
// 		// можно вернуть отдельную ошибку “refresh expired/revoked”
// 		return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
// 	}

// 	// 2) вставляем новую сессию
// 	_, err = tx.ExecContext(ctx, `
// 		INSERT INTO refresh_sessions(user_id, token_hash, expires_at, created_at)
// 		VALUES (?, ?, ?, ?)
// 	`, userID, newHash, newExp, now)
// 	if err != nil {
// 		_ = tx.Rollback()

// 		var sqliteErr sqlite3.Error
// 		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
// 			// крайне маловероятно, но на всякий случай
// 			return fmt.Errorf("%s: %w", op, err)
// 		}
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	// 3) помечаем старую revoked + replaced_by_hash
// 	_, err = tx.ExecContext(ctx, `
// 		UPDATE refresh_sessions
// 		SET revoked_at = ?, replaced_by_hash = ?
// 		WHERE token_hash = ? AND revoked_at IS NULL
// 	`, now, newHash, oldHash)
// 	if err != nil {
// 		_ = tx.Rollback()
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	if err := tx.Commit(); err != nil {
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	return nil
// }

// func (s *Storage) RevokeRefreshSession(ctx context.Context, hash string) error {
// 	const op = "storage.sqlite.RevokeRefreshSession"

// 	stmt, err := s.db.Prepare(`
// 		UPDATE refresh_sessions
// 		SET revoked_at = ?
// 		WHERE token_hash = ? AND revoked_at IS NULL
// 	`)
// 	if err != nil {
// 		return fmt.Errorf("%s: %w", op, err)
// 	}
// 	defer stmt.Close()

// 	now := time.Now().Unix()

// 	res, err := stmt.ExecContext(ctx, now, hash)
// 	if err != nil {
// 		return fmt.Errorf("%s: %w", op, err)
// 	}

// 	// Если хочешь строго: ошибка если не нашли
// 	affected, err := res.RowsAffected()
// 	if err == nil && affected == 0 {
// 		return fmt.Errorf("%s: %w", op, storage.ErrRefreshSessionNotFound)
// 	}

// 	return nil
// }

// // info aboout user
// func (s *Storage) User(ctx context.Context, email string) (models.User, error) {
// 	const op = "storage.sqlite.User"

// 	stmt, err := s.db.Prepare("SELECT * FROM users WHERE email = ?")
// 	if err != nil {
// 		return models.User{}, fmt.Errorf("%s: %w", op, err)
// 	}

// 	row := stmt.QueryRowContext(ctx, email)

// 	var user models.User
// 	err = row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.a)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			return models.User{}, fmt.Errorf("%s: %w", op, storage.ErrUserNotFound)
// 		}

// 		return models.User{}, fmt.Errorf("%s: %w", op, err)
// 	}

// 	return user, nil
// }

// func (s *Storage) IsAdmin(ctx context.Context, userID int64) (bool, error) {
// 	const op = "storage.sqlite.IsAdmin"

// 	var test int
// 	err := s.db.QueryRow("SELECT 1").Scan(&test)
// 	if err != nil {
// 		s.log.Error("FAILED - Simple query:", sl.Err(err))
// 		return false, fmt.Errorf("database broken: %w", err)
// 	}
// 	s.log.Info("PASSED - Simple query works")

// 	// Проверяем какие колонки есть в таблице users
// 	rows, err := s.db.Query("PRAGMA table_info(users)")
// 	if err != nil {
// 		s.log.Error("FAILED - Cannot get table info:", sl.Err(err))
// 		return false, fmt.Errorf("cannot get table info: %w", err)
// 	}
// 	defer rows.Close()

// 	// Проверяем есть ли данные в таблице
// 	var rowCount int
// 	err = s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&rowCount)
// 	if err != nil {
// 		s.log.Error("FAILED - Cannot count rows:", sl.Err(err))
// 		return false, fmt.Errorf("cannot count rows: %w", err)
// 	}
// 	var tmp string = "Total rows in users table:" + strconv.Itoa(rowCount)
// 	s.log.Info(tmp)

// 	// Проверяем конкретного пользователя
// 	var isAdmin bool
// 	err = s.db.QueryRow("SELECT is_admin FROM users WHERE id = ?", userID).Scan(&isAdmin)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			println("FAILED - Cannot check user:", err.Error())
// 			return false, fmt.Errorf("%s: %w", op, storage.ErrUserNotFound)
// 		}
// 		return false, fmt.Errorf("%s: %w", op, err)
// 	}

// 	return isAdmin, nil
// }

// func (s *Storage) App(ctx context.Context, appID int64) (models.App, error) {
// 	const op = "storage.sqlite.App"

// 	stmt, err := s.db.Prepare("SELECT id, name, secret FROM apps WHERE id = ?")
// 	if err != nil {
// 		return models.App{}, fmt.Errorf("%s: %w", op, err)
// 	}

// 	row := stmt.QueryRowContext(ctx, appID)

// 	var app models.App
// 	err = row.Scan(&app.ID, &app.Name, &app.Secret)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			return models.App{}, fmt.Errorf("%s: %w", op, storage.ErrAppNotFound)
// 		}

// 		return models.App{}, fmt.Errorf("%s: %w", op, err)
// 	}

// 	return app, nil
// }

// func (s *Storage) UserByID(ctx context.Context, userID int64) (models.User, error) {
// 	const op = "storage.sqlite.UserById"

// 	query := `SELECT id, email, pass_hash, is_admin FROM users WHERE id = ?`
// 	var user models.User
// 	err := s.db.QueryRowContext(ctx, query, userID).Scan(&user.ID, &user.Email, &user.PassHash, &user.IsAdmin)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			return models.User{}, storage.ErrUserNotFound
// 		}
// 		return models.User{}, fmt.Errorf("%s: %w", op, err)
// 	}

// 	return user, nil
// }
