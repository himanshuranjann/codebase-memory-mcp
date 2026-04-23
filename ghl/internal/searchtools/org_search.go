// Package searchtools — org_search.go
//
// OrgSearch provides cross-repo code search by iterating every indexed
// project's SQLite .db and running grep-style regex scanning in parallel.
// Implements enricher.OrgSearcher.

package searchtools

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/enricher"
	_ "modernc.org/sqlite"
)

// OrgSearch searches all indexed .db files in cacheDir.
type OrgSearch struct {
	cacheDir string
	// maxProjects bounds parallel project scans.
	maxProjects int
	// maxHits caps total hits to prevent runaway on huge fleets.
	maxHits int
}

// NewOrgSearch returns an OrgSearch configured against cacheDir (where CBM
// stores per-project <project>.db files).
func NewOrgSearch(cacheDir string) *OrgSearch {
	return &OrgSearch{
		cacheDir:    cacheDir,
		maxProjects: 20,
		maxHits:     200,
	}
}

// ListProjects returns all project names (db file stems) in the search's cacheDir.
func (s *OrgSearch) ListProjects(_ context.Context) ([]string, error) {
	return listOrgProjects(s.cacheDir)
}

// listOrgProjects is a package-level helper that lists project names in a cacheDir.
// Returns a deterministically-sorted slice.
func listOrgProjects(cacheDir string) ([]string, error) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, err
	}
	var projects []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".db") {
			continue
		}
		projects = append(projects, strings.TrimSuffix(name, ".db"))
	}
	sort.Strings(projects)
	return projects, nil
}

// SearchAll searches pattern (regex) across ALL project DBs in parallel.
// fileGlob is a simple extension filter (e.g. "*.ts" or "*.{ts,vue,tsx,js,jsx}").
func (s *OrgSearch) SearchAll(ctx context.Context, pattern, fileGlob string) ([]enricher.OrgSearchHit, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}
	exts := parseGlobExtensions(fileGlob)

	projects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	sem := make(chan struct{}, s.maxProjects)
	var wg sync.WaitGroup
	var mu sync.Mutex
	hits := make([]enricher.OrgSearchHit, 0, 32)
	full := false

	for _, proj := range projects {
		if ctx.Err() != nil {
			break
		}
		if full {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(project string) {
			defer wg.Done()
			defer func() { <-sem }()
			projectHits := s.searchProject(ctx, project, re, exts)
			if len(projectHits) == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, h := range projectHits {
				if len(hits) >= s.maxHits {
					full = true
					return
				}
				hits = append(hits, h)
			}
		}(proj)
	}
	wg.Wait()
	return hits, nil
}

// searchProject opens one project DB, reads files matching exts, and runs re.
func (s *OrgSearch) searchProject(ctx context.Context, project string, re *regexp.Regexp, exts []string) []enricher.OrgSearchHit {
	dbPath := filepath.Join(s.cacheDir, project+".db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil
	}
	defer db.Close()

	var rootPath string
	if err := db.QueryRowContext(ctx, `SELECT root_path FROM projects WHERE name = ?`, project).Scan(&rootPath); err != nil {
		return nil
	}

	// Fetch all indexed file paths for this project.
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT file_path FROM nodes WHERE project = ? AND file_path IS NOT NULL`, project)
	if err != nil {
		return nil
	}
	defer rows.Close()

	repoSlug := projectNameToRepo(project)
	var hits []enricher.OrgSearchHit
	for rows.Next() {
		if ctx.Err() != nil {
			return hits
		}
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			continue
		}
		if !matchExt(filePath, exts) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rootPath, filePath))
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				hits = append(hits, enricher.OrgSearchHit{
					Project:  project,
					Repo:     repoSlug,
					FilePath: filePath,
					Line:     i + 1,
					Text:     line,
				})
				if len(hits) >= 20 {
					return hits // per-project cap
				}
			}
		}
	}
	return hits
}

// projectNameToRepo strips the "data-fleet-cache-repos-" prefix.
func projectNameToRepo(projectName string) string {
	return strings.TrimPrefix(projectName, "data-fleet-cache-repos-")
}

// parseGlobExtensions extracts the extensions from a glob like "*.{ts,vue,tsx}".
// Returns nil for "*" or empty (meaning: match all).
func parseGlobExtensions(glob string) []string {
	glob = strings.TrimSpace(glob)
	if glob == "" || glob == "*" {
		return nil
	}
	i := strings.Index(glob, "{")
	j := strings.LastIndex(glob, "}")
	if i >= 0 && j > i {
		parts := strings.Split(glob[i+1:j], ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, "."+strings.TrimPrefix(p, "."))
			}
		}
		return out
	}
	if strings.HasPrefix(glob, "*.") {
		return []string{"." + strings.TrimPrefix(glob, "*.")}
	}
	return nil
}

// matchExt returns true if path matches any of exts (nil exts = match all).
func matchExt(path string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	lower := strings.ToLower(path)
	for _, e := range exts {
		if strings.HasSuffix(lower, e) {
			return true
		}
	}
	return false
}
