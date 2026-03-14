package handlers

import (
	"log/slog"
	"net/http"

	"sso/internal/transport/http/v1/contracts"
)

type AdminHandler struct {
	logger contracts.LogLevelController
}

func NewAdminHandler(logger contracts.LogLevelController) *AdminHandler {
	return &AdminHandler{logger: logger}
}

func (h *AdminHandler) LogLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.logger == nil {
		http.Error(w, "logger is not initialized", http.StatusInternalServerError)
		return
	}

	level := r.URL.Query().Get("level")
	if level != "" {
		if err := h.logger.SetLevel(level); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		slog.Warn("log level changed", slog.String("level", h.logger.Level().String()))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"level":"` + h.logger.Level().String() + `"}`))
}
