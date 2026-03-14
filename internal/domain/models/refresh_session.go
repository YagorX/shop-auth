package models

import (
	"time"

	"github.com/google/uuid"
)

type RefreshSession struct {
	ID               int64
	UserUUID         uuid.UUID
	RefreshTokenHash string
	ExpiresAt        time.Time
	CreatedAt        time.Time

	RevokedAt      *time.Time
	ReplacedByHash *string

	// поля optional
	IPAddress *string
	UserAgent *string
	DeviceID  *string
	AppID     *int64
}
