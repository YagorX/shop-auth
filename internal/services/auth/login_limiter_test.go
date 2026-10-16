package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"sso/internal/domain/models"
	"sso/internal/storage"
)

const (
	testMaxFailures = 3
	testWindow      = 15 * time.Minute
	testLockout     = 5 * time.Minute
)

func newTestLoginLimiter() *InMemoryLoginLimiter {
	return NewInMemoryLoginLimiter(testMaxFailures, testWindow, testLockout)
}

// Засчитывает n неудач; возвращает, на какой по счёту включилась блокировка (0 — ни на какой).
func failTimes(l *InMemoryLoginLimiter, login string, n int) int {
	lockedAt := 0
	for i := 1; i <= n; i++ {
		if l.Fail(login) && lockedAt == 0 {
			lockedAt = i
		}
	}

	return lockedAt
}

func limiterSize(l *InMemoryLoginLimiter) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.entries)
}

// Внутри synctest время виртуальное: time.Sleep сдвигает часы мгновенно, а без Sleep они стоят.
// Поэтому остаток блокировки сравнивается точно, без допусков.

func TestLoginLimiterLocksAfterMaxFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newTestLoginLimiter()

		if got := failTimes(l, "user@example.com", testMaxFailures-1); got != 0 {
			t.Fatalf("locked at failure %d, want no lock before %d", got, testMaxFailures)
		}
		if wait := l.Check("user@example.com"); wait != 0 {
			t.Fatalf("Check = %v before the threshold, want 0", wait)
		}

		if !l.Fail("user@example.com") {
			t.Fatalf("failure %d did not report the lock", testMaxFailures)
		}
		if wait := l.Check("user@example.com"); wait != testLockout {
			t.Fatalf("Check = %v right after the lock, want %v", wait, testLockout)
		}

		time.Sleep(testLockout - time.Second)
		if wait := l.Check("user@example.com"); wait != time.Second {
			t.Fatalf("Check = %v a second before unlock, want 1s", wait)
		}

		time.Sleep(time.Second)
		if wait := l.Check("user@example.com"); wait != 0 {
			t.Fatalf("Check = %v after the lockout, want 0", wait)
		}
	})
}

// Неудачи во время блокировки — параллельные запросы, прошедшие Check до неё.
// Они не продлевают блокировку и не переходят в следующий счёт.
func TestLoginLimiterIgnoresFailuresWhileLocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newTestLoginLimiter()
		failTimes(l, "user", testMaxFailures)

		time.Sleep(time.Minute)
		if got := failTimes(l, "user", 10); got != 0 {
			t.Fatalf("failure %d during the lock reported a new lock", got)
		}
		if wait := l.Check("user"); wait != testLockout-time.Minute {
			t.Fatalf("Check = %v, want %v: failures during the lock must not extend it", wait, testLockout-time.Minute)
		}

		time.Sleep(testLockout)
		if got := failTimes(l, "user", testMaxFailures); got != testMaxFailures {
			t.Fatalf("after the lockout locked at failure %d, want %d: count restarts from zero", got, testMaxFailures)
		}
	})
}

func TestLoginLimiterWindowExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newTestLoginLimiter()
		failTimes(l, "user", testMaxFailures-1)

		time.Sleep(testWindow + time.Second)

		if l.Fail("user") {
			t.Fatal("failure in a new window locked: old failures must have expired")
		}
		if wait := l.Check("user"); wait != 0 {
			t.Fatalf("Check = %v, want 0", wait)
		}
	})
}

func TestLoginLimiterResetClearsFailures(t *testing.T) {
	l := newTestLoginLimiter()
	failTimes(l, "user", testMaxFailures-1)

	l.Reset("user")

	if got := failTimes(l, "user", testMaxFailures-1); got != 0 {
		t.Fatalf("locked at failure %d after Reset", got)
	}
}

// Регистр и пробелы по краям — тот же логин, иначе вариации написания давали бы лишние попытки.
func TestLoginLimiterNormalizesLogin(t *testing.T) {
	l := newTestLoginLimiter()

	l.Fail("Alice@Example.com")
	l.Fail("  alice@example.com ")
	if !l.Fail("ALICE@EXAMPLE.COM") {
		t.Fatal("spelling variants of one login are counted separately")
	}
	if l.Check("alice@example.com") == 0 {
		t.Fatal("normalized login is not locked")
	}
}

