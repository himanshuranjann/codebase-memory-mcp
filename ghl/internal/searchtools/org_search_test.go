package searchtools

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func createOrgSearchDB(t *testing.T, cacheDir, projectName, rootPath string, files []string) {
	t.Helper()

	dbPath := filepath.Join(cacheDir, projectName+".db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE projects (name TEXT, root_path TEXT);`,
		`CREATE TABLE nodes (project TEXT, file_path TEXT);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("db.Exec(%q): %v", stmt, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects(name, root_path) VALUES(?, ?)`, projectName, rootPath); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for _, file := range files {
		if _, err := db.Exec(`INSERT INTO nodes(project, file_path) VALUES(?, ?)`, projectName, file); err != nil {
			t.Fatalf("insert node %q: %v", file, err)
		}
	}
}

func TestListOrgProjects_FindsAllDBFiles(t *testing.T) {
	cacheDir := t.TempDir()
	for _, name := range []string{"alpha.db", "beta.db", "gamma.db"} {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "ignore.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(ignore.txt): %v", err)
	}

	projects, err := listOrgProjects(cacheDir)
	if err != nil {
		t.Fatalf("listOrgProjects returned error: %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(projects, want) {
		t.Fatalf("projects = %v, want %v", projects, want)
	}
}

func TestOrgSearch_SearchSingleProject(t *testing.T) {
	cacheDir := t.TempDir()
	rootPath := t.TempDir()
	projectName := "data-fleet-cache-repos-ghl-revex-frontend"
	relPath := filepath.ToSlash("apps/communities/src/checkout.ts")
	fullPath := filepath.Join(rootPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	source := "const a = 1\nawait axios.post('/community-checkout/checkout', payload)\n"
	if err := os.WriteFile(fullPath, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	createOrgSearchDB(t, cacheDir, projectName, rootPath, []string{relPath})

	hits, err := NewOrgSearch(cacheDir).SearchAll(context.Background(), "community-checkout", "*.{ts,js}")
	if err != nil {
		t.Fatalf("SearchAll returned error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	if hits[0].Project != projectName {
		t.Fatalf("Project = %q, want %q", hits[0].Project, projectName)
	}
	if hits[0].Repo != "ghl-revex-frontend" {
		t.Fatalf("Repo = %q, want ghl-revex-frontend", hits[0].Repo)
	}
	if hits[0].FilePath != relPath {
		t.Fatalf("FilePath = %q, want %q", hits[0].FilePath, relPath)
	}
	if hits[0].Line != 2 {
		t.Fatalf("Line = %d, want 2", hits[0].Line)
	}
}

func TestOrgSearch_SkipsUnreadableDB(t *testing.T) {
	cacheDir := t.TempDir()
	rootPath := t.TempDir()
	projectName := "data-fleet-cache-repos-ghl-revex-frontend"
	relPath := filepath.ToSlash("apps/communities/src/checkout.ts")
	fullPath := filepath.Join(rootPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("await fetch('/community-checkout/checkout')\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	createOrgSearchDB(t, cacheDir, projectName, rootPath, []string{relPath})

	if err := os.WriteFile(filepath.Join(cacheDir, "data-fleet-cache-repos-bad.db"), []byte("not-a-sqlite-db"), 0o600); err != nil {
		t.Fatalf("WriteFile(bad db): %v", err)
	}

	hits, err := NewOrgSearch(cacheDir).SearchAll(context.Background(), "community-checkout", "*.ts")
	if err != nil {
		t.Fatalf("SearchAll returned error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	if hits[0].Project != projectName {
		t.Fatalf("Project = %q, want %q", hits[0].Project, projectName)
	}
}

func TestProjectNameToRepo_StripsPrefixCorrectly(t *testing.T) {
	if got := projectNameToRepo("data-fleet-cache-repos-ghl-revex-frontend"); got != "ghl-revex-frontend" {
		t.Fatalf("projectNameToRepo() = %q, want %q", got, "ghl-revex-frontend")
	}
}
