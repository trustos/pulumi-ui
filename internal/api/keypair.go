package api

import (
	"crypto/md5" //nolint:gosec // OCI fingerprint spec requires MD5
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"

	"golang.org/x/crypto/ssh"
)

// GenerateKeyPair generates a 2048-bit RSA key pair and returns:
//   - privateKey: PKCS1 PEM private key (for OCI key_file and Pulumi UI private key field)
//   - publicKeyPem: PKIX PEM public key (upload to OCI Console → API Keys → Add API Key)
//   - fingerprint: MD5 fingerprint in OCI format (aa:bb:cc:...) — auto-fill the fingerprint field
//   - sshPublicKey: OpenSSH public key (for SSH access to instances)
func (h *Handler) GenerateKeyPair(w http.ResponseWriter, r *http.Request) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		http.Error(w, "failed to generate key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Private key — PKCS1 PEM
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	// Public key — PKIX PEM (what OCI expects when you paste a key)
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		http.Error(w, "marshal public key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	// OCI fingerprint: MD5 of the DER-encoded public key, colon-separated hex
	//nolint:gosec // required by OCI fingerprint specification
	sum := md5.Sum(pubDER)
	fingerprint := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x",
		sum[0], sum[1], sum[2], sum[3], sum[4], sum[5], sum[6], sum[7],
		sum[8], sum[9], sum[10], sum[11], sum[12], sum[13], sum[14], sum[15])

	// SSH public key (OpenSSH wire format, base64-encoded)
	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		http.Error(w, "marshal SSH public key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sshPubStr := string(ssh.MarshalAuthorizedKey(sshPub))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"privateKey":   string(privPEM),
		"publicKeyPem": string(pubPEM),
		"fingerprint":  fingerprint,
		"sshPublicKey": sshPubStr,
	})
}