func TestLoginLimiterKeysAreIndependent(t *testing.T) {
	l := newTestLoginLimiter()
	failTimes(l, "victim", testMaxFailures)

	if wait := l.Check("bystander"); wait != 0 {
		t.Fatalf("Check(bystander) = %v: one login's lock leaked to another", wait)
	}
}

// Очистка удаляет записи с истёкшим окном и не трогает действующие блокировки; Run выходит по отмене ctx.
func TestLoginLimiterRunCleansUpExpiredEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newTestLoginLimiter()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			l.Run(ctx)
			close(done)
		}()

		l.Fail("stale")
		time.Sleep(testWindow)
		failTimes(l, "locked", testMaxFailures)

		// Ближайший тик очистки — через минуту: у stale окно к этому моменту истекло, у locked идёт блокировка.
		time.Sleep(time.Minute + time.Second)

		if n := limiterSize(l); n != 1 {
			t.Fatalf("entries = %d after cleanup, want 1 (only the locked login)", n)
		}
		if l.Check("locked") == 0 {
			t.Fatal("cleanup removed an active lock")
		}

		cancel()
		<-done
	})
}

// Под -race: Check, Fail и Reset дёргаются из всех запросов Login одновременно.
func TestLoginLimiterConcurrentUse(t *testing.T) {
	l := NewInMemoryLoginLimiter(1000, time.Minute, time.Minute)

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 200 {
				l.Check("user")
				l.Fail("user")
				l.Reset("other")
			}
		})
	}
	wg.Wait()
}

// ── Login + лимитер ─────────────────────────────────────────────────────────────

// Запоминает вызовы; locked > 0 — Check сообщает о блокировке.
type fakeLoginLimiter struct {
	locked time.Duration
	failed []string
	reset  []string
}

func (f *fakeLoginLimiter) Check(string) time.Duration { return f.locked }

func (f *fakeLoginLimiter) Fail(login string) bool {
	f.failed = append(f.failed, login)
	return false
}

func (f *fakeLoginLimiter) Reset(login string) { f.reset = append(f.reset, login) }

// Считает обращения к базе: заблокированный логин не должен до неё доходить.
type countingUserProvider struct {
	user  models.User
	err   error
	calls int
}

func (p *countingUserProvider) User(context.Context, string) (models.User, error) {
	p.calls++
	return p.user, p.err
}

func (p *countingUserProvider) UserByUUID(context.Context, uuid.UUID) (models.User, error) {
	return p.user, p.err
}

func (p *countingUserProvider) IsAdminByUUID(context.Context, uuid.UUID) (bool, error) {
	return false, p.err
}

type stubRefreshSessionSaver struct{}

func (stubRefreshSessionSaver) SaveRefreshSession(context.Context, uuid.UUID, string, time.Time, string, string, string, int64) error {
	return nil
}

func newLimitedLoginAuth(users UserProvider, hasher PasswordHasher, limiter LoginLimiter) *Auth {
	return New(
		testLogger(),
		nil,
		users,
		stubAppProvider{},
		nil,
		time.Hour,
		time.Hour,
		stubRefreshSessionSaver{},
		nil,
		nil,
		nil,
		hasher,
		limiter,
	)
}

// Заблокированный логин не стоит ни запроса в БД, ни слота bcrypt, и сам блокировку не продлевает.
func TestLoginLockedSkipsLookupAndBcrypt(t *testing.T) {
	t.Parallel()

	users := &countingUserProvider{err: storage.ErrUserNotFound}
	hasher := &recordingHasher{err: bcrypt.ErrMismatchedHashAndPassword}
	limiter := &fakeLoginLimiter{locked: 2 * time.Minute}

	_, _, err := newLimitedLoginAuth(users, hasher, limiter).
		Login(context.Background(), "user", "secret", 1, "device-1", "", "")

	var locked *storage.LoginLockedError
	if !errors.As(err, &locked) {
		t.Fatalf("err = %v, want *storage.LoginLockedError", err)
	}
	if locked.RetryAfter != 2*time.Minute {
		t.Fatalf("RetryAfter = %v, want 2m", locked.RetryAfter)
	}
	if users.calls != 0 {
		t.Fatalf("User() called %d times for a locked login", users.calls)
	}
	if len(hasher.compared) != 0 {
		t.Fatalf("Compare called %d times for a locked login", len(hasher.compared))
	}
	if len(limiter.failed) != 0 {
		t.Fatalf("Fail called for a locked login: %v", limiter.failed)
	}
}

