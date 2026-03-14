package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           int64
	UUID         uuid.UUID
	Username     string
	Email        *string // email может быть NULL
	PasswordHash []byte

	RoleID   *int64
	IsActive bool

	CreatedAt time.Time
	UpdatedAt time.Time
}
