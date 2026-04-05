package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=30000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer; serialize all writes
	return db, nil
}

func Migrate(db *sql.DB) error {
	files, _ := fs.Glob(migrationsFS, "migrations/*.sql")
	sort.Strings(files) // lexicographic = version order

	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`)

	for _, f := range files {
		version := filepath.Base(f)
		var exists int
		db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if exists > 0 {
			continue
		}
		sqlBytes, _ := migrationsFS.ReadFile(f)
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("migration %s failed: %w", version, err)
		}
		db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version)
		log.Printf("[db] applied migration %s", version)
	}
	return nil
}