// Неудача — только неверные учётные данные, и для существующего, и для несуществующего пользователя:
// иначе по блокировке было бы видно, есть ли аккаунт. Перегрузка и дедлайн — не неудача.
func TestLoginCountsOnlyInvalidCredentials(t *testing.T) {
	t.Parallel()

	existing := models.User{UUID: uuid.New(), Username: "user", PasswordHash: []byte("hash")}

	tests := []struct {
		name       string
		userErr    error
		hasherErr  error
		wantErr    error
		wantFailed int
	}{
		{name: "wrong password", hasherErr: bcrypt.ErrMismatchedHashAndPassword, wantErr: storage.ErrInvalidCredentials, wantFailed: 1},
		{name: "unknown user", userErr: storage.ErrUserNotFound, hasherErr: bcrypt.ErrMismatchedHashAndPassword, wantErr: storage.ErrInvalidCredentials, wantFailed: 1},
		{name: "overload", hasherErr: storage.ErrOverloaded, wantErr: storage.ErrOverloaded},
		{name: "unknown user under overload", userErr: storage.ErrUserNotFound, hasherErr: storage.ErrOverloaded, wantErr: storage.ErrOverloaded},
		{name: "client timeout", hasherErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "database down", userErr: errDBDown, wantErr: errDBDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limiter := &fakeLoginLimiter{}
			auth := newLimitedLoginAuth(
				&countingUserProvider{user: existing, err: tt.userErr},
				&recordingHasher{err: tt.hasherErr},
				limiter,
			)

			_, _, err := auth.Login(context.Background(), "user", "secret", 1, "device-1", "", "")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(limiter.failed) != tt.wantFailed {
				t.Fatalf("Fail called %d times, want %d", len(limiter.failed), tt.wantFailed)
			}
			if len(limiter.reset) != 0 {
				t.Fatalf("Reset called on a failed login: %v", limiter.reset)
			}
		})
	}
}

func TestLoginSuccessResetsLimiter(t *testing.T) {
	t.Parallel()

	limiter := &fakeLoginLimiter{}
	auth := newLimitedLoginAuth(
		&countingUserProvider{user: models.User{UUID: uuid.New(), Username: "user", PasswordHash: []byte("hash")}},
		&recordingHasher{},
		limiter,
	)

	if _, _, err := auth.Login(context.Background(), "user", "secret", 1, "device-1", "", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(limiter.reset) != 1 || limiter.reset[0] != "user" {
		t.Fatalf("Reset calls = %v, want [user]", limiter.reset)
	}
	if len(limiter.failed) != 0 {
		t.Fatalf("Fail called on a successful login: %v", limiter.failed)
	}
}

// Сквозь настоящий лимитер: после порога неудач следующий вход отвечает блокировкой, а не 401.
func TestLoginLocksAfterRepeatedFailures(t *testing.T) {
	t.Parallel()

	auth := newLimitedLoginAuth(
		&countingUserProvider{err: storage.ErrUserNotFound},
		&recordingHasher{err: bcrypt.ErrMismatchedHashAndPassword},
		newTestLoginLimiter(),
	)

	for i := range testMaxFailures {
		_, _, err := auth.Login(context.Background(), "nobody@example.com", "guess", 1, "device-1", "", "")
		if !errors.Is(err, storage.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v, want %v", i+1, err, storage.ErrInvalidCredentials)
		}
	}

	_, _, err := auth.Login(context.Background(), "Nobody@example.com", "guess", 1, "device-1", "", "")
	var locked *storage.LoginLockedError
	if !errors.As(err, &locked) {
		t.Fatalf("err = %v after %d failures, want *storage.LoginLockedError", err, testMaxFailures)
	}
	if locked.RetryAfter <= 0 || locked.RetryAfter > testLockout {
		t.Fatalf("RetryAfter = %v, want in (0, %v]", locked.RetryAfter, testLockout)
	}
}
