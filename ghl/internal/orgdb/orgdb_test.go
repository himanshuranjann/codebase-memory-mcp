package orgdb

import (
	"path/filepath"
	"testing"
)

func TestOpen_CreatesSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "org.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tables := []string{
		"repos", "packages", "repo_dependencies",
		"api_contracts", "event_contracts",
		"shared_databases", "service_mesh",
		"team_ownership", "deployments", "version_conflicts",
	}
	for _, table := range tables {
		var count int
		err := db.db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query sqlite_master for %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s: want 1, got %d", table, count)
		}
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "org.db")

	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	db1.Close()

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer db2.Close()
}
