package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DBPair holds separate connection pools for reads and writes.
// SQLite allows concurrent readers with WAL mode, but only one writer at a time.
// Separating the pools prevents reads from queueing behind writes.
type DBPair struct {
	ReadDB  *sql.DB // concurrent readers (up to 120)
	WriteDB *sql.DB // single writer (serialized)
}

// Close closes both database connections.
func (p *DBPair) Close() error {
	rerr := p.ReadDB.Close()
	werr := p.WriteDB.Close()
	if werr != nil {
		return werr
	}
	return rerr
}

// Open creates a dual read/write connection pool for the SQLite database.
// Pragmas tuned for performance (following PocketBase's approach):
//   - WAL mode: concurrent readers + single writer
//   - synchronous=NORMAL: faster writes, safe with WAL
//   - cache_size=-32000: 32MB page cache (vs 2MB default)
//   - temp_store=MEMORY: temp tables in RAM
//   - busy_timeout=10000: 10s retry on lock contention
func Open(path string) (*DBPair, error) {
	// For in-memory databases (tests), use shared cache so both pools see the same data.
	if path == ":memory:" {
		path = "file::memory:?cache=shared"
	}
	dsn := path + "&_journal_mode=WAL&_foreign_keys=on&_busy_timeout=10000" +
		"&_synchronous=NORMAL&_cache_size=-32000&_temp_store=MEMORY"
	// If path doesn't have query params yet, fix the separator.
	if !strings.Contains(path, "?") {
		dsn = path + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=10000" +
			"&_synchronous=NORMAL&_cache_size=-32000&_temp_store=MEMORY"
	}

	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(120)
	readDB.SetMaxIdleConns(10)

	return &DBPair{ReadDB: readDB, WriteDB: writeDB}, nil
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
