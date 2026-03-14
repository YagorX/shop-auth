package models

import (
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	ID        int64
	UserUUID  *uuid.UUID
	Action    string
	IPAddress *string
	UserAgent *string
	Details   []byte // JSON (или map[string]any, если хочешь)
	CreatedAt time.Time
}
