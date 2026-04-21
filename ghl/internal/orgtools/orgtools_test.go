package orgtools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/mcp"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/orgdb"
)

// ---------- helpers ----------

func openTestDB(t *testing.T) *orgdb.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "org.db")
	db, err := orgdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedRepo(t *testing.T, db *orgdb.DB, name, team, typ string) {
	t.Helper()
	err := db.UpsertRepo(orgdb.RepoRecord{
		Name:      name,
		GitHubURL: "https://github.com/GoHighLevel/" + name + ".git",
		Team:      team,
		Type:      typ,
		Languages: `["typescript"]`,
		NodeCount: 10,
		EdgeCount: 5,
	})
	if err != nil {
		t.Fatalf("UpsertRepo(%s): %v", name, err)
	}
}

// seedRepoWithNodeCount creates a repo with a specific node_count.
func seedRepoWithNodeCount(t *testing.T, db *orgdb.DB, name, team, typ string, nodeCount int) {
	t.Helper()
	err := db.UpsertRepo(orgdb.RepoRecord{
		Name:      name,
		GitHubURL: "https://github.com/GoHighLevel/" + name + ".git",
		Team:      team,
		Type:      typ,
		Languages: `["typescript"]`,
		NodeCount: nodeCount,
		EdgeCount: 5,
	})
	if err != nil {
		t.Fatalf("UpsertRepo(%s): %v", name, err)
	}
}

// newService creates an OrgService backed by a temp DB.
func newService(t *testing.T) (*OrgService, *orgdb.DB) {
	t.Helper()
	db := openTestDB(t)
	return New(db), db
}

// mockBridge is a test double for BridgeCaller.
type mockBridge struct {
	calls   []mockBridgeCall
	handler func(name string, params map[string]interface{}) (*mcp.ToolResult, error)
}

type mockBridgeCall struct {
	Name   string
	Params map[string]interface{}
}

func (m *mockBridge) CallTool(_ context.Context, name string, params map[string]interface{}) (*mcp.ToolResult, error) {
	m.calls = append(m.calls, mockBridgeCall{Name: name, Params: params})
	if m.handler != nil {
		return m.handler(name, params)
	}
	return &mcp.ToolResult{
		Content: []mcp.Content{{Type: "text", Text: "No results found."}},
	}, nil
}

// ---------- Definitions ----------

func TestDefinitions_Returns6Tools(t *testing.T) {
	svc, _ := newService(t)
	defs := svc.Definitions()
	if len(defs) != 6 {
		t.Fatalf("want 6 definitions, got %d", len(defs))
	}

	expected := map[string]bool{
		"org_dependency_graph": false,
		"org_blast_radius":     false,
		"org_trace_flow":       false,
		"org_team_topology":    false,
		"org_search":           false,
		"org_code_search":      false,
	}
	for _, d := range defs {
		if _, ok := expected[d.Name]; !ok {
			t.Errorf("unexpected tool name: %q", d.Name)
		}
		expected[d.Name] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing tool definition: %q", name)
		}
	}
}

// ---------- IsOrgTool ----------

func TestIsOrgTool_KnownTools(t *testing.T) {
	svc, _ := newService(t)
	for _, name := range []string{
		"org_dependency_graph", "org_blast_radius", "org_trace_flow",
		"org_team_topology", "org_search", "org_code_search",
	} {
		if !svc.IsOrgTool(name) {
			t.Errorf("IsOrgTool(%q) = false, want true", name)
		}
	}
}

func TestIsOrgTool_UnknownTool(t *testing.T) {
	svc, _ := newService(t)
	if svc.IsOrgTool("unknown_tool") {
		t.Error("IsOrgTool(unknown_tool) = true, want false")
	}
}

// ---------- CallTool: org_dependency_graph ----------

