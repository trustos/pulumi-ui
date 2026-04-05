package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/trustos/pulumi-ui/internal/crypto"
)

// OCIAccount represents a stored OCI account with decrypted credential fields.
type OCIAccount struct {
	ID           string
	UserID       string
	Name         string
	TenancyName  string
	TenancyOCID  string
	Region       string
	UserOCID     string
	Fingerprint  string
	PrivateKey   string
	SSHPublicKey string
	Status       string
	VerifiedAt   *int64
	CreatedAt    int64
	StackCount   int // populated by ListForUser only
}

// ToOCICredentials converts the account to the OCICredentials bundle used by the engine.
func (a *OCIAccount) ToOCICredentials() OCICredentials {
	return OCICredentials{
		TenancyOCID:  a.TenancyOCID,
		UserOCID:     a.UserOCID,
		Fingerprint:  a.Fingerprint,
		PrivateKey:   a.PrivateKey,
		Region:       a.Region,
		SSHPublicKey: a.SSHPublicKey,
	}
}

type AccountStore struct {
	rdb *sql.DB
	wdb *sql.DB
	enc *crypto.Encryptor
}

func NewAccountStore(p *DBPair, enc *crypto.Encryptor) *AccountStore {
	return &AccountStore{rdb: p.ReadDB, wdb: p.WriteDB, enc: enc}
}

