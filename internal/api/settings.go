package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/trustos/pulumi-ui/internal/db"
)

type AppSettings struct {
	BackendType string `json:"backendType"` // "local" or "s3"
	StateDir    string `json:"stateDir"`
}

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	backendType, _, _ := h.Creds.Get(db.KeyBackendType)
	if backendType == "" {
		backendType = "local"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AppSettings{
		BackendType: backendType,
		StateDir:    os.Getenv("PULUMI_UI_STATE_DIR"),
	})
}

func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var body AppSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.BackendType == "local" || body.BackendType == "s3" {
		h.Creds.Set(db.KeyBackendType, body.BackendType)
	}
	w.WriteHeader(http.StatusOK)
}
