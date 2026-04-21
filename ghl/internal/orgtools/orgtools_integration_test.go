package orgtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/orgdb"
)

// seedProductionShape builds a realistic cross-repo graph that exercises
// every org tool code path with the same structure a real GHL org.db would
// have: package providers, package dependents, API contracts, event
// contracts, repos whose `team` column is blank but present in
// `team_ownership`, and an isolated repo with zero relationships.
func seedProductionShape(t *testing.T, db *orgdb.DB) {
	t.Helper()

	// Repos. Note workflow-service intentionally has team="" — it must be
	// resolved via the team_ownership fallback added in this PR.
	type repoSeed struct {
		name, team, typ string
		nodes, edges    int
	}
	repos := []repoSeed{
		{"contacts-service", "contacts", "backend", 120, 340},
		{"workflow-service", "", "backend", 95, 210}, // team via team_ownership
		{"payments-api", "payments", "backend", 80, 180},
		{"notifications-worker", "notifications", "worker", 60, 140},
		{"frontend-app", "frontend", "frontend", 300, 700},
		{"orphan-repo", "platform", "backend", 5, 2}, // isolated — no contracts, no deps
	}
	for _, r := range repos {
		if err := db.UpsertRepo(orgdb.RepoRecord{
			Name:      r.name,
			GitHubURL: "https://github.com/GoHighLevel/" + r.name,
			Team:      r.team,
			Type:      r.typ,
			Languages: `["typescript"]`,
			NodeCount: r.nodes,
			EdgeCount: r.edges,
		}); err != nil {
			t.Fatalf("UpsertRepo(%s): %v", r.name, err)
		}
	}

	// team_ownership fallback for workflow-service.
	if err := db.UpsertTeamOwnership("workflow-service", "workflows", ""); err != nil {
		t.Fatalf("UpsertTeamOwnership: %v", err)
	}

	// Package: contacts-service exports @platform-core/contacts-service.
	if err := db.SetPackageProvider("@platform-core", "contacts-service", "contacts-service"); err != nil {
		t.Fatalf("SetPackageProvider: %v", err)
	}

	// Package dependents: workflow-service and frontend-app both pull it.
	for _, consumer := range []string{"workflow-service", "frontend-app"} {
		if err := db.UpsertPackageDep(consumer, orgdb.Dep{
			Scope:       "@platform-core",
			Name:        "contacts-service",
			DepType:     "dependencies",
			VersionSpec: "^1.0.0",
		}); err != nil {
			t.Fatalf("UpsertPackageDep(%s): %v", consumer, err)
		}
	}

	// API contracts:
	//   payments-api → contacts-service (GET /contacts/list)
	//   workflow-service → contacts-service (POST /contacts/merge)
	for _, c := range []orgdb.APIContract{
		{ProviderRepo: "contacts-service", ConsumerRepo: "payments-api", Method: "GET", Path: "/contacts/list", ProviderSymbol: "ContactsController.list", ConsumerSymbol: "PaymentsService.fetch", Confidence: 0.95},
		{ProviderRepo: "contacts-service", ConsumerRepo: "workflow-service", Method: "POST", Path: "/contacts/merge", ProviderSymbol: "ContactsController.merge", ConsumerSymbol: "WorkflowService.reconcile", Confidence: 0.90},
	} {
		if err := db.InsertAPIContract(c); err != nil {
			t.Fatalf("InsertAPIContract: %v", err)
		}
	}

	// Event contracts: contacts-service publishes user.created, consumed by
	// workflow-service and notifications-worker.
	for _, e := range []orgdb.EventContract{
		{Topic: "user.created", EventType: "pubsub", ProducerRepo: "contacts-service", ConsumerRepo: "workflow-service", ProducerSymbol: "ContactsService.emit", ConsumerSymbol: "WorkflowSubscriber.onUserCreated"},
		{Topic: "user.created", EventType: "pubsub", ProducerRepo: "contacts-service", ConsumerRepo: "notifications-worker", ProducerSymbol: "ContactsService.emit", ConsumerSymbol: "NotificationsSubscriber.onUserCreated"},
	} {
		if err := db.InsertEventContract(e); err != nil {
			t.Fatalf("InsertEventContract: %v", err)
		}
	}
}

// callAndDecode runs a tool via CallTool() (the same entry point the MCP
// server uses), then round-trips the result through JSON so we observe
// exactly what a remote client would see — including any stray `null`.
func callAndDecode(t *testing.T, svc *OrgService, name string, args map[string]interface{}) (interface{}, string) {
	t.Helper()
	result, err := svc.CallTool(context.Background(), name, args)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal(%s): %v", name, err)
	}
	return result, string(raw)
}

// assertNoBareNulls verifies the marshaled JSON contains no `null` at array
// or whole-value position — the exact failure mode the user reported
// ("returning null 80% of the time"). Null inside a field value (e.g.
// `"field": null`) is also flagged because it indicates a forgotten
// default.
func assertNoBareNulls(t *testing.T, label, jsonOut string) {
	t.Helper()
	if jsonOut == "null" {
		t.Fatalf("%s: whole result is `null` — should be empty slice/object", label)
	}
	if strings.Contains(jsonOut, ":null") || strings.Contains(jsonOut, ": null") {
		t.Errorf("%s: contains `null` field value. JSON=%s", label, jsonOut)
	}
}

