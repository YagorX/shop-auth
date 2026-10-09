package auth

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/semaphore"

	"sso/internal/observability"
	"sso/internal/storage"
)

// Auth зависит от интерфейса, а не от bcrypt напрямую: в тестах хешер подменяется заглушкой.
type PasswordHasher interface {
	Hash(ctx context.Context, password string) ([]byte, error)
	Compare(ctx context.Context, hash []byte, password string) error
}

// BcryptHasher ограничивает число одновременных bcrypt семафором (bulkhead).
// Без него выше потолка CPU каждый хеш делит ядра с тысячами других и считается секундами,
// а validate стоит за ними в той же очереди планировщика и тоже деградирует.
type BcryptHasher struct {
	sem     *semaphore.Weighted
	maxWait time.Duration
}

// maxConcurrent <= 0 — автоподбор: 3/4 от GOMAXPROCS, остальные ядра остаются
// под validate и refresh. Go 1.25 считает GOMAXPROCS от CPU-лимита cgroup, так что в контейнере это доля лимита.
func NewBcryptHasher(maxConcurrent int64, maxWait time.Duration) *BcryptHasher {
	if maxConcurrent <= 0 {
		maxConcurrent = max(1, int64(runtime.GOMAXPROCS(0)*3/4))
	}
	if maxWait <= 0 {
		maxWait = time.Millisecond * 200
	}

	observability.SetPasswordHashSlots(maxConcurrent)

	return &BcryptHasher{
		sem:     semaphore.NewWeighted(maxConcurrent),
		maxWait: maxWait,
	}
}

// Ждём слот не дольше maxWait: если очередь за это время не сдвинулась, перегрузка устойчивая,
// и честнее сразу отказать, чем держать клиента до его дедлайна.
func (h *BcryptHasher) acquire(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, h.maxWait)
	defer cancel()

	started := time.Now()
	err := h.sem.Acquire(waitCtx, 1)
	// Пишем и успешные ожидания, и отказы: отказы ложатся у границы maxWait, и по графику видно, что упёрлись в неё.
	observability.ObservePasswordHashWait(started)

	if err != nil {
		// waitCtx умирает и от нашего maxWait, и вместе с родителем. Различить можно только по родителю:
		// умер он — это таймаут или отмена клиента, а не наша перегрузка.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return storage.ErrOverloaded
	}

	observability.PasswordHashSlotAcquired()

	return nil
}

func (h *BcryptHasher) release() {
	h.sem.Release(1)
	observability.PasswordHashSlotReleased()
}

// Ошибка bcrypt возвращается как есть: по bcrypt.ErrMismatchedHashAndPassword Login отличает
// неверный пароль от перегрузки и таймаута.
func (h *BcryptHasher) Compare(ctx context.Context, hash []byte, password string) error {
	if err := h.acquire(ctx); err != nil {
		return err
	}
	defer h.release()

	hashStarted := time.Now()
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	observability.ObservePasswordHash("compare", hashStarted)

	return err
}

func (h *BcryptHasher) Hash(ctx context.Context, password string) ([]byte, error) {
	if err := h.acquire(ctx); err != nil {
		return nil, err
	}
	defer h.release()

	hashStarted := time.Now()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	observability.ObservePasswordHash("hash", hashStarted)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	return hash, nil
}
