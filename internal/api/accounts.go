package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/oci"
)

type accountResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	TenancyName string  `json:"tenancyName"`
	TenancyOCID string  `json:"tenancyOcid"`
	Region      string  `json:"region"`
	UserOCID    string  `json:"userOcid"`
	Fingerprint string  `json:"fingerprint"`
	Status      string  `json:"status"`
	VerifiedAt  *string `json:"verifiedAt"`
	CreatedAt   string  `json:"createdAt"`
	StackCount  int     `json:"stackCount"`
}

// ListAccounts returns all OCI accounts for the authenticated user.
func (h *IdentityHandler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	accounts, err := h.Accounts.ListForUser(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]accountResponse, 0, len(accounts))
	for _, a := range accounts {
		r := toAccountResponse(a.ID, a.Name, a.TenancyName, a.TenancyOCID, a.Region, a.UserOCID, a.Fingerprint, a.Status, a.VerifiedAt, a.CreatedAt)
		r.StackCount = a.StackCount
		result = append(result, r)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// CreateAccount adds a new OCI account for the authenticated user.
func (h *IdentityHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var body struct {
		Name         string `json:"name"`
		TenancyName  string `json:"tenancyName"`
		TenancyOCID  string `json:"tenancyOcid"`
		Region       string `json:"region"`
		UserOCID     string `json:"userOcid"`
		Fingerprint  string `json:"fingerprint"`
		PrivateKey   string `json:"privateKey"`
		SSHPublicKey string `json:"sshPublicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.TenancyOCID == "" || body.Region == "" ||
		body.UserOCID == "" || body.Fingerprint == "" || body.PrivateKey == "" {
		http.Error(w, "name, tenancyOcid, region, userOcid, fingerprint, privateKey are required", http.StatusBadRequest)
		return
	}

	account, err := h.Accounts.Create(user.ID, body.Name, body.TenancyName, body.TenancyOCID, body.Region,
		body.UserOCID, body.Fingerprint, body.PrivateKey, body.SSHPublicKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toAccountResponse(account.ID, account.Name, account.TenancyName, account.TenancyOCID, account.Region, account.UserOCID, account.Fingerprint, account.Status, account.VerifiedAt, account.CreatedAt))
}

// GetAccount returns a single OCI account.
func (h *IdentityHandler) GetAccount(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toAccountResponse(account.ID, account.Name, account.TenancyName, account.TenancyOCID, account.Region, account.UserOCID, account.Fingerprint, account.Status, account.VerifiedAt, account.CreatedAt))
}

// UpdateAccount replaces the credentials of an existing OCI account.
func (h *IdentityHandler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := h.Accounts.Get(id)
	if err != nil || existing == nil || existing.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	var body struct {
		Name         string `json:"name"`
		TenancyName  string `json:"tenancyName"`
		TenancyOCID  string `json:"tenancyOcid"`
		Region       string `json:"region"`
		UserOCID     string `json:"userOcid"`
		Fingerprint  string `json:"fingerprint"`
		PrivateKey   string `json:"privateKey"`
		SSHPublicKey string `json:"sshPublicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if err := h.Accounts.UpdatePartial(id, body.Name, body.TenancyName, body.TenancyOCID, body.Region,
		body.UserOCID, body.Fingerprint, body.PrivateKey, body.SSHPublicKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// DeleteAccount removes an OCI account.
func (h *IdentityHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	count, err := h.Accounts.CountStacks(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if count > 0 {
		http.Error(w, fmt.Sprintf("cannot delete: %d stack(s) are linked to this account", count), http.StatusConflict)
		return
	}

	if err := h.Accounts.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// VerifyAccount tests the account's OCI credentials and updates its status.
func (h *IdentityHandler) VerifyAccount(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	client, err := oci.NewClient(account.TenancyOCID, account.UserOCID, account.Fingerprint, account.PrivateKey, account.Region)
	if err != nil {
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	verifyErr := client.VerifyCredentials()
	if verifyErr != nil {
		_ = h.Accounts.SetStatus(id, "error", nil)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": verifyErr.Error()})
		return
	}

	now := time.Now().Unix()
	if err := h.Accounts.SetStatus(id, "verified", &now); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Best-effort: fetch tenancy name. Fails silently if IAM policy is missing.
	if tenancyName := client.GetTenancyName(); tenancyName != "" {
		_ = h.Accounts.SetTenancyName(id, tenancyName)
		account.TenancyName = tenancyName
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "verified",
		"tenancyName": account.TenancyName,
	})
}

// ListShapes returns available compute shapes for the account's region.
func (h *IdentityHandler) ListShapes(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	client, err := oci.NewClient(account.TenancyOCID, account.UserOCID, account.Fingerprint, account.PrivateKey, account.Region)
	if err != nil {
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	shapes, err := client.ListShapes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(shapes)
}

// ListImages returns Oracle Linux ARM images for the account's region.
func (h *IdentityHandler) ListImages(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	client, err := oci.NewClient(account.TenancyOCID, account.UserOCID, account.Fingerprint, account.PrivateKey, account.Region)
	if err != nil {
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	images, err := client.ListImages()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(images)
}

// ListCompartments returns active compartments for the account's tenancy.
func (h *IdentityHandler) ListCompartments(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	client, err := oci.NewClient(account.TenancyOCID, account.UserOCID, account.Fingerprint, account.PrivateKey, account.Region)
	if err != nil {
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	compartments, err := client.ListCompartments()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(compartments)
}

// ListAvailabilityDomains returns availability domains for the account's region.
func (h *IdentityHandler) ListAvailabilityDomains(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	account, err := h.Accounts.Get(id)
	if err != nil || account == nil || account.UserID != user.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	client, err := oci.NewClient(account.TenancyOCID, account.UserOCID, account.Fingerprint, account.PrivateKey, account.Region)
	if err != nil {
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	ads, err := client.ListAvailabilityDomains()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ads)
}

func toAccountResponse(id, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, status string, verifiedAt *int64, createdAt int64) accountResponse {
	resp := accountResponse{
		ID:          id,
		Name:        name,
		TenancyName: tenancyName,
		TenancyOCID: tenancyOCID,
		Region:      region,
		UserOCID:    userOCID,
		Fingerprint: fingerprint,
		Status:      status,
		CreatedAt:   time.Unix(createdAt, 0).Format(time.RFC3339),
	}
	if verifiedAt != nil {
		ts := time.Unix(*verifiedAt, 0).Format(time.RFC3339)
		resp.VerifiedAt = &ts
	}
	return resp
}
