package keystore

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ConsulStore persists the encryption key in a Consul KV path.
// It uses the Consul HTTP API directly — no external client library needed.
//
// Configuration (all optional, sensible defaults):
//
//	addr     — Consul HTTP address, e.g. "http://127.0.0.1:8500"
//	token    — Consul ACL token (may be empty if ACLs are disabled)
//	keyPath  — KV path, e.g. "pulumi-ui/encryption-key"
type ConsulStore struct {
	addr    string
	token   string
	keyPath string
	client  *http.Client
}

// NewConsulStore constructs a ConsulStore.
func NewConsulStore(addr, token, keyPath string) *ConsulStore {
	if addr == "" {
		addr = "http://127.0.0.1:8500"
	}
	if keyPath == "" {
		keyPath = "pulumi-ui/encryption-key"
	}
	return &ConsulStore{
		addr:    strings.TrimRight(addr, "/"),
		token:   token,
		keyPath: keyPath,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Load fetches the key from Consul KV. Returns ("", nil) if the path does not exist.
func (s *ConsulStore) Load() (string, error) {
	url := fmt.Sprintf("%s/v1/kv/%s?raw", s.addr, s.keyPath)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if s.token != "" {
		req.Header.Set("X-Consul-Token", s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("consul GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("consul GET %s: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(body))
	if key == "" {
		return "", nil
	}
	return key, validateHexKey(key)
}

// Save writes the key to Consul KV.
func (s *ConsulStore) Save(hexKey string) error {
	url := fmt.Sprintf("%s/v1/kv/%s", s.addr, s.keyPath)
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(hexKey))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	if s.token != "" {
		req.Header.Set("X-Consul-Token", s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("consul PUT %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul PUT %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

func (s *ConsulStore) Description() string {
	return fmt.Sprintf("consul:%s/%s", s.addr, s.keyPath)
}

// consulKVResponse is used only when the ?raw query param is NOT used (JSON mode).
// Kept here for reference; the store uses ?raw for simplicity.
type consulKVResponse struct {
	Value string `json:"Value"` // base64-encoded
}

func decodeConsulValue(b64 string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// consulKVResponses is used only when reading the full JSON array response.
func parseConsulKVJSON(body []byte) (string, error) {
	var items []consulKVResponse
	if err := json.Unmarshal(body, &items); err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", nil
	}
	return decodeConsulValue(items[0].Value)
}
