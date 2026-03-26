package db

import (
	"database/sql"
	"fmt"

	"github.com/trustos/pulumi-ui/internal/crypto"
)

// StackConnection holds the Nebula mesh state for a deployed stack.
type StackConnection struct {
	StackName      string
	NebulaCACert   []byte  // PEM, plaintext
	NebulaCAKey    []byte  // PEM, plaintext (decrypted on read)
	NebulaUICert   []byte  // PEM, plaintext
	NebulaUIKey    []byte  // PEM, plaintext (decrypted on read)
	NebulaSubnet   string  // e.g. "10.42.1.0/24"
	NebulaAgentCert []byte // PEM, plaintext — dedicated agent identity (.2)
	NebulaAgentKey  []byte // PEM, plaintext (decrypted on read)
	AgentToken     string  // per-stack Bearer token (hex)
	AgentRealIP    *string // public or NLB IP for Nebula static_host_map
	LighthouseAddr *string
	AgentNebulaIP  *string
	ConnectedAt    int64
	LastSeenAt     *int64
	ClusterInfo    *string // JSON
}

type StackConnectionStore struct {
	db  *sql.DB
	enc *crypto.Encryptor
}

func NewStackConnectionStore(db *sql.DB, enc *crypto.Encryptor) *StackConnectionStore {
	return &StackConnectionStore{db: db, enc: enc}
}

// Create inserts a new stack connection with Nebula PKI material.
// The CA key, UI key, and agent key are AES-GCM encrypted before storage.
func (s *StackConnectionStore) Create(conn *StackConnection) error {
	encCAKey, err := s.enc.EncryptBytes(conn.NebulaCAKey)
	if err != nil {
		return fmt.Errorf("encrypt nebula CA key: %w", err)
	}
	encUIKey, err := s.enc.EncryptBytes(conn.NebulaUIKey)
	if err != nil {
		return fmt.Errorf("encrypt nebula UI key: %w", err)
	}
	var encAgentKey []byte
	if len(conn.NebulaAgentKey) > 0 {
		encAgentKey, err = s.enc.EncryptBytes(conn.NebulaAgentKey)
		if err != nil {
			return fmt.Errorf("encrypt nebula agent key: %w", err)
		}
	}

	_, err = s.db.Exec(`
		INSERT INTO stack_connections
			(stack_name, nebula_ca_cert, nebula_ca_key, nebula_ui_cert, nebula_ui_key,
			 nebula_subnet, agent_cert, agent_key, agent_token)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, conn.StackName, conn.NebulaCACert, encCAKey, conn.NebulaUICert, encUIKey,
		conn.NebulaSubnet, conn.NebulaAgentCert, encAgentKey, conn.AgentToken)
	return err
}

// Get returns the stack connection for the given stack, or nil if not found.
func (s *StackConnectionStore) Get(stackName string) (*StackConnection, error) {
	var conn StackConnection
	var encCAKey, encUIKey, encAgentKey []byte

	err := s.db.QueryRow(`
		SELECT stack_name, nebula_ca_cert, nebula_ca_key, nebula_ui_cert, nebula_ui_key,
		       nebula_subnet, agent_cert, agent_key, agent_token, agent_real_ip,
		       lighthouse_addr, agent_nebula_ip, connected_at, last_seen_at, cluster_info
		FROM stack_connections WHERE stack_name = ?
	`, stackName).Scan(
		&conn.StackName, &conn.NebulaCACert, &encCAKey, &conn.NebulaUICert, &encUIKey,
		&conn.NebulaSubnet, &conn.NebulaAgentCert, &encAgentKey, &conn.AgentToken, &conn.AgentRealIP,
		&conn.LighthouseAddr, &conn.AgentNebulaIP,
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
	if len(encAgentKey) > 0 {
		conn.NebulaAgentKey, err = s.enc.DecryptBytes(encAgentKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt nebula agent key: %w", err)
		}
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

// UpdateAgentConnected records agent mesh IP, real IP, and refreshes timestamps.
func (s *StackConnectionStore) UpdateAgentConnected(stackName, nebulaIP, realIP, clusterInfo string) error {
	_, err := s.db.Exec(`
		UPDATE stack_connections
		SET agent_nebula_ip = ?, agent_real_ip = ?, last_seen_at = unixepoch(), cluster_info = ?
		WHERE stack_name = ?
	`, nebulaIP, realIP, clusterInfo, stackName)
	return err
}

// UpdateAgentRealIP stores the instance's public or NLB IP (for Nebula static_host_map).
func (s *StackConnectionStore) UpdateAgentRealIP(stackName, realIP string) error {
	_, err := s.db.Exec(`
		UPDATE stack_connections SET agent_real_ip = ? WHERE stack_name = ?
	`, realIP, stackName)
	return err
}

// ClearAgentConnection resets the runtime-discovered fields after a destroy so
// the UI no longer shows the agent as connected. The PKI material (certs, keys,
// subnet) is preserved so a re-deploy can reuse the same Nebula identity.
func (s *StackConnectionStore) ClearAgentConnection(stackName string) error {
	_, err := s.db.Exec(`
		UPDATE stack_connections
		SET agent_nebula_ip = NULL, agent_real_ip = NULL,
		    lighthouse_addr = NULL, last_seen_at = NULL, cluster_info = NULL
		WHERE stack_name = ?
	`, stackName)
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
	// Map index n into a unique /24 subnet within the 10.{second}.{third}.0/24 space.
	// second octet: 42 + (n / 256), third octet: n % 256.
	return fmt.Sprintf("10.%d.%d.0/24", 42+(idx/256), idx%256), nil
}
