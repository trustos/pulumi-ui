package api

import (
	"encoding/json"
	"net/http"

	"github.com/trustos/pulumi-ui/internal/programs"
)

func (h *Handler) ListPrograms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(programs.List())
}
