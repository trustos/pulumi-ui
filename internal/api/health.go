package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/db"
)

type ServiceStatus struct {
	OK    bool   `json:"ok"`
	Info  string `json:"info,omitempty"`
	Error string `json:"error,omitempty"`
}

type HealthResponse struct {
	EncryptionKey ServiceStatus `json:"encryptionKey"` // key loaded successfully at startup
	DB            ServiceStatus `json:"db"`             // SQLite reachable
	OCI           ServiceStatus `json:"oci"`            // OCI accounts configured for this user
	Backend       ServiceStatus `json:"backend"`        // Pulumi state dir accessible
	Passphrase    ServiceStatus `json:"passphrase"`     // Pulumi stack encryption passphrase set
}

func (h *Handler) GetHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{}

	// Encryption key — always loaded (auto-generated or from env/keystore at startup).
	// If we're handling requests the key is guaranteed to be present.
	resp.EncryptionKey = ServiceStatus{OK: true, Info: "key loaded"}

	// DB — ping the SQLite connection
	if err := h.DB.PingContext(r.Context()); err != nil {
		resp.DB = ServiceStatus{OK: false, Error: err.Error()}
	} else {
		resp.DB = ServiceStatus{OK: true}
	}

	// OCI accounts — count accounts for the authenticated user
	user := auth.UserFromContext(r.Context())
	if user != nil {
		accounts, err := h.Accounts.ListForUser(user.ID)
		if err != nil {
			resp.OCI = ServiceStatus{OK: false, Error: err.Error()}
		} else if len(accounts) == 0 {
			resp.OCI = ServiceStatus{OK: false, Error: "no OCI accounts configured — add one in the Accounts page"}
		} else {
			verified := 0
			for _, a := range accounts {
				if a.Status == "verified" {
					verified++
				}
			}
			resp.OCI = ServiceStatus{OK: true, Info: formatAccountInfo(len(accounts), verified)}
		}
	} else {
		resp.OCI = ServiceStatus{OK: false, Error: "not authenticated"}
	}

	// Passphrase — at least one named passphrase must exist
	if hasAny, err := h.Passphrases.HasAny(); err != nil {
		resp.Passphrase = ServiceStatus{OK: false, Error: err.Error()}
	} else if hasAny {
		resp.Passphrase = ServiceStatus{OK: true}
	} else {
		resp.Passphrase = ServiceStatus{OK: false, Error: "No passphrases configured — create one in Settings before running stack operations"}
	}

	// Backend — check that the Pulumi state backend is accessible
	backendType, _, _ := h.Creds.Get(db.KeyBackendType)
	if backendType == "s3" {
		bucket, _, _ := h.Creds.Get(db.KeyS3Bucket)
		ns, _, _ := h.Creds.Get(db.KeyS3Namespace)
		region, _, _ := h.Creds.Get(db.KeyS3Region)
		if bucket == "" || ns == "" || region == "" {
			resp.Backend = ServiceStatus{OK: false, Error: "S3 backend configured but missing bucket/namespace/region"}
		} else {
			endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
			resp.Backend = ServiceStatus{OK: true, Info: fmt.Sprintf("s3://%s @ %s", bucket, endpoint)}
		}
	} else {
		stateDir := os.Getenv("PULUMI_UI_STATE_DIR")
		if stateDir == "" {
			dataDir := os.Getenv("PULUMI_UI_DATA_DIR")
			if dataDir == "" {
				dataDir = "/data"
			}
			stateDir = dataDir + "/state"
		}
		if _, err := os.Stat(stateDir); err != nil {
			resp.Backend = ServiceStatus{OK: false, Error: "state dir not accessible: " + err.Error()}
		} else {
			resp.Backend = ServiceStatus{OK: true, Info: "file://" + stateDir}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func formatAccountInfo(total, verified int) string {
	if verified == total {
		if total == 1 {
			return "1 account (verified)"
		}
		return formatInt(total) + " accounts (all verified)"
	}
	return formatInt(total) + " account(s), " + formatInt(verified) + " verified"
}

func formatInt(n int) string {
	switch n {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return "many"
	}
}
