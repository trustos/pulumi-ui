package keystore

import (
	"fmt"
	"os"
)

// New returns a KeyStore based on the PULUMI_UI_KEY_STORE environment variable.
//
//	PULUMI_UI_KEY_STORE=file   (default)
//	  PULUMI_UI_KEY_FILE        — path to key file (default: dataDir/encryption.key)
//
//	PULUMI_UI_KEY_STORE=consul
//	  PULUMI_UI_CONSUL_ADDR     — Consul address (default: http://127.0.0.1:8500, also reads CONSUL_HTTP_ADDR)
//	  PULUMI_UI_CONSUL_TOKEN    — ACL token (optional, also reads CONSUL_HTTP_TOKEN)
//	  PULUMI_UI_CONSUL_KEY_PATH — KV path (default: pulumi-ui/encryption-key)
func New(dataDir string) (KeyStore, error) {
	backend := envOr("PULUMI_UI_KEY_STORE", "file")
	switch backend {
	case "file":
		path := envOr("PULUMI_UI_KEY_FILE", dataDir+"/encryption.key")
		return NewFileStore(path), nil

	case "consul":
		addr := envOr("PULUMI_UI_CONSUL_ADDR", envOr("CONSUL_HTTP_ADDR", ""))
		token := envOr("PULUMI_UI_CONSUL_TOKEN", envOr("CONSUL_HTTP_TOKEN", ""))
		keyPath := envOr("PULUMI_UI_CONSUL_KEY_PATH", "pulumi-ui/encryption-key")
		return NewConsulStore(addr, token, keyPath), nil

	default:
		return nil, fmt.Errorf("unknown PULUMI_UI_KEY_STORE %q — valid values: file, consul", backend)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
