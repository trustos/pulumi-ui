package db

import (
	"database/sql"
	"time"
)

type StackRow struct {
	Name         string
	Program      string
	ConfigYAML   string
	OciAccountID *string
	PassphraseID *string
	CreatedAt    int64
	UpdatedAt    int64
}

type StackStore struct {
	db *sql.DB
}

func NewStackStore(db *sql.DB) *StackStore {
	return &StackStore{db: db}
}

func (s *StackStore) Upsert(name, program, configYAML string, ociAccountID, passphraseID *string) error {
	_, err := s.db.Exec(`
		INSERT INTO stacks (name, program, config_yaml, oci_account_id, passphrase_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			program        = excluded.program,
			config_yaml    = excluded.config_yaml,
			oci_account_id = excluded.oci_account_id,
			passphrase_id  = excluded.passphrase_id,
			updated_at     = excluded.updated_at
	`, name, program, configYAML, ociAccountID, passphraseID, time.Now().Unix())
	return err
}

func (s *StackStore) Get(name string) (*StackRow, error) {
	row := &StackRow{}
	err := s.db.QueryRow(`
		SELECT name, program, config_yaml, oci_account_id, passphrase_id, created_at, updated_at
		FROM stacks WHERE name = ?
	`, name).Scan(&row.Name, &row.Program, &row.ConfigYAML, &row.OciAccountID, &row.PassphraseID, &row.CreatedAt, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return row, err
}

func (s *StackStore) List() ([]StackRow, error) {
	rows, err := s.db.Query(`
		SELECT name, program, config_yaml, oci_account_id, passphrase_id, created_at, updated_at
		FROM stacks ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []StackRow
	for rows.Next() {
		var r StackRow
		rows.Scan(&r.Name, &r.Program, &r.ConfigYAML, &r.OciAccountID, &r.PassphraseID, &r.CreatedAt, &r.UpdatedAt)
		result = append(result, r)
	}
	return result, nil
}

func (s *StackStore) Delete(name string) error {
	_, err := s.db.Exec(`DELETE FROM stacks WHERE name = ?`, name)
	return err
}