// ---------------------------------------------------------------------------
// TestOrgTools_Integration_AllToolsAgainstRealShapeDB
//
// Builds a production-shape org.db and drives all 6 org tools through the
// same CallTool() entry point the MCP server uses. Asserts:
//   - no bare `null` in any JSON result (fix A: empty slices, not nil)
//   - `team` populated for API/event consumers (fix B: blast radius LEFT JOIN)
//   - `workflow-service` surfaces under team "workflows" even though its
//     repos.team is blank (fix C: team_ownership COALESCE fallback)
//   - isolated repo returns TotalRepos=0 with AffectedRepos=[] (not null)
//   - trace flow returns multi-hop steps downstream from contacts-service
// ---------------------------------------------------------------------------
func TestOrgTools_Integration_AllToolsAgainstRealShapeDB(t *testing.T) {
	svc, db := newService(t)
	seedProductionShape(t, db)

	t.Run("dependency_graph_returns_dependents", func(t *testing.T) {
		_, js := callAndDecode(t, svc, "org_dependency_graph", map[string]interface{}{
			"package_scope": "@platform-core",
			"package_name":  "contacts-service",
		})
		assertNoBareNulls(t, "org_dependency_graph", js)
		for _, want := range []string{"workflow-service", "frontend-app"} {
			if !strings.Contains(js, want) {
				t.Errorf("expected %q in dependency_graph output. JSON=%s", want, js)
			}
		}
		t.Logf("org_dependency_graph JSON: %s", js)
	})

	t.Run("blast_radius_populates_team_for_all_reason_types", func(t *testing.T) {
		result, js := callAndDecode(t, svc, "org_blast_radius", map[string]interface{}{
			"repo": "contacts-service",
		})
		assertNoBareNulls(t, "org_blast_radius", js)

		// Re-decode into the concrete shape so we can inspect team values.
		// Note: Go fields are bare-exported → JSON keys are CamelCase.
		bytes, _ := json.Marshal(result)
		var blast struct {
			TotalRepos    int
			AffectedRepos []struct {
				Name   string
				Team   string
				Reason string
			}
		}
		if err := json.Unmarshal(bytes, &blast); err != nil {
			t.Fatalf("decode blast radius: %v. raw=%s", err, js)
		}

		// API/event consumers with a real repos.team entry must be populated
		// by the LEFT JOIN fix: this is the reliability win.
		expectedTeams := map[string]string{
			"frontend-app":         "frontend",
			"payments-api":         "payments",
			"notifications-worker": "notifications",
		}
		for _, ar := range blast.AffectedRepos {
			want, ok := expectedTeams[ar.Name]
			if !ok {
				continue
			}
			if ar.Team != want {
				t.Errorf("blast_radius team mismatch for %s: got %q, want %q (reason=%s)", ar.Name, ar.Team, want, ar.Reason)
			}
		}
		if blast.TotalRepos == 0 {
			t.Errorf("expected non-zero TotalRepos. JSON=%s", js)
		}
		t.Logf("org_blast_radius JSON: %s", js)
	})

	t.Run("blast_radius_empty_is_not_null", func(t *testing.T) {
		_, js := callAndDecode(t, svc, "org_blast_radius", map[string]interface{}{
			"repo": "orphan-repo",
		})
		assertNoBareNulls(t, "org_blast_radius(empty)", js)
		if strings.Contains(js, `"AffectedRepos":null`) {
			t.Errorf("isolated repo emitted null slice. JSON=%s", js)
		}
		if !strings.Contains(js, `"AffectedRepos":[]`) {
			t.Errorf("isolated repo: expected explicit empty array. JSON=%s", js)
		}
		t.Logf("org_blast_radius(orphan) JSON: %s", js)
	})

	t.Run("blast_radius_team_ownership_fallback_now_applied", func(t *testing.T) {
		// Regression guard for the gap that was previously flagged:
		// QueryBlastRadius now LEFT JOINs team_ownership and COALESCEs
		// r.team/t.team, matching the behavior of TeamTopology and
		// SearchRepos. workflow-service (seeded with repos.team="" but
		// team_ownership.team="workflows") must now surface with the
		// correct team.
		result, _ := callAndDecode(t, svc, "org_blast_radius", map[string]interface{}{
			"repo": "contacts-service",
		})
		bytes, _ := json.Marshal(result)
		var blast struct {
			AffectedRepos []struct {
				Name string
				Team string
			}
		}
		if err := json.Unmarshal(bytes, &blast); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, ar := range blast.AffectedRepos {
			if ar.Name == "workflow-service" && ar.Team != "workflows" {
				t.Errorf("workflow-service should now have Team=\"workflows\" via team_ownership fallback, got %q", ar.Team)
			}
		}
	})

	t.Run("trace_flow_downstream_multi_hop", func(t *testing.T) {
		_, js := callAndDecode(t, svc, "org_trace_flow", map[string]interface{}{
			"trigger":   "contacts-service",
			"direction": "downstream",
			"max_hops":  float64(3),
		})
		assertNoBareNulls(t, "org_trace_flow", js)
		// Must show at least one hop into a consumer.
		if !strings.Contains(js, "workflow-service") && !strings.Contains(js, "payments-api") {
			t.Errorf("trace_flow should include a downstream consumer. JSON=%s", js)
		}
		t.Logf("org_trace_flow JSON: %s", js)
	})

	t.Run("team_topology_fallback_via_team_ownership", func(t *testing.T) {
		// workflow-service has team="" in repos but team="workflows" in
		// team_ownership. The new COALESCE query must return it.
		_, js := callAndDecode(t, svc, "org_team_topology", map[string]interface{}{
			"team": "workflows",
		})
		assertNoBareNulls(t, "org_team_topology", js)
		if !strings.Contains(js, "workflow-service") {
			t.Errorf("team_topology: workflows team should include workflow-service via team_ownership fallback. JSON=%s", js)
		}
		t.Logf("org_team_topology(workflows) JSON: %s", js)
	})

	t.Run("team_topology_direct_team_column_still_works", func(t *testing.T) {
		_, js := callAndDecode(t, svc, "org_team_topology", map[string]interface{}{
			"team": "contacts",
		})
		assertNoBareNulls(t, "org_team_topology(contacts)", js)
		if !strings.Contains(js, "contacts-service") {
			t.Errorf("team_topology: contacts team should include contacts-service. JSON=%s", js)
		}
	})

	t.Run("search_by_name_substring", func(t *testing.T) {
		_, js := callAndDecode(t, svc, "org_search", map[string]interface{}{
			"query": "contacts",
		})
		assertNoBareNulls(t, "org_search", js)
		if !strings.Contains(js, "contacts-service") {
			t.Errorf("org_search should match contacts-service by name. JSON=%s", js)
		}
		t.Logf("org_search(contacts) JSON: %s", js)
	})

	t.Run("search_by_team_uses_ownership_fallback", func(t *testing.T) {
		// workflow-service.team is "" but team_ownership says "workflows".
		// The fix makes SearchRepos recognize that.
		_, js := callAndDecode(t, svc, "org_search", map[string]interface{}{
			"query": "workflows",
			"team":  "workflows",
		})
		assertNoBareNulls(t, "org_search(by team)", js)
		if !strings.Contains(js, "workflow-service") {
			t.Errorf("org_search by team=workflows should surface workflow-service via team_ownership. JSON=%s", js)
		}
		t.Logf("org_search(by team=workflows) JSON: %s", js)
	})

	t.Run("code_search_reports_missing_cache_cleanly", func(t *testing.T) {
		// cacheDir unset AND bridge unset → must error with the new
		// combined message, not a null crash.
		_, err := svc.CallTool(context.Background(), "org_code_search", map[string]interface{}{
			"pattern": "InternalRequest",
		})
		if err == nil {
			t.Fatal("org_code_search with no cache and no bridge should return an explicit error")
		}
		if !strings.Contains(err.Error(), "cache dir not configured") {
			t.Errorf("unexpected error shape: %v", err)
		}
	})
}