func TestCallTool_DependencyGraph(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "repo-a", "team-a", "backend")
	seedRepo(t, db, "repo-b", "team-b", "backend")

	for _, name := range []string{"repo-a", "repo-b"} {
		if err := db.UpsertPackageDep(name, orgdb.Dep{
			Scope: "@platform-core", Name: "base-service",
			DepType: "dependencies", VersionSpec: "^3.0.0",
		}); err != nil {
			t.Fatalf("UpsertPackageDep(%s): %v", name, err)
		}
	}

	result, err := svc.CallTool(context.Background(), "org_dependency_graph", map[string]interface{}{
		"package_scope": "@platform-core",
		"package_name":  "base-service",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	deps, ok := result.([]orgdb.DependencyResult)
	if !ok {
		t.Fatalf("result type: got %T, want []orgdb.DependencyResult", result)
	}
	if len(deps) != 2 {
		t.Fatalf("want 2 results, got %d", len(deps))
	}
}

func TestCallTool_DependencyGraph_MissingArgs(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "org_dependency_graph", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

// ---------- CallTool: org_blast_radius ----------

func TestCallTool_BlastRadius(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "provider-repo", "platform", "backend")
	seedRepo(t, db, "api-consumer", "payments", "backend")

	if err := db.InsertAPIContract(orgdb.APIContract{
		ProviderRepo: "provider-repo", ConsumerRepo: "api-consumer",
		Method: "GET", Path: "/api/v1/users", Confidence: 0.9,
	}); err != nil {
		t.Fatalf("InsertAPIContract: %v", err)
	}

	result, err := svc.CallTool(context.Background(), "org_blast_radius", map[string]interface{}{
		"repo": "provider-repo",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	br, ok := result.(orgdb.BlastRadiusResult)
	if !ok {
		t.Fatalf("result type: got %T, want orgdb.BlastRadiusResult", result)
	}
	if br.TotalRepos != 1 {
		t.Errorf("TotalRepos: want 1, got %d", br.TotalRepos)
	}
}

func TestCallTool_BlastRadius_MissingArgs(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "org_blast_radius", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

// ---------- CallTool: org_trace_flow ----------

func TestCallTool_TraceFlow(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "svc-a", "team", "backend")
	seedRepo(t, db, "svc-b", "team", "backend")

	if err := db.InsertAPIContract(orgdb.APIContract{
		ProviderRepo: "svc-a", ConsumerRepo: "svc-b",
		Method: "GET", Path: "/api/v1/data", Confidence: 0.9,
	}); err != nil {
		t.Fatalf("InsertAPIContract: %v", err)
	}

	result, err := svc.CallTool(context.Background(), "org_trace_flow", map[string]interface{}{
		"trigger":   "svc-a",
		"direction": "downstream",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	steps, ok := result.([]orgdb.FlowStep)
	if !ok {
		t.Fatalf("result type: got %T, want []orgdb.FlowStep", result)
	}
	if len(steps) == 0 {
		t.Fatal("want at least 1 step, got 0")
	}
	if steps[0].FromRepo != "svc-a" || steps[0].ToRepo != "svc-b" {
		t.Errorf("step: got %s -> %s, want svc-a -> svc-b", steps[0].FromRepo, steps[0].ToRepo)
	}
}

func TestCallTool_TraceFlow_DefaultDirection(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "svc-a", "team", "backend")
	seedRepo(t, db, "svc-b", "team", "backend")

	if err := db.InsertAPIContract(orgdb.APIContract{
		ProviderRepo: "svc-a", ConsumerRepo: "svc-b",
		Method: "GET", Path: "/api/v1/data", Confidence: 0.9,
	}); err != nil {
		t.Fatalf("InsertAPIContract: %v", err)
	}

	// No direction specified — should default to "downstream"
	result, err := svc.CallTool(context.Background(), "org_trace_flow", map[string]interface{}{
		"trigger": "svc-a",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	steps, ok := result.([]orgdb.FlowStep)
	if !ok {
		t.Fatalf("result type: got %T", result)
	}
	if len(steps) == 0 {
		t.Fatal("want at least 1 step with default direction")
	}
}

func TestCallTool_TraceFlow_MissingArgs(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "org_trace_flow", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing trigger")
	}
}

// ---------- CallTool: org_team_topology ----------

func TestCallTool_TeamTopology(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "revex-backend", "revex", "backend")
	seedRepo(t, db, "revex-frontend", "revex", "frontend")

	result, err := svc.CallTool(context.Background(), "org_team_topology", map[string]interface{}{
		"team": "revex",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	info, ok := result.(orgdb.TeamInfo)
	if !ok {
		t.Fatalf("result type: got %T, want orgdb.TeamInfo", result)
	}
	if info.Team != "revex" {
		t.Errorf("Team: got %q, want %q", info.Team, "revex")
	}
	if len(info.Repos) != 2 {
		t.Errorf("Repos: want 2, got %d", len(info.Repos))
	}
}

func TestCallTool_TeamTopology_MissingArgs(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "org_team_topology", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing team")
	}
}

// ---------- CallTool: org_search ----------

func TestCallTool_Search(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "ghl-revex-backend", "revex", "backend")
	seedRepo(t, db, "ghl-revex-frontend", "revex", "frontend")
	seedRepo(t, db, "ghl-payments-backend", "payments", "backend")

	result, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{
		"query": "revex",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	repos, ok := result.([]orgdb.RepoSearchResult)
	if !ok {
		t.Fatalf("result type: got %T, want []orgdb.RepoSearchResult", result)
	}
	if len(repos) != 2 {
		t.Fatalf("want 2 results, got %d", len(repos))
	}
}

func TestCallTool_Search_WithFilters(t *testing.T) {
	svc, db := newService(t)

	seedRepo(t, db, "ghl-revex-backend", "revex", "backend")
	seedRepo(t, db, "ghl-revex-frontend", "revex", "frontend")

	result, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{
		"query": "revex",
		"scope": "backend",
		"team":  "revex",
		"limit": float64(5),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	repos, ok := result.([]orgdb.RepoSearchResult)
	if !ok {
		t.Fatalf("result type: got %T", result)
	}
	if len(repos) != 1 {
		t.Fatalf("want 1 result with scope=backend, got %d", len(repos))
	}
	if repos[0].Name != "ghl-revex-backend" {
		t.Errorf("Name: got %q, want %q", repos[0].Name, "ghl-revex-backend")
	}
}

func TestCallTool_Search_MissingArgs(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

// ---------- CallTool: org_code_search ----------

func TestCallTool_CodeSearch_FansOut(t *testing.T) {
	svc, db := newService(t)

	// Seed 3 repos with different node counts
	seedRepoWithNodeCount(t, db, "big-repo", "platform", "backend", 500)
	seedRepoWithNodeCount(t, db, "medium-repo", "platform", "backend", 200)
	seedRepoWithNodeCount(t, db, "small-repo", "platform", "backend", 50)

	mb := &mockBridge{
		handler: func(name string, params map[string]interface{}) (*mcp.ToolResult, error) {
			project, _ := params["project"].(string)
			if project == "data-fleet-cache-repos-big-repo" {
				return &mcp.ToolResult{
					Content: []mcp.Content{{Type: "text", Text: "found: Controller in big-repo"}},
				}, nil
			}
			return &mcp.ToolResult{
				Content: []mcp.Content{{Type: "text", Text: "No results found."}},
			}, nil
		},
	}
	svc.SetBridge(mb)

	result, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
		"pattern": "@Controller",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	results, ok := result.([]CodeSearchResult)
	if !ok {
		t.Fatalf("result type: got %T, want []CodeSearchResult", result)
	}

	// Should have 1 result (big-repo matched, others returned "No results found.")
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Project != "big-repo" {
		t.Errorf("Project: got %q, want %q", results[0].Project, "big-repo")
	}

	// Verify the bridge was called 3 times (once per repo)
	if len(mb.calls) != 3 {
		t.Errorf("bridge calls: want 3, got %d", len(mb.calls))
	}

	// Verify @ was stripped from pattern
	for _, call := range mb.calls {
		pattern, _ := call.Params["pattern"].(string)
		if pattern != "controller" { // lowercase because case_insensitive defaults to true
			t.Errorf("pattern not normalized: got %q, want %q", pattern, "controller")
		}
	}
}

func TestCallTool_CodeSearch_CaseSensitive(t *testing.T) {
	svc, db := newService(t)

	seedRepoWithNodeCount(t, db, "test-repo", "team", "backend", 100)

	mb := &mockBridge{
		handler: func(name string, params map[string]interface{}) (*mcp.ToolResult, error) {
			return &mcp.ToolResult{
				Content: []mcp.Content{{Type: "text", Text: "No results found."}},
			}, nil
		},
	}
	svc.SetBridge(mb)

	_, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
		"pattern":          "MyController",
		"case_insensitive": false,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	// Verify pattern was NOT lowercased
	if len(mb.calls) != 1 {
		t.Fatalf("bridge calls: want 1, got %d", len(mb.calls))
	}
	pattern, _ := mb.calls[0].Params["pattern"].(string)
	if pattern != "MyController" {
		t.Errorf("pattern: got %q, want %q", pattern, "MyController")
	}
}

func TestCallTool_CodeSearch_MissingPattern(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestCallTool_CodeSearch_NoBridge(t *testing.T) {
	svc, db := newService(t)
	seedRepoWithNodeCount(t, db, "test-repo", "team", "backend", 100)
	// Don't set bridge

	_, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
		"pattern": "test",
	})
	if err == nil {
		t.Fatal("expected error when bridge not configured")
	}
}

func TestCallTool_CodeSearch_NoRepos(t *testing.T) {
	svc, _ := newService(t)
	mb := &mockBridge{}
	svc.SetBridge(mb)

	result, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
		"pattern": "test",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	results, ok := result.([]CodeSearchResult)
	if !ok {
		t.Fatalf("result type: got %T, want []CodeSearchResult", result)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results for empty org, got %d", len(results))
	}
	if len(mb.calls) != 0 {
		t.Errorf("bridge calls: want 0, got %d", len(mb.calls))
	}
}

func TestCallTool_CodeSearch_BridgeError(t *testing.T) {
	svc, db := newService(t)
	seedRepoWithNodeCount(t, db, "error-repo", "team", "backend", 100)

	mb := &mockBridge{
		handler: func(name string, params map[string]interface{}) (*mcp.ToolResult, error) {
			return nil, fmt.Errorf("bridge timeout")
		},
	}
	svc.SetBridge(mb)

	result, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
		"pattern": "test",
	})
	if err != nil {
		t.Fatalf("CallTool should not fail entirely: %v", err)
	}

	results, ok := result.([]CodeSearchResult)
	if !ok {
		t.Fatalf("result type: got %T", result)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 error result, got %d", len(results))
	}
	if !results[0].IsError {
		t.Error("expected IsError=true for bridge failure")
	}
}

func TestCallTool_CodeSearch_MaxReposCapped(t *testing.T) {
	svc, db := newService(t)

	// Seed 3 repos
	seedRepoWithNodeCount(t, db, "repo-a", "team", "backend", 300)
	seedRepoWithNodeCount(t, db, "repo-b", "team", "backend", 200)
	seedRepoWithNodeCount(t, db, "repo-c", "team", "backend", 100)

	mb := &mockBridge{
		handler: func(name string, params map[string]interface{}) (*mcp.ToolResult, error) {
			return &mcp.ToolResult{
				Content: []mcp.Content{{Type: "text", Text: "No results found."}},
			}, nil
		},
	}
	svc.SetBridge(mb)

	_, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
		"pattern":   "test",
		"max_repos": float64(2),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	// Should only search top 2 repos
	if len(mb.calls) != 2 {
		t.Errorf("bridge calls: want 2, got %d", len(mb.calls))
	}
}

// ---------- NormalizePattern ----------

func TestNormalizePattern_StripsAt(t *testing.T) {
	got := NormalizePattern("@Controller", false)
	if got != "Controller" {
		t.Errorf("got %q, want %q", got, "Controller")
	}
}

func TestNormalizePattern_CaseInsensitive(t *testing.T) {
	got := NormalizePattern("@Controller", true)
	if got != "controller" {
		t.Errorf("got %q, want %q", got, "controller")
	}
}

func TestNormalizePattern_NoAt(t *testing.T) {
	got := NormalizePattern("handlePayment", false)
	if got != "handlePayment" {
		t.Errorf("got %q, want %q", got, "handlePayment")
	}
}

// ---------- CallTool: unknown tool ----------

func TestCallTool_UnknownTool(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CallTool(context.Background(), "unknown_tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallTool_WaitsForWarmup(t *testing.T) {
	svc, _ := newService(t)
	wantErr := errors.New("still warming")
	called := false
	svc.SetWarmupWaiter(func(_ context.Context, toolName string) error {
		called = true
		if toolName != "org_search" {
			t.Fatalf("toolName: got %q, want %q", toolName, "org_search")
		}
		return wantErr
	})

	_, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{
		"query": "revex",
	})
	if !called {
		t.Fatal("expected warmup waiter to be called")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("CallTool error: got %v, want %v", err, wantErr)
	}
}
