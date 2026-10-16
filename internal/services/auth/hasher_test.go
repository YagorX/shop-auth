package auth

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"sso/internal/domain/models"
	"sso/internal/storage"
)

// MinCost: Compare работает с любой стоимостью, а тестам незачем тратить 50 мс на хеш.
func testPasswordHash(t *testing.T, password string) []byte {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}

	return hash
}

// Занимает все слоты, как будто их держат чужие bcrypt.
func occupyAllSlots(t *testing.T, h *BcryptHasher, n int64) {
	t.Helper()

	if !h.sem.TryAcquire(n) {
		t.Fatal("не удалось занять слоты семафора")
	}
	t.Cleanup(func() { h.sem.Release(n) })
}

func TestBcryptHasherRejectsWhenBusy(t *testing.T) {
	t.Parallel()

	const maxWait = 20 * time.Millisecond

	h := NewBcryptHasher(1, maxWait)
	occupyAllSlots(t, h, 1)

	started := time.Now()
	err := h.Compare(context.Background(), testPasswordHash(t, "secret"), "secret")
	elapsed := time.Since(started)

	if !errors.Is(err, storage.ErrOverloaded) {
		t.Fatalf("err = %v, want %v", err, storage.ErrOverloaded)
	}
	// Смысл ограниченной очереди — отказать быстро, а не держать клиента до его дедлайна.
	if elapsed > maxWait+200*time.Millisecond {
		t.Fatalf("отказ занял %v, ожидалось около %v", elapsed, maxWait)
	}

	if _, err := h.Hash(context.Background(), "secret"); !errors.Is(err, storage.ErrOverloaded) {
		t.Fatalf("Hash: err = %v, want %v: Hash и Compare должны делить один семафор", err, storage.ErrOverloaded)
	}
}

// Клиент ушёл раньше, чем истёк maxWait: это его таймаут, а не наша перегрузка.
func TestBcryptHasherReturnsClientContextError(t *testing.T) {
	t.Parallel()

	h := NewBcryptHasher(1, time.Second)
	occupyAllSlots(t, h, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	t.Cleanup(cancel)

	err := h.Compare(ctx, testPasswordHash(t, "secret"), "secret")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want %v", err, context.DeadlineExceeded)
	}
	if errors.Is(err, storage.ErrOverloaded) {
		t.Fatal("таймаут клиента выдан за перегрузку")
	}
}

func TestBcryptHasherReleasesSlot(t *testing.T) {
	t.Parallel()

	h := NewBcryptHasher(1, 20*time.Millisecond)
	hash := testPasswordHash(t, "secret")

	if err := h.Compare(context.Background(), hash, "secret"); err != nil {
		t.Fatalf("верный пароль: %v", err)
	}

	err := h.Compare(context.Background(), hash, "wrong")
	if !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Fatalf("неверный пароль: err = %v, want %v", err, bcrypt.ErrMismatchedHashAndPassword)
	}

	// После успеха и после ошибки слот свободен: иначе семафор «утекает» и сервис со временем перестаёт пускать логины.
	if !h.sem.TryAcquire(1) {
		t.Fatal("слот не освобождён после Compare")
	}
	h.sem.Release(1)
}

func TestBcryptHasherHashRoundTrip(t *testing.T) {
	t.Parallel()

	h := NewBcryptHasher(1, time.Second)

	hash, err := h.Hash(context.Background(), "secret")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if err := h.Compare(context.Background(), hash, "secret"); err != nil {
		t.Fatalf("Compare со своим хешем: %v", err)
	}
}

func TestNewBcryptHasherAutoSize(t *testing.T) {
	t.Parallel()

	want := max(1, int64(runtime.GOMAXPROCS(0)*3/4))

	h := NewBcryptHasher(0, time.Second)

	if !h.sem.TryAcquire(want) {
		t.Fatalf("в семафоре меньше %d слотов", want)
	}
	if h.sem.TryAcquire(1) {
		t.Fatalf("в семафоре больше %d слотов", want)
	}
}

type stubPasswordHasher struct {
	err error
}

func (s stubPasswordHasher) Hash(context.Context, string) ([]byte, error) {
	return []byte("hash"), s.err
}

func (s stubPasswordHasher) Compare(context.Context, []byte, string) error {
	return s.err
}

