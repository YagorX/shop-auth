package storage

import (
	"errors"
)

var (
	ErrUserExist              = errors.New("user already exists")
	ErrUserNotFound           = errors.New("user not found")
	ErrAppNotFound            = errors.New("app not found")
	ErrRefreshSessionExists   = errors.New("refresh session already exists")
	ErrRefreshSessionNotFound = errors.New("refresh session not found")
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrInvalidToken           = errors.New("invalid token")
)
