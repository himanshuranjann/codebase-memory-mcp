// Package pipeline — PopulateFromProjectDB builds org.db by directly reading
// the hydrated per-project SQLite .db files from disk. No MCP calls needed.
// This is the most reliable approach for Cloud Run: project .db files are
// persisted to GCS and hydrated to /tmp/codebase-memory-mcp/ on startup.
package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/manifest"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/mcp"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/orgdb"
)

// MCPCaller is the interface for calling MCP tools on the C binary.
type MCPCaller interface {
	CallTool(ctx context.Context, name string, params map[string]interface{}) (*mcp.ToolResult, error)
}

// PopulateOrgFromProjectDBs builds org.db by directly reading all project .db files
// from the CBM cache directory. Each project's SQLite DB contains nodes (Route, Function,
// Class, etc.) and edges (HANDLES, HTTP_CALLS, IMPORTS, CALLS) that we extract to build
// the org-wide dependency graph, API contracts, and team topology.
func PopulateOrgFromProjectDBs(ctx context.Context, db *orgdb.DB, caller MCPCaller, repos []manifest.Repo, cbmCacheDir string) error {
	// Find all project .db files on disk
	pattern := filepath.Join(cbmCacheDir, "*.db")
	dbFiles, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("pipeline: glob project dbs: %w", err)
	}
	// Filter out WAL/SHM files and org.db
	var projectDBs []string
	for _, f := range dbFiles {
		base := filepath.Base(f)
		if strings.HasSuffix(base, "-wal") || strings.HasSuffix(base, "-shm") {
			continue
		}
		if base == "org.db" {
			continue
		}
		projectDBs = append(projectDBs, f)
	}

	slog.Info("populating org.db from project DB files", "files", len(projectDBs), "cache_dir", cbmCacheDir)

	// Build repo lookup
	repoByName := make(map[string]manifest.Repo, len(repos))
	for _, r := range repos {
		repoByName[r.Name] = r
	}

	populated := 0
	routeCount := 0
	httpCallCount := 0

	for _, dbPath := range projectDBs {
		projectName := strings.TrimSuffix(filepath.Base(dbPath), ".db")
		repoName := stripProjectPrefix(projectName)

		repo, ok := repoByName[repoName]
		if !ok {
			repo = manifest.Repo{Name: repoName}
		}

		// Open project DB read-only
		projDB, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=busy_timeout(1000)")
		if err != nil {
			slog.Debug("skip project db", "path", dbPath, "err", err)
			continue
		}

		// Get node/edge counts
		var nodeCount, edgeCount int
		projDB.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&nodeCount)
		projDB.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edgeCount)

		// Write repo record
		db.UpsertRepo(orgdb.RepoRecord{
			Name:      repoName,
			GitHubURL: repo.GitHubURL,
			Team:      repo.Team,
			Type:      repo.Type,
			NodeCount: nodeCount,
			EdgeCount: edgeCount,
		})
		db.UpsertTeamOwnership(repoName, repo.Team, "")

		// Extract Route nodes → API contracts (provider side)
		routes := extractRoutes(projDB, projectName)
		for _, r := range routes {
			db.InsertAPIContract(orgdb.APIContract{
				ProviderRepo:   repoName,
				Method:         r.method,
				Path:           r.path,
				ProviderSymbol: r.handler,
				Confidence:     0.3,
			})
			routeCount++
		}

		// Extract HTTP_CALLS edges → API contracts (consumer side)
		calls := extractHTTPCalls(projDB, projectName)
		for _, c := range calls {
			db.InsertAPIContract(orgdb.APIContract{
				ConsumerRepo:   repoName,
				Method:         c.method,
				Path:           c.path,
				ConsumerSymbol: c.caller,
				Confidence:     0.5,
			})
			httpCallCount++
		}

		// Extract IMPORTS edges → package dependencies
		imports := extractImports(projDB, projectName)
		for _, imp := range imports {
			if isGHLPackage(imp.packageName) {
				scope, name := splitPackage(imp.packageName)
				if scope != "" {
					db.UpsertPackageDep(repoName, orgdb.Dep{
						Scope:   scope,
						Name:    name,
						DepType: "dependencies",
					})
				}
			}
		}

		projDB.Close()
		populated++

		if populated%50 == 0 {
			slog.Info("org.db population progress", "populated", populated, "total", len(projectDBs),
				"routes", routeCount, "http_calls", httpCallCount)
		}
	}

	// Cross-reference consumer→provider contracts
	matched, err := db.CrossReferenceContracts()
	if err != nil {
		slog.Warn("cross-reference contracts failed", "err", err)
	}

	slog.Info("org.db populated from project DB files",
		"repos", populated, "routes", routeCount, "http_calls", httpCallCount,
		"cross_referenced", matched)
	return nil
}