// «Неверный пароль» — только когда bcrypt сказал, что пароль не совпал.
// Перегрузка и таймаут клиента уходят наверх как есть: пароль мог быть верным.
func TestLoginHasherErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hasherErr error
		wantErr   error
	}{
		{
			name:      "wrong password is invalid credentials",
			hasherErr: bcrypt.ErrMismatchedHashAndPassword,
			wantErr:   storage.ErrInvalidCredentials,
		},
		{
			name:      "overload is not invalid credentials",
			hasherErr: storage.ErrOverloaded,
			wantErr:   storage.ErrOverloaded,
		},
		{
			name:      "client timeout is not invalid credentials",
			hasherErr: context.DeadlineExceeded,
			wantErr:   context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := New(
				testLogger(),
				nil,
				stubUserProvider{user: models.User{UUID: uuid.New(), Username: "user"}},
				stubAppProvider{},
				nil,
				time.Hour,
				time.Hour,
				nil,
				nil,
				nil,
				nil,
				stubPasswordHasher{err: tt.hasherErr},
				&fakeLoginLimiter{},
			)

			_, _, err := auth.Login(context.Background(), "user", "secret", 1, "device-1", "", "")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != storage.ErrInvalidCredentials && errors.Is(err, storage.ErrInvalidCredentials) {
				t.Fatalf("err = %v: сервер занят, а клиенту сказали «неверный пароль»", err)
			}
		})
	}
}

func TestRegisterHasherOverload(t *testing.T) {
	t.Parallel()

	auth := New(
		testLogger(),
		nil,
		nil,
		nil,
		nil,
		time.Hour,
		time.Hour,
		nil,
		nil,
		nil,
		nil,
		stubPasswordHasher{err: storage.ErrOverloaded},
		nil,
	)

	_, err := auth.RegisterNewUser(context.Background(), "user", "user@example.com", "secret")
	if !errors.Is(err, storage.ErrOverloaded) {
		t.Fatalf("err = %v, want %v", err, storage.ErrOverloaded)
	}
}

// Запоминает, с какими хешами сравнивали пароль.
type recordingHasher struct {
	err      error
	compared [][]byte
}

func (r *recordingHasher) Hash(context.Context, string) ([]byte, error) {
	return []byte("hash"), r.err
}

func (r *recordingHasher) Compare(_ context.Context, hash []byte, _ string) error {
	r.compared = append(r.compared, hash)
	return r.err
}

func newLoginTestAuth(userErr error, hasher PasswordHasher) *Auth {
	return New(
		testLogger(),
		nil,
		stubUserProvider{err: userErr},
		stubAppProvider{},
		nil,
		time.Hour,
		time.Hour,
		nil,
		nil,
		nil,
		nil,
		hasher,
		&fakeLoginLimiter{},
	)
}

// Несуществующий пользователь тоже платит за bcrypt: иначе ответ за доли миллисекунды
// против ~40 мс выдаёт, зарегистрирован ли email.
func TestLoginUnknownUserSpendsBcrypt(t *testing.T) {
	t.Parallel()

	hasher := &recordingHasher{err: bcrypt.ErrMismatchedHashAndPassword}
	auth := newLoginTestAuth(storage.ErrUserNotFound, hasher)

	_, _, err := auth.Login(context.Background(), "nobody@example.com", "secret", 1, "device-1", "", "")
	if !errors.Is(err, storage.ErrInvalidCredentials) {
		t.Fatalf("err = %v, want %v", err, storage.ErrInvalidCredentials)
	}
	if len(hasher.compared) != 1 {
		t.Fatalf("Compare called %d times, want 1: unknown user must cost a bcrypt too", len(hasher.compared))
	}
}

// Перегрузка на несуществующем пользователе выглядит так же, как на существующем: 429, а не 401.
func TestLoginUnknownUserUnderOverload(t *testing.T) {
	t.Parallel()

	auth := newLoginTestAuth(storage.ErrUserNotFound, &recordingHasher{err: storage.ErrOverloaded})

	_, _, err := auth.Login(context.Background(), "nobody@example.com", "secret", 1, "device-1", "", "")
	if !errors.Is(err, storage.ErrOverloaded) {
		t.Fatalf("err = %v, want %v", err, storage.ErrOverloaded)
	}
}

// Время сравнения задаёт стоимость хеша: у фиктивного она обязана совпадать с настоящими паролями.
func TestDummyPasswordHashMatchesRealCost(t *testing.T) {
	t.Parallel()

	cost, err := bcrypt.Cost(dummyPasswordHash())
	if err != nil {
		t.Fatalf("dummy hash is not a bcrypt hash: %v", err)
	}

	realHash, err := NewBcryptHasher(1, time.Second).Hash(context.Background(), "secret")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	realCost, err := bcrypt.Cost(realHash)
	if err != nil {
		t.Fatalf("real hash is not a bcrypt hash: %v", err)
	}

	if cost != realCost {
		t.Fatalf("dummy hash cost = %d, real password cost = %d", cost, realCost)
	}
}
