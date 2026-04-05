package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/auth"
	"golang.org/x/crypto/ssh"
)

func (h *IdentityHandler) ListSSHKeys(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	keys, err := h.SSHKeys.List(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type item struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		PublicKey     string `json:"publicKey"`
		HasPrivateKey bool   `json:"hasPrivateKey"`
		StackCount    int    `json:"stackCount"`
		CreatedAt     int64  `json:"createdAt"`
	}
	result := make([]item, len(keys))
	for i, k := range keys {
		result[i] = item{
			ID: k.ID, Name: k.Name, PublicKey: k.PublicKey,
			HasPrivateKey: k.HasPrivateKey, StackCount: k.StackCount, CreatedAt: k.CreatedAt,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *IdentityHandler) CreateSSHKey(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var body struct {
		Name       string `json:"name"`
		PublicKey  string `json:"publicKey"`
		PrivateKey string `json:"privateKey"`
		Generate   bool   `json:"generate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	var generatedPrivateKey string

	if body.Generate {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			http.Error(w, "key generation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			http.Error(w, "marshal public key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		body.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))

		privBlock, err := ssh.MarshalPrivateKey(priv, "")
		if err != nil {
			http.Error(w, "marshal private key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		body.PrivateKey = string(pem.EncodeToMemory(privBlock))
		generatedPrivateKey = body.PrivateKey
	}

	if body.PublicKey == "" {
		http.Error(w, "publicKey is required (or set generate=true)", http.StatusBadRequest)
		return
	}

	key, err := h.SSHKeys.Create(user.ID, body.Name, body.PublicKey, body.PrivateKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type response struct {
		ID                  string `json:"id"`
		Name                string `json:"name"`
		PublicKey           string `json:"publicKey"`
		HasPrivateKey       bool   `json:"hasPrivateKey"`
		StackCount          int    `json:"stackCount"`
		CreatedAt           int64  `json:"createdAt"`
		GeneratedPrivateKey string `json:"generatedPrivateKey,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{
		ID: key.ID, Name: key.Name, PublicKey: key.PublicKey,
		HasPrivateKey: key.HasPrivateKey, CreatedAt: key.CreatedAt,
		GeneratedPrivateKey: generatedPrivateKey,
	})
}

func (h *IdentityHandler) DeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.SSHKeys.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DownloadSSHPrivateKey serves the encrypted-then-decrypted private key as a file download.
// A trailing newline is ensured because SSH tools reject PEM files without one.
func (h *IdentityHandler) DownloadSSHPrivateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	name, privKey, err := h.SSHKeys.GetPrivateKey(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if !strings.HasSuffix(privKey, "\n") {
		privKey += "\n"
	}

	filename := sshKeyFilename(name)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write([]byte(privKey))
}

// sshKeyFilename returns a safe filename for the private key download.
func sshKeyFilename(name string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if safe == "" {
		safe = "id_ed25519"
	}
	return safe
}
