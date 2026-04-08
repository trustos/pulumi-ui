package db

import (
	"database/sql"
	"time"
)

type Operation struct {
	ID         string `json:"id"`
	StackName  string `json:"stackName"`
	Operation  string `json:"operation"`
	Status     string `json:"status"`
	Log        string `json:"log,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt *int64 `json:"finishedAt,omitempty"`
}

type OperationStore struct {
	rdb *sql.DB
	wdb *ResilientWriter
}

func NewOperationStore(p *DBPair) *OperationStore {
	return &OperationStore{rdb: p.ReadDB, wdb: p.WriteDB}
}

func (s *OperationStore) Create(id, stackName, operation string) error {
	_, err := s.wdb.Exec(`
		INSERT INTO operations (id, stack_name, operation, status)
		VALUES (?, ?, ?, 'running')
	`, id, stackName, operation)
	return err
}

func (s *OperationStore) AppendLog(id, line string) error {
	_, err := s.wdb.Exec(`
		UPDATE operations SET log = log || ? WHERE id = ?
	`, line+"\n", id)
	return err
}

func (s *OperationStore) Finish(id, status string) error {
	_, err := s.wdb.Exec(`
		UPDATE operations SET status = ?, finished_at = ? WHERE id = ?
	`, status, time.Now().Unix(), id)
	return err
}

// MarkStaleRunning is called on startup to fail any operations that were left
// in 'running' state by a previous server crash or ungraceful shutdown.
func (s *OperationStore) MarkStaleRunning() error {
	_, err := s.wdb.Exec(`
		UPDATE operations
		SET status = 'failed',
		    finished_at = ?,
		    log = log || ?
		WHERE status = 'running'
	`, time.Now().Unix(), "Server restarted while this operation was in progress.\n")
	return err
}

// GetLatestLog returns the most recent operation for a stack, including its full log.
func (s *OperationStore) GetLatestLog(stackName string) (*Operation, error) {
	op := &Operation{}
	err := s.rdb.QueryRow(`
		SELECT id, stack_name, operation, status, log, started_at, finished_at
		FROM operations WHERE stack_name = ?
		ORDER BY started_at DESC LIMIT 1
	`, stackName).Scan(&op.ID, &op.StackName, &op.Operation, &op.Status, &op.Log, &op.StartedAt, &op.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return op, err
}

// ListLogsForStack returns up to limit operations for a stack with full log content,
// ordered oldest-first. since filters to operations that started on or after the
// stack's creation time, so a recreated stack with the same name starts with a clean log.
func (s *OperationStore) ListLogsForStack(stackName string, limit int, since int64) ([]Operation, error) {
	rows, err := s.rdb.Query(`
		SELECT id, stack_name, operation, status, log, started_at, finished_at
		FROM operations WHERE stack_name = ? AND started_at >= ?
		ORDER BY started_at DESC LIMIT ?
	`, stackName, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []Operation
	for rows.Next() {
		var op Operation
		rows.Scan(&op.ID, &op.StackName, &op.Operation, &op.Status, &op.Log, &op.StartedAt, &op.FinishedAt)
		ops = append(ops, op)
	}
	// Reverse to chronological order (oldest first)
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops, nil
}

// DeleteForStack removes all operations (and their logs) for a given stack name.
// Called when a stack is deleted so a new stack with the same name starts clean.
func (s *OperationStore) DeleteForStack(stackName string) error {
	_, err := s.wdb.Exec(`DELETE FROM operations WHERE stack_name = ?`, stackName)
	return err
}

func (s *OperationStore) ListForStack(stackName string, limit int, since int64) ([]Operation, error) {
	rows, err := s.rdb.Query(`
		SELECT id, stack_name, operation, status, started_at, finished_at
		FROM operations WHERE stack_name = ? AND started_at >= ?
		ORDER BY started_at DESC LIMIT ?
	`, stackName, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []Operation
	for rows.Next() {
		var op Operation
		rows.Scan(&op.ID, &op.StackName, &op.Operation, &op.Status, &op.StartedAt, &op.FinishedAt)
		ops = append(ops, op)
	}
	return ops, nil
}
