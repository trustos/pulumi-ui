package api

import (
	"archive/zip"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/trustos/pulumi-ui/internal/auth"
)

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// ExportAccounts handles GET /api/accounts/export.
// Returns a ZIP archive containing:
//   - config        — OCI SDK config file (INI format, one profile per account)
//   - {name}_key.pem — RSA private key for each account
func (h *Handler) ExportAccounts(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	accounts, err := h.Accounts.ListForUser(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build the OCI config file content and collect key file names
	type keyEntry struct {
		filename string
		pem      string
	}
	keys := make([]keyEntry, 0, len(accounts))

	var configLines strings.Builder
	first := true
	for _, a := range accounts {
		profileName := sanitizeName(a.Name)
		keyFilename := profileName + "_key.pem"

		keys = append(keys, keyEntry{filename: keyFilename, pem: a.PrivateKey})

		section := profileName
		if first {
			section = "DEFAULT"
			first = false
		}

		configLines.WriteString(fmt.Sprintf("[%s]\n", section))
		configLines.WriteString(fmt.Sprintf("user=%s\n", a.UserOCID))
		configLines.WriteString(fmt.Sprintf("fingerprint=%s\n", a.Fingerprint))
		configLines.WriteString(fmt.Sprintf("tenancy=%s\n", a.TenancyOCID))
		configLines.WriteString(fmt.Sprintf("region=%s\n", a.Region))
		configLines.WriteString(fmt.Sprintf("key_file=./%s\n", keyFilename))
		configLines.WriteString("\n")
	}

	ts := time.Now().Format("20060102-150405")
	filename := "oci-config-" + ts + ".zip"

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Write config file
	cf, err := zw.Create("config")
	if err != nil {
		return
	}
	cf.Write([]byte(configLines.String()))

	// Write each key file
	for _, ke := range keys {
		kf, err := zw.Create(ke.filename)
		if err != nil {
			continue
		}
		kf.Write([]byte(ke.pem))
	}
}

func sanitizeName(name string) string {
	s := nonAlnum.ReplaceAllString(name, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "account"
	}
	return strings.ToLower(s)
}
