package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	authv1 "github.com/YagorX/shop-contracts/gen/go/proto/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type server struct {
	grpc authv1.AuthServiceClient
}

func main() {
	grpcAddr := env("GRPC_ADDR", "localhost:44044")
	httpAddr := env("HTTP_ADDR", ":8080")

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	s := &server{grpc: authv1.NewAuthServiceClient(conn)}

	mux := http.NewServeMux()

	// API
	mux.HandleFunc("POST /api/register", s.handleRegister)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/validate", s.handleValidate)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("POST /api/is-admin", s.handleIsAdmin)

	// Static UI
	mux.Handle("/", http.FileServer(http.Dir("./web")))

	log.Printf("HTTP gateway listening on %s (gRPC=%s)", httpAddr, grpcAddr)
	if err := http.ListenAndServe(httpAddr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// простая CORS, чтобы UI работал
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	type req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Username string `json:"username"`
	}
	var body req
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	ctx, cancel := ctxWithTimeout(r, 5*time.Second)
	defer cancel()

	resp, err := s.grpc.Register(ctx, &authv1.RegisterRequest{
		Email:    strings.TrimSpace(body.Email),
		Password: body.Password,
		Username: strings.TrimSpace(body.Username),
	})
	if err != nil {
		writeGrpcErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_uuid": resp.GetUserUuid(),
	})
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	type req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		AppID    int64  `json:"app_id"`
		DeviceID string `json:"device_id"`
	}
	var body req
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	ctx, cancel := ctxWithTimeout(r, 5*time.Second)
	defer cancel()

	resp, err := s.grpc.Login(ctx, &authv1.LoginRequest{
		EmailOrName: strings.TrimSpace(body.Email),
		Password:    body.Password,
		AppId:       body.AppID,
		DeviceId:    strings.TrimSpace(body.DeviceID),
	})
	if err != nil {
		writeGrpcErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  resp.GetAccessToken(),
		"refresh_token": resp.GetRefreshToken(),
	})
}

func (s *server) handleValidate(w http.ResponseWriter, r *http.Request) {
	type req struct {
		Token string `json:"token"`
		AppID int64  `json:"app_id"`
	}
	var body req
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	ctx, cancel := ctxWithTimeout(r, 5*time.Second)
	defer cancel()

	resp, err := s.grpc.ValidateToken(ctx, &authv1.ValidateTokenRequest{
		Token: strings.TrimSpace(body.Token),
		AppId: body.AppID,
	})
	if err != nil {
		writeGrpcErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_uuid": resp.GetUserUuid(),
	})
}

func (s *server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	type req struct {
		RefreshToken string `json:"refresh_token"`
		AppID        int64  `json:"app_id"`
		DeviceID     string `json:"device_id"`
	}
	var body req
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	ctx, cancel := ctxWithTimeout(r, 5*time.Second)
	defer cancel()

	resp, err := s.grpc.Refresh(ctx, &authv1.RefreshRequest{
		RefreshToken: strings.TrimSpace(body.RefreshToken),
		AppId:        body.AppID,
		DeviceId:     strings.TrimSpace(body.DeviceID),
	})
	if err != nil {
		writeGrpcErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  resp.GetAccessToken(),
		"refresh_token": resp.GetRefreshToken(),
	})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	type req struct {
		RefreshToken string `json:"refresh_token"`
		AppID        int64  `json:"app_id"`
		DeviceID     string `json:"device_id"`
	}
	var body req
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	ctx, cancel := ctxWithTimeout(r, 5*time.Second)
	defer cancel()

	_, err := s.grpc.Logout(ctx, &authv1.LogoutRequest{
		RefreshToken: strings.TrimSpace(body.RefreshToken),
		AppId:        body.AppID,
		DeviceId:     strings.TrimSpace(body.DeviceID),
	})
	if err != nil {
		writeGrpcErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleIsAdmin(w http.ResponseWriter, r *http.Request) {
	type req struct {
		UserUUID string `json:"user_uuid"`
	}
	var body req
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	ctx, cancel := ctxWithTimeout(r, 5*time.Second)
	defer cancel()

	resp, err := s.grpc.IsAdmin(ctx, &authv1.IsAdminRequest{
		UserUuid: strings.TrimSpace(body.UserUUID),
	})
	if err != nil {
		writeGrpcErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"is_admin": resp.GetIsAdmin(),
	})
}

func ctxWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	// уважаем отмену запроса браузером + добавляем таймаут
	ctx := r.Context()
	return context.WithTimeout(ctx, d)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: failed to encode response", slog.String("error", err.Error()))
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func writeGrpcErr(w http.ResponseWriter, err error) {
	// чтобы в UI было видно нормальную ошибку
	// здесь можно маппить status.Code → http code, но пока достаточно текста
	if err == nil {
		writeErr(w, 500, "unknown error")
		return
	}
	writeErr(w, 500, err.Error())
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
