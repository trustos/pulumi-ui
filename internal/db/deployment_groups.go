package db

import (
	"database/sql"
	"time"
)

// DeploymentGroup is a coordinated set of stacks deployed across multiple accounts.
type DeploymentGroup struct {
	ID           string
	Name         string
	Blueprint    string
	Status       string // configuring, deploying, deployed, partial, failed
	SharedConfig *string
	DeployLog    string
	Applications string // JSON: map[string]bool (app key → enabled)
	AppConfig    string // JSON: map[string]string ("app.field" → value)
	CreatedAt    int64
	UpdatedAt    int64
}

// GroupMember links a stack to a deployment group with a role and deploy order.
type GroupMember struct {
	GroupID     string
	StackName   string
	Role        string // e.g. "primary", "worker"
	DeployOrder int
	AccountID   *string
}

type DeploymentGroupStore struct {
	rdb *sql.DB
	wdb *ResilientWriter
}

func NewDeploymentGroupStore(p *DBPair) *DeploymentGroupStore {
	return &DeploymentGroupStore{rdb: p.ReadDB, wdb: p.WriteDB}
}

func (s *DeploymentGroupStore) Create(g *DeploymentGroup) error {
	_, err := s.wdb.Exec(`
		INSERT INTO deployment_groups (id, name, blueprint, status, shared_config, applications, app_config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, g.ID, g.Name, g.Blueprint, g.Status, g.SharedConfig, g.Applications, g.AppConfig, time.Now().Unix(), time.Now().Unix())
	return err
}

func (s *DeploymentGroupStore) Get(id string) (*DeploymentGroup, error) {
	g := &DeploymentGroup{}
	err := s.rdb.QueryRow(`
		SELECT id, name, blueprint, status, shared_config, deploy_log, applications, app_config, created_at, updated_at
		FROM deployment_groups WHERE id = ?
	`, id).Scan(&g.ID, &g.Name, &g.Blueprint, &g.Status, &g.SharedConfig, &g.DeployLog, &g.Applications, &g.AppConfig, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return g, err
}

func (s *DeploymentGroupStore) GetByName(name string) (*DeploymentGroup, error) {
	g := &DeploymentGroup{}
	err := s.rdb.QueryRow(`
		SELECT id, name, blueprint, status, shared_config, deploy_log, applications, app_config, created_at, updated_at
		FROM deployment_groups WHERE name = ?
	`, name).Scan(&g.ID, &g.Name, &g.Blueprint, &g.Status, &g.SharedConfig, &g.DeployLog, &g.Applications, &g.AppConfig, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return g, err
}

func (s *DeploymentGroupStore) List() ([]DeploymentGroup, error) {
	rows, err := s.rdb.Query(`
		SELECT id, name, blueprint, status, shared_config, created_at, updated_at
		FROM deployment_groups ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []DeploymentGroup
	for rows.Next() {
		var g DeploymentGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Blueprint, &g.Status, &g.SharedConfig, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// MarkStaleDeploying is called on startup to reset any groups left in
// 'deploying' state by a server restart or crash.
func (s *DeploymentGroupStore) MarkStaleDeploying() error {
	_, err := s.wdb.Exec(`
		UPDATE deployment_groups SET status = 'failed', updated_at = unixepoch()
		WHERE status = 'deploying'
	`)
	return err
}

// AppendDeployLog appends a JSON-encoded SSE event line to the deploy log.
func (s *DeploymentGroupStore) AppendDeployLog(id, line string) error {
	_, err := s.wdb.Exec(`
		UPDATE deployment_groups SET deploy_log = deploy_log || ? WHERE id = ?
	`, line+"\n", id)
	return err
}

// ClearDeployLog resets the deploy log for a new deployment.
func (s *DeploymentGroupStore) ClearDeployLog(id string) error {
	_, err := s.wdb.Exec(`
		UPDATE deployment_groups SET deploy_log = '' WHERE id = ?
	`, id)
	return err
}

func (s *DeploymentGroupStore) UpdateStatus(id, status string) error {
	_, err := s.wdb.Exec(`
		UPDATE deployment_groups SET status = ?, updated_at = ? WHERE id = ?
	`, status, time.Now().Unix(), id)
	return err
}

func (s *DeploymentGroupStore) UpdateApps(id, applications, appConfig string) error {
	_, err := s.wdb.Exec(`
		UPDATE deployment_groups SET applications = ?, app_config = ?, updated_at = ? WHERE id = ?
	`, applications, appConfig, time.Now().Unix(), id)
	return err
}

func (s *DeploymentGroupStore) Delete(id string) error {
	_, err := s.wdb.Exec(`DELETE FROM deployment_groups WHERE id = ?`, id)
	return err
}

// AddMember adds a stack to a deployment group.
func (s *DeploymentGroupStore) AddMember(m *GroupMember) error {
	_, err := s.wdb.Exec(`
		INSERT INTO stack_group_membership (group_id, stack_name, role, deploy_order, account_id)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(group_id, stack_name) DO UPDATE SET
			role = excluded.role,
			deploy_order = excluded.deploy_order,
			account_id = excluded.account_id
	`, m.GroupID, m.StackName, m.Role, m.DeployOrder, m.AccountID)
	return err
}

// ListMembers returns all stacks in a group, ordered by deploy_order.
func (s *DeploymentGroupStore) ListMembers(groupID string) ([]GroupMember, error) {
	rows, err := s.rdb.Query(`
		SELECT group_id, stack_name, role, deploy_order, account_id
		FROM stack_group_membership
		WHERE group_id = ?
		ORDER BY deploy_order, stack_name
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.GroupID, &m.StackName, &m.Role, &m.DeployOrder, &m.AccountID); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

// RemoveMember removes a stack from a group.
func (s *DeploymentGroupStore) RemoveMember(groupID, stackName string) error {
	_, err := s.wdb.Exec(`
		DELETE FROM stack_group_membership WHERE group_id = ? AND stack_name = ?
	`, groupID, stackName)
	return err
}

// GetGroupForStack returns the group a stack belongs to, or nil if not in a group.
func (s *DeploymentGroupStore) GetGroupForStack(stackName string) (*DeploymentGroup, *GroupMember, error) {
	var m GroupMember
	err := s.rdb.QueryRow(`
		SELECT group_id, stack_name, role, deploy_order, account_id
		FROM stack_group_membership WHERE stack_name = ?
	`, stackName).Scan(&m.GroupID, &m.StackName, &m.Role, &m.DeployOrder, &m.AccountID)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	g, err := s.Get(m.GroupID)
	if err != nil {
		return nil, nil, err
	}
	return g, &m, nil
}