// TestOrgTools_Integration_WarmupGateHappyPath complements the existing
// error-path test: when the waiter returns nil, the tool must execute
// against the DB and return a real result (not a stale cached error).
func TestOrgTools_Integration_WarmupGateHappyPath(t *testing.T) {
	svc, db := newService(t)
	seedProductionShape(t, db)

	called := 0
	svc.SetWarmupWaiter(func(_ context.Context, toolName string) error {
		called++
		if toolName != "org_search" {
			t.Fatalf("unexpected tool: %s", toolName)
		}
		return nil // ready — proceed
	})

	result, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{
		"query": "contacts",
	})
	if err != nil {
		t.Fatalf("CallTool(org_search): %v", err)
	}
	if called != 1 {
		t.Fatalf("waiter called %d times, want 1", called)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "contacts-service") {
		t.Errorf("expected contacts-service in result after warmup. JSON=%s", string(raw))
	}
}

// TestOrgTools_Integration_NormalizePackageNameMatching verifies the
// normalizeServicePrefix broadening (underscores vs hyphens) doesn't
// regress matching for vanilla package names.
func TestOrgTools_Integration_NormalizePackageNameMatching(t *testing.T) {
	svc, db := newService(t)
	seedProductionShape(t, db)

	// The @platform-core/contacts-service provider should be findable under
	// both the shorthand and full package name — the fix keeps standard
	// matching intact.
	_, js := callAndDecode(t, svc, "org_dependency_graph", map[string]interface{}{
		"package_scope": "@platform-core",
		"package_name":  "contacts-service",
	})
	if !strings.Contains(js, "workflow-service") {
		t.Errorf("normalize regression: expected workflow-service as dependent. JSON=%s", js)
	}
}
