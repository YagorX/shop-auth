package auth

import (
	"context"
	"fmt"
	"runtime"
	"sso/internal/observability"
	"sso/internal/storage"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/semaphore"
)

type PasswordHasher interface {
	Hash(ctx context.Context, password string) ([]byte, error)
	Compare(ctx context.Context, hash []byte, password string) error
}

type BcryptHasher struct {
	sem     *semaphore.Weighted
	maxWait time.Duration
}

func NewBcryptHasher(maxConcurrent int64, maxWait time.Duration) *BcryptHasher {
	if maxConcurrent <= 0 {
		maxConcurrent = max(1, int64(runtime.GOMAXPROCS(0)*3/4))
	}
	if maxWait <= 0 {
		maxWait = time.Millisecond * 200
	}

	return &BcryptHasher{
		sem:     semaphore.NewWeighted(maxConcurrent),
		maxWait: maxWait,
	}
}

func (h *BcryptHasher) acquire(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, h.maxWait)
	defer cancel()

	if err := h.sem.Acquire(waitCtx, 1); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return storage.ErrOverloaded
	}
	return nil
}

func (h *BcryptHasher) Compare(ctx context.Context, hash []byte, password string) error {
	if err := h.acquire(ctx); err != nil {
		return err
	}
	defer h.sem.Release(1)

	hashStarted := time.Now()
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	observability.ObservePasswordHash("compare", hashStarted)
	if err != nil {
		return err
	}
	return nil
}

func (h *BcryptHasher) Hash(ctx context.Context, password string) ([]byte, error) {
	if err := h.acquire(ctx); err != nil {
		return nil, err
	}
	defer h.sem.Release(1)

	hashStarted := time.Now()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	observability.ObservePasswordHash("hash", hashStarted)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	return hash, nil
}
