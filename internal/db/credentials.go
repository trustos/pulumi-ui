package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/trustos/pulumi-ui/internal/crypto"
)

// Known credential keys
const (
	KeyOCITenancyOCID    = "oci/tenancy-ocid"
	KeyOCIUserOCID       = "oci/user-ocid"
	KeyOCIFingerprint    = "oci/fingerprint"
	KeyOCIPrivateKey     = "oci/private-key"
	KeyOCIRegion         = "oci/region"
	KeySSHPublicKey      = "ssh/public-key"
	KeyPulumiPassphrase  = "pulumi/passphrase"
	KeyS3Namespace       = "s3/namespace"
	KeyS3Bucket          = "s3/bucket"
	KeyS3AccessKeyID     = "s3/access-key-id"
	KeyS3SecretAccessKey = "s3/secret-access-key"
	KeyBackendType       = "config/backend-type"
)

// AllCredentialKeys are the keys shown on the Settings page.
// OCI credentials are no longer stored here — they live in the oci_accounts table.
var AllCredentialKeys = []string{
	KeyPulumiPassphrase, KeyS3Namespace, KeyS3Bucket,
	KeyS3AccessKeyID, KeyS3SecretAccessKey, KeyBackendType,
}

type CredentialStore struct {
	db  *sql.DB
	enc *crypto.Encryptor
}

func NewCredentialStore(db *sql.DB, enc *crypto.Encryptor) *CredentialStore {
	return &CredentialStore{db: db, enc: enc}
}

func (s *CredentialStore) Set(key, value string) error {
	ciphertext, err := s.enc.Encrypt(value)
	if err != nil {
		return fmt.Errorf("encrypt %s: %w", key, err)
	}
	_, err = s.db.Exec(`
		INSERT INTO credentials (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, ciphertext, time.Now().Unix())
	return err
}

func (s *CredentialStore) Get(key string) (string, bool, error) {
	var ciphertext []byte
	err := s.db.QueryRow(`SELECT value FROM credentials WHERE key = ?`, key).Scan(&ciphertext)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plaintext, err := s.enc.Decrypt(ciphertext)
	if err != nil {
		return "", false, err
	}
	return plaintext, true, nil
}

func (s *CredentialStore) GetRequired(key string) (string, error) {
	v, ok, err := s.Get(key)
	if err != nil {
		return "", err
	}
	if !ok || v == "" {
		return "", fmt.Errorf("credential %q is not set", key)
	}
	return v, nil
}

// CredentialStatus is the safe, masked view returned to the API (never raw values).
type CredentialStatus struct {
	Key     string  `json:"key"`
	IsSet   bool    `json:"isSet"`
	Preview *string `json:"preview"`
}

func (s *CredentialStore) Status() ([]CredentialStatus, error) {
	result := make([]CredentialStatus, 0, len(AllCredentialKeys))
	for _, key := range AllCredentialKeys {
		v, ok, err := s.Get(key)
		if err != nil {
			return nil, err
		}
		var preview *string
		if ok && v != "" {
			p := maskValue(v)
			preview = &p
		}
		result = append(result, CredentialStatus{Key: key, IsSet: ok && v != "", Preview: preview})
	}
	return result, nil
}

func maskValue(v string) string {
	if len(v) <= 8 {
		return "***"
	}
	return v[:12] + "..." + v[len(v)-3:]
}

// OCICredentials bundles all OCI fields — used by engine to build workspace options.
type OCICredentials struct {
	TenancyOCID  string
	UserOCID     string
	Fingerprint  string
	PrivateKey   string
	Region       string
	SSHPublicKey string
}

func (s *CredentialStore) GetOCICredentials() (OCICredentials, error) {
	var c OCICredentials
	var err error
	fields := map[string]*string{
		KeyOCITenancyOCID: &c.TenancyOCID,
		KeyOCIUserOCID:    &c.UserOCID,
		KeyOCIFingerprint: &c.Fingerprint,
		KeyOCIPrivateKey:  &c.PrivateKey,
		KeyOCIRegion:      &c.Region,
		KeySSHPublicKey:   &c.SSHPublicKey,
	}
	for key, dst := range fields {
		*dst, err = s.GetRequired(key)
		if err != nil {
			return OCICredentials{}, fmt.Errorf("missing OCI credential: %w", err)
		}
	}
	return c, nil
}
