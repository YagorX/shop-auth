package auth

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"sso/internal/observability"
)

// InMemoryLoginLimiter блокирует вход по логину после серии неудач: maxFailures за window
// (окно считается от первой неудачи) — блокировка на lockout. Ключ — логин, а не UUID пользователя:
// иначе несуществующие email не блокировались бы никогда, и 429 выдавал бы, что аккаунт есть.
//
// Состояние в памяти процесса: при N инстансах фактический порог — N×maxFailures.
type InMemoryLoginLimiter struct {
	mu          sync.Mutex
	entries     map[[sha256.Size]byte]*loginAttempts
	maxFailures int
	window      time.Duration
	lockout     time.Duration
}

type loginAttempts struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

func NewInMemoryLoginLimiter(maxFailures int, window, lockout time.Duration) *InMemoryLoginLimiter {
	return &InMemoryLoginLimiter{
		entries:     make(map[[sha256.Size]byte]*loginAttempts),
		maxFailures: maxFailures,
		window:      window,
		lockout:     lockout,
	}
}

// Check — сколько ещё ждать до разблокировки; 0 — можно пробовать.
func (l *InMemoryLoginLimiter) Check(login string) time.Duration {
	login_sha256 := loginKey(login)

	l.mu.Lock()
	defer l.mu.Unlock()

	attempts, exists := l.entries[login_sha256]
	if !exists {
		return 0
	}

	now := time.Now()

	if attempts.lockedUntil.After(now) {
		return attempts.lockedUntil.Sub(now)
	}

	if now.Sub(attempts.windowStart) > l.window {
		// Сбросить окно и количество неудачных попыток
		attempts.failures = 0
		attempts.windowStart = now
	}

	return 0
}

// Fail засчитывает неудачу. lockedNow — эта неудача включила блокировку: её логируют один раз.
func (l *InMemoryLoginLimiter) Fail(login string) (lockedNow bool) {
	login_sha256 := loginKey(login)

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	attempts, exists := l.entries[login_sha256]
	switch {
	case !exists:
		attempts = &loginAttempts{windowStart: now}
		l.entries[login_sha256] = attempts
	case now.Before(attempts.lockedUntil):
		// Уже заблокирован: это параллельные запросы, прошедшие Check до блокировки. Не считаем,
		// иначе они продлевали бы блокировку или засчитывались в следующее окно.
		return false
	case now.Sub(attempts.windowStart) > l.window:
		// Сбросить окно и количество неудачных попыток
		attempts.failures = 0
		attempts.windowStart = now
	}

	attempts.failures++
	if attempts.failures < l.maxFailures {
		return false
	}

	attempts.failures = 0
	attempts.windowStart = now
	attempts.lockedUntil = now.Add(l.lockout)
	observability.LoginLocked()

	return true
}

func (l *InMemoryLoginLimiter) Reset(login string) {
	login_sha256 := loginKey(login)

	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.entries, login_sha256)
}

// Run раз в минуту удаляет записи, у которых кончились и окно, и блокировка. Без очистки карта
// росла бы на каждый новый логин из перебора. Работает до отмены ctx.
func (l *InMemoryLoginLimiter) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.cleanup()
		}
	}
}

// Хеш, а не сама строка: 32 байта на запись вместо логина до 255 символов, и email не лежат в памяти.
// Регистр и пробелы по краям не различаем, чтобы «Alice@x.com» и « alice@x.com» не давали лишних попыток.
func loginKey(login string) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(login))))
}

func (l *InMemoryLoginLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for key, attempts := range l.entries {
		if now.Sub(attempts.windowStart) > l.window && now.After(attempts.lockedUntil) {
			delete(l.entries, key)
		}
	}

	observability.SetLoginLimiterKeys(len(l.entries))
}
