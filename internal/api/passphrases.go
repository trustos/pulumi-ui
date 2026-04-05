package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *IdentityHandler) ListPassphrases(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Passphrases.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type item struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		StackCount int    `json:"stackCount"`
		CreatedAt  int64  `json:"createdAt"`
	}
	result := make([]item, len(rows))
	for i, row := range rows {
		result[i] = item{ID: row.ID, Name: row.Name, StackCount: row.StackCount, CreatedAt: row.CreatedAt}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *IdentityHandler) CreatePassphrase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Value == "" {
		http.Error(w, "name and value are required", http.StatusBadRequest)
		return
	}

	row, err := h.Passphrases.Create(body.Name, body.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": row.ID, "name": row.Name, "stackCount": 0, "createdAt": row.CreatedAt,
	})
}

func (h *IdentityHandler) RenamePassphrase(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := h.Passphrases.Rename(id, body.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *IdentityHandler) GetPassphraseValue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	value, err := h.Passphrases.GetValue(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"value": value})
}

func (h *IdentityHandler) DeletePassphrase(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Passphrases.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}
