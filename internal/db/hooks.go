package db

import (
	"database/sql"

	"github.com/google/uuid"
)

type Hook struct {
	ID              string  `json:"id"`
	StackName       string  `json:"stackName"`
	Trigger         string  `json:"trigger"`
	Type            string  `json:"type"`
	Priority        int     `json:"priority"`
	ContinueOnError bool    `json:"continueOnError"`
	Command         *string `json:"command,omitempty"`
	NodeIndex       *int    `json:"nodeIndex,omitempty"`
	URL             *string `json:"url,omitempty"`
	Source          string  `json:"source"`
	Description     string  `json:"description"`
	CreatedAt       int64   `json:"createdAt"`
}

type HookStore struct {
	db *sql.DB
}

func NewHookStore(db *sql.DB) *HookStore {
	return &HookStore{db: db}
}

func (s *HookStore) Create(h *Hook) error {
	if h.ID == "" {
		h.ID = uuid.New().String()
	}
	_, err := s.db.Exec(`
		INSERT INTO lifecycle_hooks (id, stack_name, trigger, type, priority, continue_on_error, command, node_index, url, source, description)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, h.ID, h.StackName, h.Trigger, h.Type, h.Priority, boolToInt(h.ContinueOnError), h.Command, h.NodeIndex, h.URL, h.Source, h.Description)
	return err
}

func (s *HookStore) ListForStack(stackName string) ([]Hook, error) {
	rows, err := s.db.Query(`
		SELECT id, stack_name, trigger, type, priority, continue_on_error, command, node_index, url, source, description, created_at
		FROM lifecycle_hooks WHERE stack_name = ?
		ORDER BY priority ASC
	`, stackName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHooks(rows)
}

func (s *HookStore) ListByTrigger(stackName, trigger string) ([]Hook, error) {
	rows, err := s.db.Query(`
		SELECT id, stack_name, trigger, type, priority, continue_on_error, command, node_index, url, source, description, created_at
		FROM lifecycle_hooks WHERE stack_name = ? AND trigger = ?
		ORDER BY priority ASC
	`, stackName, trigger)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHooks(rows)
}

func (s *HookStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM lifecycle_hooks WHERE id = ?`, id)
	return err
}

func (s *HookStore) DeleteBySource(stackName, source string) error {
	_, err := s.db.Exec(`DELETE FROM lifecycle_hooks WHERE stack_name = ? AND source = ?`, stackName, source)
	return err
}

func (s *HookStore) DeleteForStack(stackName string) error {
	_, err := s.db.Exec(`DELETE FROM lifecycle_hooks WHERE stack_name = ?`, stackName)
	return err
}

func scanHooks(rows *sql.Rows) ([]Hook, error) {
	var hooks []Hook
	for rows.Next() {
		var h Hook
		var contOnErr int
		if err := rows.Scan(&h.ID, &h.StackName, &h.Trigger, &h.Type, &h.Priority, &contOnErr, &h.Command, &h.NodeIndex, &h.URL, &h.Source, &h.Description, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.ContinueOnError = contOnErr != 0
		hooks = append(hooks, h)
	}
	return hooks, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
