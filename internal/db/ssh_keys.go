package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/trustos/pulumi-ui/internal/crypto"
)

// SSHKey represents a stored SSH key pair (private key encrypted, may be absent).
type SSHKey struct {
	ID            string
	UserID        string
	Name          string
	PublicKey     string
	HasPrivateKey bool
	StackCount    int
	CreatedAt     int64
}

type SSHKeyStore struct {
	rdb *sql.DB
	wdb *sql.DB
	enc *crypto.Encryptor
}

func NewSSHKeyStore(p *DBPair, enc *crypto.Encryptor) *SSHKeyStore {
	return &SSHKeyStore{rdb: p.ReadDB, wdb: p.WriteDB, enc: enc}
}

// Create stores a new SSH key. privateKey may be empty (public-key-only entry).
func (s *SSHKeyStore) Create(userID, name, publicKey, privateKey string) (*SSHKey, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	var encPriv []byte
	if privateKey != "" {
		var err error
		encPriv, err = s.enc.Encrypt(privateKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt private key: %w", err)
		}
	}

	_, err := s.wdb.Exec(
		`INSERT INTO ssh_keys (id, user_id, name, public_key, private_key, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, name, publicKey, encPriv, now,
	)
	if err != nil {
		return nil, err
	}
	return &SSHKey{
		ID: id, UserID: userID, Name: name, PublicKey: publicKey,
		HasPrivateKey: privateKey != "", CreatedAt: now,
	}, nil
}

// List returns all SSH keys for a user, including how many stacks reference each.
func (s *SSHKeyStore) List(userID string) ([]SSHKey, error) {
	rows, err := s.rdb.Query(`
		SELECT k.id, k.user_id, k.name, k.public_key,
		       (k.private_key IS NOT NULL) AS has_private_key,
		       COUNT(st.name) AS stack_count, k.created_at
		FROM ssh_keys k
		LEFT JOIN stacks st ON st.ssh_key_id = k.id
		WHERE k.user_id = ?
		GROUP BY k.id
		ORDER BY k.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SSHKey
	for rows.Next() {
		var k SSHKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.PublicKey, &k.HasPrivateKey, &k.StackCount, &k.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, k)
	}
	return result, nil
}

// GetByID returns key metadata (without private key content).
func (s *SSHKeyStore) GetByID(id string) (*SSHKey, error) {
	var k SSHKey
	err := s.rdb.QueryRow(`
		SELECT id, user_id, name, public_key, (private_key IS NOT NULL)
		FROM ssh_keys WHERE id = ?`, id,
	).Scan(&k.ID, &k.UserID, &k.Name, &k.PublicKey, &k.HasPrivateKey)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &k, err
}

// GetPublicKey returns the OpenSSH public key for a given SSH key ID.
func (s *SSHKeyStore) GetPublicKey(id string) (string, error) {
	var pub string
	err := s.rdb.QueryRow(`SELECT public_key FROM ssh_keys WHERE id = ?`, id).Scan(&pub)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("SSH key not found")
	}
	return pub, err
}

// GetPrivateKey decrypts and returns the stored private key for download.
func (s *SSHKeyStore) GetPrivateKey(id string) (name, privateKey string, err error) {
	var encPriv []byte
	err = s.rdb.QueryRow(`SELECT name, private_key FROM ssh_keys WHERE id = ?`, id).Scan(&name, &encPriv)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("SSH key not found")
	}
	if err != nil {
		return "", "", err
	}
	if encPriv == nil {
		return "", "", fmt.Errorf("no private key stored for this SSH key")
	}
	privateKey, err = s.enc.Decrypt(encPriv)
	return
}

// Delete removes an SSH key, refusing if any stacks still reference it.
func (s *SSHKeyStore) Delete(id string) error {
	var count int
	s.rdb.QueryRow(`SELECT COUNT(*) FROM stacks WHERE ssh_key_id = ?`, id).Scan(&count)
	if count > 0 {
		return fmt.Errorf("SSH key is used by %d stack(s) — unlink it from those stacks first", count)
	}
	_, err := s.wdb.Exec(`DELETE FROM ssh_keys WHERE id = ?`, id)
	return err
}
