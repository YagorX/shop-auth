package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"sso/internal/domain/models"
)

const (
	claimSub   = "sub" // стандартный идентификатор субъекта (user uuid)
	claimEmail = "email"
	claimAppID = "app_id"
)

// NewToken генерирует ACCESS JWT.
// sub = user UUID (строкой)
func NewToken(user models.User, app models.App, duration time.Duration) (string, error) {
	if user.UUID == uuid.Nil {
		return "", errors.New("user UUID is empty")
	}
	if app.Secret == "" {
		return "", errors.New("app secret is empty")
	}

	now := time.Now()

	claims := jwt.MapClaims{
		claimSub:   user.UUID.String(),
		claimEmail: derefEmail(user.Email),
		claimAppID: app.ID,
		"iat":      now.Unix(),
		"exp":      now.Add(duration).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString([]byte(app.Secret))
}

// ParseTokenUUID валидирует ACCESS JWT и возвращает user UUID из sub.
// Также проверяет, что app_id в токене совпадает с app.ID.
func ParseTokenUUID(tokenString string, app models.App) (uuid.UUID, error) {
	if app.Secret == "" {
		return uuid.Nil, errors.New("app secret is empty")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(app.Secret), nil
	})
	if err != nil {
		return uuid.Nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return uuid.Nil, errors.New("invalid token")
	}

	// 1) sub
	sub, ok := claims[claimSub].(string)
	if !ok || sub == "" {
		return uuid.Nil, errors.New("missing sub in token")
	}

	userUUID, err := uuid.Parse(sub)
	if err != nil {
		return uuid.Nil, errors.New("invalid sub uuid in token")
	}

	// 2) app_id check (защита от токена, выпущенного для другого app)
	appID, err := getInt64Claim(claims, claimAppID)
	if err != nil {
		return uuid.Nil, err
	}
	if appID != app.ID {
		return uuid.Nil, errors.New("token app_id mismatch")
	}

	return userUUID, nil
}

func getInt64Claim(claims jwt.MapClaims, key string) (int64, error) {
	v, ok := claims[key]
	if !ok {
		return 0, fmt.Errorf("missing %s in token", key)
	}

	// json numbers -> float64
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	default:
		return 0, fmt.Errorf("invalid %s type in token", key)
	}
}

func derefEmail(emailPtr *string) string {
	if emailPtr == nil {
		return ""
	}
	return *emailPtr
}
