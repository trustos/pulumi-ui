package db

import (
	"database/sql"
	"time"
)

type StackRow struct {
	Name               string
	Blueprint          string
	ConfigYAML         string
	OciAccountID       *string
	PassphraseID       *string
	SshKeyID           *string
	CreatedByAccountID *string
	CreatedAt          int64
	UpdatedAt          int64
}

type StackStore struct {
	rdb *sql.DB
	wdb *sql.DB
}

func NewStackStore(p *DBPair) *StackStore {
	return &StackStore{rdb: p.ReadDB, wdb: p.WriteDB}
}

func (s *StackStore) Upsert(name, blueprint, configYAML string, ociAccountID, passphraseID, sshKeyID, createdByAccountID *string) error {
	_, err := s.wdb.Exec(`
		INSERT INTO stacks (name, blueprint, config_yaml, oci_account_id, passphrase_id, ssh_key_id, created_by_account_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			blueprint              = excluded.blueprint,
			config_yaml            = excluded.config_yaml,
			oci_account_id         = excluded.oci_account_id,
			passphrase_id          = excluded.passphrase_id,
			ssh_key_id             = excluded.ssh_key_id,
			created_by_account_id  = COALESCE(stacks.created_by_account_id, excluded.created_by_account_id),
			updated_at             = excluded.updated_at,
			created_at             = CASE WHEN blueprint != excluded.blueprint THEN excluded.updated_at ELSE created_at END
	`, name, blueprint, configYAML, ociAccountID, passphraseID, sshKeyID, createdByAccountID, time.Now().Unix())
	return err
}

func (s *StackStore) Get(name string) (*StackRow, error) {
	row := &StackRow{}
	err := s.rdb.QueryRow(`
		SELECT name, blueprint, config_yaml, oci_account_id, passphrase_id, ssh_key_id, created_by_account_id, created_at, updated_at
		FROM stacks WHERE name = ?
	`, name).Scan(&row.Name, &row.Blueprint, &row.ConfigYAML, &row.OciAccountID, &row.PassphraseID, &row.SshKeyID, &row.CreatedByAccountID, &row.CreatedAt, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return row, err
}

func (s *StackStore) List() ([]StackRow, error) {
	rows, err := s.rdb.Query(`
		SELECT name, blueprint, config_yaml, oci_account_id, passphrase_id, ssh_key_id, created_by_account_id, created_at, updated_at
		FROM stacks ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []StackRow
	for rows.Next() {
		var r StackRow
		rows.Scan(&r.Name, &r.Blueprint, &r.ConfigYAML, &r.OciAccountID, &r.PassphraseID, &r.SshKeyID, &r.CreatedByAccountID, &r.CreatedAt, &r.UpdatedAt)
		result = append(result, r)
	}
	return result, nil
}

func (s *StackStore) Delete(name string) error {
	_, err := s.wdb.Exec(`DELETE FROM stacks WHERE name = ?`, name)
	return err
}

func (s *StackStore) CountByPassphrase(id string) (int, error) {
	var count int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM stacks WHERE passphrase_id = ?`, id).Scan(&count)
	return count, err
}

func (s *StackStore) CountBySSHKey(id string) (int, error) {
	var count int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM stacks WHERE ssh_key_id = ?`, id).Scan(&count)
	return count, err
}
