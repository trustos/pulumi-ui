package db

import (
	"database/sql"
	"fmt"

	"github.com/trustos/pulumi-ui/internal/crypto"
)

// NodeCert holds the Nebula identity for one OCI instance within a stack.
type NodeCert struct {
	StackName   string
	NodeIndex   int
	NebulaCert  []byte  // PEM, plaintext
	NebulaKey   []byte  // PEM, plaintext (decrypted on read)
	NebulaIP    string  // e.g. "10.42.1.2/24"
	AgentRealIP *string // public IP discovered after deploy
}

// NodeCertStore manages per-node Nebula certificates in stack_node_certs.
type NodeCertStore struct {
	rdb *sql.DB
	wdb *sql.DB
	enc *crypto.Encryptor
}

func NewNodeCertStore(p *DBPair, enc *crypto.Encryptor) *NodeCertStore {
	return &NodeCertStore{rdb: p.ReadDB, wdb: p.WriteDB, enc: enc}
}

// CreateAll inserts node certs for a stack in a single transaction.
// The nebula_key is AES-GCM encrypted before storage.
func (s *NodeCertStore) CreateAll(certs []*NodeCert) error {
	tx, err := s.wdb.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO stack_node_certs (stack_name, node_index, nebula_cert, nebula_key, nebula_ip)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, c := range certs {
		encKey, err := s.enc.EncryptBytes(c.NebulaKey)
		if err != nil {
			return fmt.Errorf("encrypt nebula key for node %d: %w", c.NodeIndex, err)
		}
		if _, err := stmt.Exec(c.StackName, c.NodeIndex, c.NebulaCert, encKey, c.NebulaIP); err != nil {
			return fmt.Errorf("insert node cert %d: %w", c.NodeIndex, err)
		}
	}

	return tx.Commit()
}

// ListForStack returns all node certs for a stack, sorted by node_index ascending.
func (s *NodeCertStore) ListForStack(stackName string) ([]*NodeCert, error) {
	rows, err := s.rdb.Query(`
		SELECT stack_name, node_index, nebula_cert, nebula_key, nebula_ip, agent_real_ip
		FROM stack_node_certs
		WHERE stack_name = ?
		ORDER BY node_index ASC
	`, stackName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var certs []*NodeCert
	for rows.Next() {
		var c NodeCert
		var encKey []byte
		if err := rows.Scan(&c.StackName, &c.NodeIndex, &c.NebulaCert, &encKey, &c.NebulaIP, &c.AgentRealIP); err != nil {
			return nil, err
		}
		c.NebulaKey, err = s.enc.DecryptBytes(encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt nebula key for node %d: %w", c.NodeIndex, err)
		}
		certs = append(certs, &c)
	}
	return certs, rows.Err()
}

// UpdateAgentRealIP stores the public IP discovered for a specific node after deploy.
func (s *NodeCertStore) UpdateAgentRealIP(stackName string, nodeIndex int, realIP string) error {
	_, err := s.wdb.Exec(`
		UPDATE stack_node_certs SET agent_real_ip = ? WHERE stack_name = ? AND node_index = ?
	`, realIP, stackName, nodeIndex)
	return err
}

// Delete removes all node certs for a stack (called on stack delete).
func (s *NodeCertStore) Delete(stackName string) error {
	_, err := s.wdb.Exec(`DELETE FROM stack_node_certs WHERE stack_name = ?`, stackName)
	return err
}