type routeData struct {
	method  string
	path    string
	handler string
}

func extractRoutes(db *sql.DB, project string) []routeData {
	rows, err := db.Query(`
		SELECT n.name, n.qualified_name, n.properties
		FROM nodes n
		WHERE n.label = 'Route'
		LIMIT 500
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var routes []routeData
	for rows.Next() {
		var name, qn, propsJSON string
		if rows.Scan(&name, &qn, &propsJSON) != nil {
			continue
		}
		r := routeData{path: name}

		// Parse properties for method
		var props map[string]interface{}
		if json.Unmarshal([]byte(propsJSON), &props) == nil {
			if m, ok := props["method"].(string); ok {
				r.method = m
			}
			if h, ok := props["handler"].(string); ok {
				r.handler = h
			}
		}

		// Extract from qualified name: __route__POST__/api/path
		if strings.HasPrefix(qn, "__route__") {
			parts := strings.SplitN(strings.TrimPrefix(qn, "__route__"), "__", 2)
			if len(parts) == 2 {
				r.method = parts[0]
				if r.path == "" || r.path == name {
					r.path = parts[1]
				}
			}
		}
		if r.method == "" {
			r.method = "GET"
		}
		if r.path == "" {
			r.path = name
		}
		routes = append(routes, r)
	}
	return routes
}

type httpCallData struct {
	method string
	path   string
	caller string
}

func extractHTTPCalls(db *sql.DB, project string) []httpCallData {
	rows, err := db.Query(`
		SELECT src.qualified_name, tgt.qualified_name, e.properties
		FROM edges e
		JOIN nodes src ON e.source_id = src.id
		JOIN nodes tgt ON e.target_id = tgt.id
		WHERE e.type = 'HTTP_CALLS'
		LIMIT 500
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var calls []httpCallData
	for rows.Next() {
		var srcQN, tgtQN, propsJSON string
		if rows.Scan(&srcQN, &tgtQN, &propsJSON) != nil {
			continue
		}

		c := httpCallData{caller: srcQN}

		// Parse edge properties
		var props map[string]interface{}
		if json.Unmarshal([]byte(propsJSON), &props) == nil {
			if p, ok := props["url_path"].(string); ok {
				c.path = p
			}
			if m, ok := props["method"].(string); ok {
				c.method = m
			}
		}

		// Extract from target Route QN
		if strings.HasPrefix(tgtQN, "__route__") {
			parts := strings.SplitN(strings.TrimPrefix(tgtQN, "__route__"), "__", 2)
			if len(parts) == 2 {
				if c.method == "" {
					c.method = parts[0]
				}
				if c.path == "" {
					c.path = parts[1]
				}
			}
		}

		if c.method == "" {
			c.method = "GET"
		}
		calls = append(calls, c)
	}
	return calls
}

type importData struct {
	packageName string
}

func extractImports(db *sql.DB, project string) []importData {
	rows, err := db.Query(`
		SELECT DISTINCT tgt.name
		FROM edges e
		JOIN nodes tgt ON e.target_id = tgt.id
		WHERE e.type = 'IMPORTS' AND tgt.label = 'Package'
		LIMIT 200
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var imports []importData
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		imports = append(imports, importData{packageName: name})
	}
	return imports
}

func isGHLPackage(name string) bool {
	return strings.HasPrefix(name, "@platform-core/") ||
		strings.HasPrefix(name, "@platform-ui/") ||
		strings.HasPrefix(name, "@gohighlevel/") ||
		strings.HasPrefix(name, "@ghl/") ||
		strings.HasPrefix(name, "@frontend-core/")
}

func splitPackage(name string) (string, string) {
	if !strings.HasPrefix(name, "@") {
		return "", name
	}
	idx := strings.Index(name, "/")
	if idx < 0 {
		return "", name
	}
	return name[:idx], name[idx+1:]
}

func stripProjectPrefix(name string) string {
	for _, prefix := range []string{
		"data-fleet-cache-repos-",
		"tmp-fleet-cache-repos-",
		"tmp-fleet-cache-",
		"app-fleet-cache-",
	} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

func extractText(result *mcp.ToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].Text
}
