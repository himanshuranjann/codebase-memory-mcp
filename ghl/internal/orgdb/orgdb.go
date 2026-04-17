// Package orgdb manages the cross-repo org intelligence graph (org.db).
package orgdb

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps a connection to the org.db SQLite database.
type DB struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) the org.db at the given path and ensures the schema exists.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("orgdb: open %s: %w", path, err)
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("orgdb: ping %s: %w", path, err)
	}
	d := &DB{db: sqlDB, path: path}
	if err := d.ensureSchema(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *DB) ensureSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS repos (
			id          INTEGER PRIMARY KEY,
			name        TEXT UNIQUE NOT NULL,
			github_url  TEXT NOT NULL,
			team        TEXT,
			type        TEXT,
			languages   TEXT,
			indexed_at  INTEGER,
			node_count  INTEGER,
			edge_count  INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS packages (
			id            INTEGER PRIMARY KEY,
			scope         TEXT NOT NULL,
			name          TEXT NOT NULL,
			provider_repo TEXT,
			version       TEXT,
			UNIQUE(scope, name)
		)`,
		`CREATE TABLE IF NOT EXISTS repo_dependencies (
			repo_id      INTEGER REFERENCES repos(id),
			package_id   INTEGER REFERENCES packages(id),
			dep_type     TEXT,
			version_spec TEXT,
			PRIMARY KEY (repo_id, package_id)
		)`,
		`CREATE TABLE IF NOT EXISTS api_contracts (
			id               INTEGER PRIMARY KEY,
			provider_repo    TEXT NOT NULL,
			consumer_repo    TEXT,
			method           TEXT NOT NULL,
			path             TEXT NOT NULL,
			provider_symbol  TEXT,
			consumer_symbol  TEXT,
			confidence       REAL DEFAULT 0.5
		)`,
		`CREATE TABLE IF NOT EXISTS event_contracts (
			id               INTEGER PRIMARY KEY,
			topic            TEXT NOT NULL,
			event_type       TEXT NOT NULL,
			producer_repo    TEXT,
			consumer_repo    TEXT,
			producer_symbol  TEXT,
			consumer_symbol  TEXT,
			schema_hash      TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS shared_databases (
			id            INTEGER PRIMARY KEY,
			connection_id TEXT NOT NULL,
			db_type       TEXT NOT NULL,
			repo_name     TEXT NOT NULL,
			access_type   TEXT NOT NULL,
			collection    TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS service_mesh (
			id            INTEGER PRIMARY KEY,
			source_repo   TEXT NOT NULL,
			source_app    TEXT NOT NULL,
			target_fqdn   TEXT NOT NULL,
			target_repo   TEXT,
			env           TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS team_ownership (
			repo_name   TEXT NOT NULL,
			team        TEXT NOT NULL,
			sub_team    TEXT,
			PRIMARY KEY (repo_name)
		)`,
		`CREATE TABLE IF NOT EXISTS deployments (
			id          INTEGER PRIMARY KEY,
			repo_name   TEXT NOT NULL,
			app_name    TEXT NOT NULL,
			deploy_type TEXT NOT NULL,
			env         TEXT NOT NULL,
			namespace   TEXT,
			helm_chart  TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS version_conflicts (
			package_id  INTEGER REFERENCES packages(id),
			repo_a      TEXT NOT NULL,
			version_a   TEXT NOT NULL,
			repo_b      TEXT NOT NULL,
			version_b   TEXT NOT NULL,
			severity    TEXT,
			detected_at INTEGER
		)`,
	}
	for _, stmt := range statements {
		if _, err := d.db.Exec(stmt); err != nil {
			return fmt.Errorf("orgdb: create schema: %w", err)
		}
	}
	return nil
}
