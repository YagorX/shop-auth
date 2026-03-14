package tests

import (
	"testing"
	"time"

	"sso/tests/suite"

	authv1 "github.com/YagorX/shop-contracts/gen/go/proto/auth/v1"
	"github.com/brianvoe/gofakeit/v6"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/status"
)

const passDefaultLen = 10

func TestRegisterLogin_Login_HappyPath(t *testing.T) {
	ctx, st := suite.New(t)

	email := gofakeit.Email()
	pass := randomFakePassword()
	username := randomFakePassword()

	respReg, err := st.AuthClient.Register(ctx, &authv1.RegisterRequest{
		Email:    email,
		Password: pass,
		Username: username,
	})
	require.NoError(t, err)
	require.NotEmpty(t, respReg.GetUserUuid())

	loginTime := time.Now()

	respLogin, err := st.AuthClient.Login(ctx, &authv1.LoginRequest{
		EmailOrName: username,
		Password:    pass,
		AppId:       st.AppID,
		DeviceId:    st.DeviceID,
	})
	if err != nil {
		if s, ok := status.FromError(err); ok {
			t.Fatalf("login rpc failed: code=%s msg=%q err=%v", s.Code(), s.Message(), err)
		}
		t.Fatalf("login rpc failed: err=%v", err)
	}

	token := respLogin.GetAccessToken()
	require.NotEmpty(t, token)

	parsed, err := jwt.Parse(token, func(tk *jwt.Token) (interface{}, error) {
		if _, ok := tk.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(st.AppSecret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)

	claims, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)

	// ✅ твой jwt.NewToken кладёт UUID в "sub"
	sub, ok := claims["sub"].(string)
	require.True(t, ok, "sub claim should be string uuid")
	assert.Equal(t, respReg.GetUserUuid(), sub)

	assert.Equal(t, email, claims["email"].(string))
	assert.Equal(t, int(st.AppID), int(claims["app_id"].(float64)))

	const deltaSeconds = 1
	assert.InDelta(t,
		loginTime.Add(st.Cfg.TokenTTL).Unix(),
		claims["exp"].(float64),
		deltaSeconds,
	)
}

func TestRegisterLogin_DuplicatedRegistration(t *testing.T) {
	ctx, st := suite.New(t)

	email := gofakeit.Email()
	username := randomFakePassword()
	pass := randomFakePassword()

	respReg, err := st.AuthClient.Register(ctx, &authv1.RegisterRequest{
		Username: username,
		Email:    email,
		Password: pass,
	})
	require.NoError(t, err)
	require.NotEmpty(t, respReg.GetUserUuid())

	_, err = st.AuthClient.Register(ctx, &authv1.RegisterRequest{
		Username: username,
		Email:    email,
		Password: pass,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user already exists")
}

func TestRegister_FailCases(t *testing.T) {
	ctx, st := suite.New(t)

	tests := []struct {
		name        string
		email       string
		password    string
		expectedErr string
	}{
		{
			name:        "Register with Empty Password",
			email:       gofakeit.Email(),
			password:    "",
			expectedErr: "password is required",
		},
		{
			name:        "Register with Empty Email",
			email:       "",
			password:    randomFakePassword(),
			expectedErr: "email is required",
		},
		{
			name:        "Register with Both Empty",
			email:       "",
			password:    "",
			expectedErr: "email is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := st.AuthClient.Register(ctx, &authv1.RegisterRequest{
				Username: tt.name,
				Email:    tt.email,
				Password: tt.password,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestLogin_FailCases(t *testing.T) {
	ctx, st := suite.New(t)

	tests := []struct {
		name        string
		email       string
		password    string
		appID       int64
		deviceID    string
		expectedErr string
	}{
		{
			name:        "Login with Empty Password",
			email:       gofakeit.Email(),
			password:    "",
			appID:       st.AppID,
			deviceID:    st.DeviceID,
			expectedErr: "password is required",
		},
		{
			name:        "Login with Empty Email",
			email:       "",
			password:    randomFakePassword(),
			appID:       st.AppID,
			deviceID:    st.DeviceID,
			expectedErr: "email is required",
		},
		{
			name:        "Login with Both Empty Email and Password",
			email:       "",
			password:    "",
			appID:       int64(st.AppID),
			deviceID:    st.DeviceID,
			expectedErr: "email is required",
		},
		{
			name:        "Login with Non-Matching Password",
			email:       gofakeit.Email(),
			password:    randomFakePassword(),
			appID:       int64(st.AppID),
			deviceID:    st.DeviceID,
			expectedErr: "invalid email or password",
		},
		{
			name:        "Login without AppID",
			email:       gofakeit.Email(),
			password:    randomFakePassword(),
			appID:       0,
			deviceID:    st.DeviceID,
			expectedErr: "app_id is required",
		},
		{
			name:        "Login without DeviceID",
			email:       gofakeit.Email(),
			password:    randomFakePassword(),
			appID:       int64(st.AppID),
			deviceID:    "",
			expectedErr: "device_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// регаем пользователя только для кейсов "non-matching password" и "happy login",
			// но не мешает делать всегда — email/password случайные
			email := gofakeit.Email()
			pass := randomFakePassword()

			_, err := st.AuthClient.Register(ctx, &authv1.RegisterRequest{
				Username: gofakeit.UUID(),
				Email:    email,
				Password: pass,
			})
			require.NoError(t, err)

			// если кейс про “non-matching password” — используем зарегистрированный email, но другой пароль
			loginEmail := tt.email
			loginPass := tt.password
			if tt.name == "Login with Non-Matching Password" {
				loginEmail = email
				loginPass = randomFakePassword()
			}

			_, err = st.AuthClient.Login(ctx, &authv1.LoginRequest{
				EmailOrName: loginEmail,
				Password:    loginPass,
				AppId:       tt.appID,
				DeviceId:    tt.deviceID,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func randomFakePassword() string {
	return gofakeit.Password(true, true, true, true, false, passDefaultLen)
}
