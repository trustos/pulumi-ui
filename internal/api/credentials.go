package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/trustos/pulumi-ui/internal/db"
)

func (h *IdentityHandler) GetCredentials(w http.ResponseWriter, r *http.Request) {
	status, err := h.Creds.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// PutCredentials accepts typed credential bundles:
//
//	{ "type": "pulumi",  "passphrase": "..." }
//	{ "type": "s3",      "namespace": "...", "bucket": "...", ... }
//	{ "type": "backend", "backendType": "local"|"s3" }
//	{ "type": "key",     "key": "...", "value": "..." }
//
// Note: OCI credentials are managed via /api/accounts, not here.
func (h *IdentityHandler) PutCredentials(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var toSet map[string]string
	switch body["type"] {
	case "pulumi":
		toSet = map[string]string{
			db.KeyPulumiPassphrase: body["passphrase"],
		}
	case "s3":
		toSet = map[string]string{
			db.KeyS3Namespace:       body["namespace"],
			db.KeyS3Region:          body["region"],
			db.KeyS3Bucket:          body["bucket"],
			db.KeyS3AccessKeyID:     body["accessKeyId"],
			db.KeyS3SecretAccessKey: body["secretAccessKey"],
		}
	case "backend":
		toSet = map[string]string{
			db.KeyBackendType: body["backendType"],
		}
	case "key":
		if key, val := body["key"], body["value"]; key != "" {
			toSet = map[string]string{key: val}
		}
	default:
		http.Error(w, "unknown credential type: "+body["type"], http.StatusBadRequest)
		return
	}

	for key, value := range toSet {
		if value == "" {
			continue // skip empty — don't overwrite existing values
		}
		if err := h.Creds.Set(key, value); err != nil {
			http.Error(w, fmt.Sprintf("failed to save %s: %v", key, err), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}
