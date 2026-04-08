package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/trustos/pulumi-ui/internal/crypto"
)

type PassphraseRow struct {
	ID         string
	Name       string
	StackCount int
	CreatedAt  int64
}

type PassphraseStore struct {
	rdb *sql.DB
	wdb *ResilientWriter
	enc *crypto.Encryptor
}

func NewPassphraseStore(p *DBPair, enc *crypto.Encryptor) *PassphraseStore {
	return &PassphraseStore{rdb: p.ReadDB, wdb: p.WriteDB, enc: enc}
}

func (s *PassphraseStore) Create(name, value string) (*PassphraseRow, error) {
	ciphertext, err := s.enc.Encrypt(value)
	if err != nil {
		return nil, fmt.Errorf("encrypt passphrase: %w", err)
	}
	id := uuid.New().String()
	now := time.Now().Unix()
	_, err = s.wdb.Exec(
		`INSERT INTO passphrases (id, name, value, created_at) VALUES (?, ?, ?, ?)`,
		id, name, ciphertext, now,
	)
	if err != nil {
		return nil, err
	}
	return &PassphraseRow{ID: id, Name: name, CreatedAt: now}, nil
}

func (s *PassphraseStore) List() ([]PassphraseRow, error) {
	rows, err := s.rdb.Query(`
		SELECT p.id, p.name, p.created_at, COUNT(st.name) AS stack_count
		FROM passphrases p
		LEFT JOIN stacks st ON st.passphrase_id = p.id
		GROUP BY p.id
		ORDER BY p.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PassphraseRow
	for rows.Next() {
		var r PassphraseRow
		rows.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.StackCount)
		result = append(result, r)
	}
	return result, nil
}

func (s *PassphraseStore) GetValue(id string) (string, error) {
	var ciphertext []byte
	err := s.rdb.QueryRow(`SELECT value FROM passphrases WHERE id = ?`, id).Scan(&ciphertext)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("passphrase not found")
	}
	if err != nil {
		return "", err
	}
	return s.enc.Decrypt(ciphertext)
}

func (s *PassphraseStore) Rename(id, newName string) error {
	res, err := s.wdb.Exec(`UPDATE passphrases SET name = ? WHERE id = ?`, newName, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("passphrase not found")
	}
	return nil
}

func (s *PassphraseStore) Delete(id string) error {
	var count int
	s.rdb.QueryRow(`SELECT COUNT(*) FROM stacks WHERE passphrase_id = ?`, id).Scan(&count)
	if count > 0 {
		return fmt.Errorf("passphrase is used by %d stack(s) — remove those stacks first", count)
	}
	_, err := s.wdb.Exec(`DELETE FROM passphrases WHERE id = ?`, id)
	return err
}

func (s *PassphraseStore) HasAny() (bool, error) {
	var count int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM passphrases`).Scan(&count)
	return count > 0, err
}