// Create stores a new OCI account for the given user.
func (s *AccountStore) Create(userID, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey string) (*OCIAccount, error) {
	encUserOCID, err := s.enc.Encrypt(userOCID)
	if err != nil {
		return nil, fmt.Errorf("encrypt user_ocid: %w", err)
	}
	encFingerprint, err := s.enc.Encrypt(fingerprint)
	if err != nil {
		return nil, fmt.Errorf("encrypt fingerprint: %w", err)
	}
	encPrivateKey, err := s.enc.Encrypt(privateKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt private_key: %w", err)
	}
	encSSHPublicKey, err := s.enc.Encrypt(sshPublicKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt ssh_public_key: %w", err)
	}

	id := uuid.New().String()
	now := time.Now().Unix()
	_, err = s.wdb.Exec(`
		INSERT INTO oci_accounts (id, user_id, name, tenancy_name, tenancy_ocid, region, user_ocid, fingerprint, private_key, ssh_public_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, name, tenancyName, tenancyOCID, region, encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey, now,
	)
	if err != nil {
		return nil, err
	}
	return &OCIAccount{
		ID: id, UserID: userID, Name: name, TenancyName: tenancyName, TenancyOCID: tenancyOCID, Region: region,
		UserOCID: userOCID, Fingerprint: fingerprint, PrivateKey: privateKey, SSHPublicKey: sshPublicKey,
		Status: "unverified", CreatedAt: now,
	}, nil
}

func (s *AccountStore) Get(id string) (*OCIAccount, error) {
	a := &OCIAccount{}
	var encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey []byte
	err := s.rdb.QueryRow(`
		SELECT id, user_id, name, tenancy_name, tenancy_ocid, region, user_ocid, fingerprint, private_key, ssh_public_key, status, verified_at, created_at
		FROM oci_accounts WHERE id = ?`, id,
	).Scan(&a.ID, &a.UserID, &a.Name, &a.TenancyName, &a.TenancyOCID, &a.Region,
		&encUserOCID, &encFingerprint, &encPrivateKey, &encSSHPublicKey,
		&a.Status, &a.VerifiedAt, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.decrypt(a, encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey)
}

func (s *AccountStore) ListForUser(userID string) ([]OCIAccount, error) {
	rows, err := s.rdb.Query(`
		SELECT a.id, a.user_id, a.name, a.tenancy_name, a.tenancy_ocid, a.region,
		       a.user_ocid, a.fingerprint, a.private_key, a.ssh_public_key,
		       a.status, a.verified_at, a.created_at,
		       COUNT(s.name) AS stack_count
		FROM oci_accounts a
		LEFT JOIN stacks s ON s.oci_account_id = a.id
		WHERE a.user_id = ?
		GROUP BY a.id
		ORDER BY a.created_at`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []OCIAccount
	for rows.Next() {
		a := &OCIAccount{}
		var encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.TenancyName, &a.TenancyOCID, &a.Region,
			&encUserOCID, &encFingerprint, &encPrivateKey, &encSSHPublicKey,
			&a.Status, &a.VerifiedAt, &a.CreatedAt, &a.StackCount); err != nil {
			return nil, err
		}
		dec, err := s.decrypt(a, encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey)
		if err != nil {
			return nil, err
		}
		result = append(result, *dec)
	}
	return result, nil
}

func (s *AccountStore) Update(id, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey string) error {
	encUserOCID, err := s.enc.Encrypt(userOCID)
	if err != nil {
		return err
	}
	encFingerprint, err := s.enc.Encrypt(fingerprint)
	if err != nil {
		return err
	}
	encPrivateKey, err := s.enc.Encrypt(privateKey)
	if err != nil {
		return err
	}
	encSSHPublicKey, err := s.enc.Encrypt(sshPublicKey)
	if err != nil {
		return err
	}
	_, err = s.wdb.Exec(`
		UPDATE oci_accounts SET name=?, tenancy_name=?, tenancy_ocid=?, region=?, user_ocid=?, fingerprint=?, private_key=?, ssh_public_key=?, status='unverified', verified_at=NULL
		WHERE id=?`,
		name, tenancyName, tenancyOCID, region, encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey, id,
	)
	return err
}

// UpdatePartial updates account fields. If privateKey or sshPublicKey is empty,
// the existing encrypted values are preserved.
func (s *AccountStore) UpdatePartial(id, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey string) error {
	encUserOCID, err := s.enc.Encrypt(userOCID)
	if err != nil {
		return err
	}
	encFingerprint, err := s.enc.Encrypt(fingerprint)
	if err != nil {
		return err
	}

	if privateKey == "" && sshPublicKey == "" {
		// Skip key updates entirely
		_, err = s.wdb.Exec(`
			UPDATE oci_accounts SET name=?, tenancy_name=?, tenancy_ocid=?, region=?, user_ocid=?, fingerprint=?, status='unverified', verified_at=NULL
			WHERE id=?`,
			name, tenancyName, tenancyOCID, region, encUserOCID, encFingerprint, id,
		)
		return err
	}

	if privateKey == "" || sshPublicKey == "" {
		// Need the existing encrypted values for the fields we're keeping
		existing, err := s.Get(id)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("account not found")
		}
		if privateKey == "" {
			privateKey = existing.PrivateKey
		}
		if sshPublicKey == "" {
			sshPublicKey = existing.SSHPublicKey
		}
	}

	return s.Update(id, name, tenancyName, tenancyOCID, region, userOCID, fingerprint, privateKey, sshPublicKey)
}

func (s *AccountStore) SetStatus(id, status string, verifiedAt *int64) error {
	_, err := s.wdb.Exec(`UPDATE oci_accounts SET status=?, verified_at=? WHERE id=?`, status, verifiedAt, id)
	return err
}

func (s *AccountStore) SetTenancyName(id, name string) error {
	_, err := s.wdb.Exec(`UPDATE oci_accounts SET tenancy_name=? WHERE id=?`, name, id)
	return err
}

// CountStacks returns the number of stacks that reference this account.
func (s *AccountStore) CountStacks(id string) (int, error) {
	var count int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM stacks WHERE oci_account_id = ?`, id).Scan(&count)
	return count, err
}

func (s *AccountStore) Delete(id string) error {
	_, err := s.wdb.Exec(`DELETE FROM oci_accounts WHERE id = ?`, id)
	return err
}

func (s *AccountStore) decrypt(a *OCIAccount, encUserOCID, encFingerprint, encPrivateKey, encSSHPublicKey []byte) (*OCIAccount, error) {
	var err error
	if a.UserOCID, err = s.enc.Decrypt(encUserOCID); err != nil {
		return nil, fmt.Errorf("decrypt user_ocid: %w", err)
	}
	if a.Fingerprint, err = s.enc.Decrypt(encFingerprint); err != nil {
		return nil, fmt.Errorf("decrypt fingerprint: %w", err)
	}
	if a.PrivateKey, err = s.enc.Decrypt(encPrivateKey); err != nil {
		return nil, fmt.Errorf("decrypt private_key: %w", err)
	}
	if a.SSHPublicKey, err = s.enc.Decrypt(encSSHPublicKey); err != nil {
		return nil, fmt.Errorf("decrypt ssh_public_key: %w", err)
	}
	return a, nil
}
