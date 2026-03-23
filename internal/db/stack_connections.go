package db

import (
	"database/sql"
	"fmt"

	"github.com/trustos/pulumi-ui/internal/crypto"
)

// StackConnection holds the Nebula mesh state for a deployed stack.
type StackConnection struct {
	StackName     string
	NebulaCACert  []byte  // PEM, plaintext
	NebulaCAKey   []byte  // PEM, plaintext (decrypted on read)
	NebulaUICert  []byte  // PEM, plaintext
	NebulaUIKey   []byte  // PEM, plaintext (decrypted on read)
	NebulaSubnet  string  // e.g. "10.42.1.0/24"
	LighthouseAddr *string
	AgentNebulaIP  *string
	ConnectedAt   int64
	LastSeenAt    *int64
	ClusterInfo   *string // JSON
}

type StackConnectionStore struct {
	db  *sql.DB
	enc *crypto.Encryptor
}

func NewStackConnectionStore(db *sql.DB, enc *crypto.Encryptor) *StackConnectionStore {
	return &StackConnectionStore{db: db, enc: enc}
}

// Create inserts a new stack connection with Nebula PKI material.
// The CA key and UI key are AES-GCM encrypted before storage.
func (s *StackConnectionStore) Create(conn *StackConnection) error {
	encCAKey, err := s.enc.EncryptBytes(conn.NebulaCAKey)
	if err != nil {
		return fmt.Errorf("encrypt nebula CA key: %w", err)
	}
	encUIKey, err := s.enc.EncryptBytes(conn.NebulaUIKey)
	if err != nil {
		return fmt.Errorf("encrypt nebula UI key: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO stack_connections
			(stack_name, nebula_ca_cert, nebula_ca_key, nebula_ui_cert, nebula_ui_key, nebula_subnet)
		VALUES (?, ?, ?, ?, ?, ?)
	`, conn.StackName, conn.NebulaCACert, encCAKey, conn.NebulaUICert, encUIKey, conn.NebulaSubnet)
	return err
}

// Get returns the stack connection for the given stack, or nil if not found.
func (s *StackConnectionStore) Get(stackName string) (*StackConnection, error) {
	var conn StackConnection
	var encCAKey, encUIKey []byte

	err := s.db.QueryRow(`
		SELECT stack_name, nebula_ca_cert, nebula_ca_key, nebula_ui_cert, nebula_ui_key,
		       nebula_subnet, lighthouse_addr, agent_nebula_ip, connected_at, last_seen_at, cluster_info
		FROM stack_connections WHERE stack_name = ?
	`, stackName).Scan(
		&conn.StackName, &conn.NebulaCACert, &encCAKey, &conn.NebulaUICert, &encUIKey,
		&conn.NebulaSubnet, &conn.LighthouseAddr, &conn.AgentNebulaIP,
		&conn.ConnectedAt, &conn.LastSeenAt, &conn.ClusterInfo,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	conn.NebulaCAKey, err = s.enc.DecryptBytes(encCAKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt nebula CA key: %w", err)
	}
	conn.NebulaUIKey, err = s.enc.DecryptBytes(encUIKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt nebula UI key: %w", err)
	}
	return &conn, nil
}

// UpdateLighthouse sets the lighthouse address after infrastructure deploy.
func (s *StackConnectionStore) UpdateLighthouse(stackName, addr string) error {
	_, err := s.db.Exec(`
		UPDATE stack_connections SET lighthouse_addr = ? WHERE stack_name = ?
	`, addr, stackName)
	return err
}

// UpdateAgentConnected records agent mesh IP and refreshes timestamps.
func (s *StackConnectionStore) UpdateAgentConnected(stackName, nebulaIP, clusterInfo string) error {
	_, err := s.db.Exec(`
		UPDATE stack_connections
		SET agent_nebula_ip = ?, last_seen_at = unixepoch(), cluster_info = ?
		WHERE stack_name = ?
	`, nebulaIP, clusterInfo, stackName)
	return err
}

// UpdateLastSeen refreshes the last_seen_at timestamp (called by periodic health checks).
func (s *StackConnectionStore) UpdateLastSeen(stackName string) error {
	_, err := s.db.Exec(`
		UPDATE stack_connections SET last_seen_at = unixepoch() WHERE stack_name = ?
	`, stackName)
	return err
}

// Delete removes the stack connection.
func (s *StackConnectionStore) Delete(stackName string) error {
	_, err := s.db.Exec(`DELETE FROM stack_connections WHERE stack_name = ?`, stackName)
	return err
}

// AllocateSubnet atomically assigns the next /24 from the 10.42.0.0/8 range.
// Returns a subnet string like "10.42.1.0/24".
func (s *StackConnectionStore) AllocateSubnet() (string, error) {
	var idx int
	err := s.db.QueryRow(`
		UPDATE nebula_subnet_counter SET next = next + 1 WHERE id = 1 RETURNING next - 1
	`).Scan(&idx)
	if err != nil {
		return "", fmt.Errorf("allocate nebula subnet: %w", err)
	}
	if idx < 1 || idx > 65535 {
		return "", fmt.Errorf("nebula subnet index %d out of range [1, 65535]", idx)
	}
	// index n → 10.42.{n/256}.{n%256}.0/24
	return fmt.Sprintf("10.42.%d.%d.0/24", idx/256, idx%256), nil
}
