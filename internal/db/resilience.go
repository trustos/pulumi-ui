package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// Lock retry intervals, escalating backoff.
// Modeled after PocketBase's approach (also used in hopssh).
var lockRetryIntervals = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	150 * time.Millisecond,
	200 * time.Millisecond,
	300 * time.Millisecond,
	400 * time.Millisecond,
	500 * time.Millisecond,
	700 * time.Millisecond,
	1000 * time.Millisecond,
	1500 * time.Millisecond,
	2000 * time.Millisecond,
	3000 * time.Millisecond,
}

// isLockError returns true if the error is a SQLite lock contention error.
func isLockError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "table is locked")
}

// ResilientWriter wraps a *sql.DB with application-level retry for lock errors.
// It complements SQLite's busy_timeout pragma with exponential backoff,
// providing a second chance for writes that fail under heavy contention
// (e.g., concurrent AppendDeployLog calls from parallel worker deploys).
//
// This is applied to the write pool only — reads through WAL mode rarely
// encounter lock errors.
type ResilientWriter struct {
	*sql.DB
}

// Exec wraps sql.DB.Exec with lock retry.
func (r *ResilientWriter) Exec(query string, args ...interface{}) (sql.Result, error) {
	result, err := r.DB.Exec(query, args...)
	if err == nil || !isLockError(err) {
		return result, err
	}
	return r.retryExec(context.Background(), query, args)
}

// ExecContext wraps sql.DB.ExecContext with lock retry.
func (r *ResilientWriter) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	result, err := r.DB.ExecContext(ctx, query, args...)
	if err == nil || !isLockError(err) {
		return result, err
	}
	return r.retryExec(ctx, query, args)
}

func (r *ResilientWriter) retryExec(ctx context.Context, query string, args []interface{}) (sql.Result, error) {
	// Truncate query for logging
	logQuery := query
	if len(logQuery) > 80 {
		logQuery = logQuery[:80] + "..."
	}
	log.Printf("[db] lock contention on exec, starting retry: %s", strings.TrimSpace(logQuery))
	var lastErr error
	for i, interval := range lockRetryIntervals {
		_ = i
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		result, err := r.DB.ExecContext(ctx, query, args...)
		if err == nil {
			log.Printf("[db] lock retry succeeded")
			return result, nil
		}
		if !isLockError(err) {
			return result, err
		}
		lastErr = err
	}
	log.Printf("[db] lock retry EXHAUSTED after %d attempts: %s", len(lockRetryIntervals), strings.TrimSpace(logQuery))
	return nil, fmt.Errorf("database locked after %d retries: %w", len(lockRetryIntervals), lastErr)
}
