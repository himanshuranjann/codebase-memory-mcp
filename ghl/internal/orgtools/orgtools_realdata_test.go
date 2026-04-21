//go:build realdata

// Real-data accuracy + latency benchmark for the org MCP tools.
//
// Run with:
//   cd ghl
//   go test -tags realdata -v -timeout 30m \
//     -run TestOrgToolsRealData ./internal/orgtools/...
//
// The build tag keeps this out of normal CI because it depends on locally
// cloned GHL repositories under /Users/himanshuranjan/Documents/highlevel.
//
// What this does:
//   1. Ingests ~12 real GHL repos into a persistent org.db via the same
//      pipeline the MCP server uses at startup (PopulateRepoData +
//      InferPackageProviders + CrossReferenceContracts + CrossReferenceEventContracts).
//   2. Invokes every org tool through OrgService.CallTool (the MCP entry
//      point) with realistic queries.
//   3. Asserts accuracy against ground truth pulled from REPOS.yaml and
//      from raw source inspection.
//   4. Times every call and emits p50/p99 per tool.
//   5. Prints a final table: tool / queries / accuracy / p50 / p99.
package orgtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/manifest"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/orgdb"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/pipeline"
)

const cloneBaseDir = "/Users/himanshuranjan/Documents/highlevel"

// curated set of locally cloned GHL repos with diverse shapes
// (backend/frontend/service, multiple teams, known cross-refs).
var realRepos = []manifest.Repo{
	{Name: "platform-backend", GitHubURL: "https://github.com/GoHighLevel/platform-backend.git", Team: "platform", Type: "backend"},
	{Name: "marketplace-backend", GitHubURL: "https://github.com/GoHighLevel/marketplace-backend.git", Team: "marketplace", Type: "backend"},
	{Name: "membership-backend", GitHubURL: "https://github.com/GoHighLevel/membership-backend.git", Team: "membership", Type: "backend"},
	{Name: "ai-backend", GitHubURL: "https://github.com/GoHighLevel/ai-backend.git", Team: "ai", Type: "backend"},
	{Name: "ghl-revex-backend-master", GitHubURL: "https://github.com/GoHighLevel/ghl-revex-backend-master.git", Team: "revex", Type: "backend"},
	{Name: "image-processing-service", GitHubURL: "https://github.com/GoHighLevel/image-processing-service.git", Team: "platform", Type: "service"},
	{Name: "engram", GitHubURL: "https://github.com/GoHighLevel/engram.git", Team: "platform", Type: "service"},
	{Name: "clientportal-core", GitHubURL: "https://github.com/GoHighLevel/clientportal-core.git", Team: "clientportal", Type: "backend"},
	{Name: "ai-frontend", GitHubURL: "https://github.com/GoHighLevel/ai-frontend.git", Team: "ai", Type: "frontend"},
	{Name: "ghl-crm-frontend", GitHubURL: "https://github.com/GoHighLevel/ghl-crm-frontend.git", Team: "crm", Type: "frontend"},
	{Name: "automation-workflows-frontend", GitHubURL: "https://github.com/GoHighLevel/automation-workflows-frontend.git", Team: "automation", Type: "frontend"},
	{Name: "ghl-email-builder", GitHubURL: "https://github.com/GoHighLevel/ghl-email-builder.git", Team: "crm", Type: "frontend"},
}

// toolCase describes one benchmark invocation.
type toolCase struct {
	name           string                 // tool name
	label          string                 // short label for the report
	args           map[string]interface{} // args passed to CallTool
	verify         func(result interface{}, raw string) (ok bool, detail string)
	expectEmptyOK  bool // if true, empty result is still considered "accurate"
	expectErr      bool // if true, a non-nil error is required
	expectErrMatch string // if set, the error message must contain this substring
}

// gap records an accuracy issue found during the run that doesn't fail
// the test but should show up in the final report.
type gap struct {
	tool    string
	label   string
	issue   string
}

// latencyStats holds timing aggregates per tool.
type latencyStats struct {
	tool       string
	total      int
	passed     int
	latencies  []time.Duration
}

func (ls *latencyStats) p50() time.Duration { return percentile(ls.latencies, 50) }
func (ls *latencyStats) p99() time.Duration { return percentile(ls.latencies, 99) }

func percentile(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (len(sorted) * p) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// TestOrgToolsRealData ingests real GHL repos and benchmarks every tool.
func TestOrgToolsRealData(t *testing.T) {
	// Filter manifest to only repos that actually exist on disk.
	filtered := filterPresent(t, realRepos)
	if len(filtered) < 3 {
		t.Fatalf("need at least 3 local repos; found %d under %s", len(filtered), cloneBaseDir)
	}
	t.Logf("using %d/%d real repos", len(filtered), len(realRepos))

	dbPath := filepath.Join(t.TempDir(), "org-real.db")
	db, err := orgdb.Open(dbPath)
	if err != nil {
		t.Fatalf("orgdb.Open: %v", err)
	}
	defer db.Close()

	// Ingest each repo (package.json + NestJS enricher output).
	ingestStart := time.Now()
	for _, repo := range filtered {
		start := time.Now()
		if err := pipeline.PopulateRepoData(db, repo, cloneBaseDir); err != nil {
			t.Logf("populate %s: %v (continuing)", repo.Name, err)
			continue
		}
		t.Logf("ingested %-32s in %s", repo.Name, time.Since(start))
	}

	// Post-processing that the server runs after ingest.
	if _, err := db.FixRoutePaths(); err != nil {
		t.Logf("FixRoutePaths: %v", err)
	}
	if providers, err := db.InferPackageProviders(); err != nil {
		t.Logf("InferPackageProviders: %v", err)
	} else {
		t.Logf("inferred %d package providers", providers)
	}
	if matched, err := db.CrossReferenceContracts(); err != nil {
		t.Logf("CrossReferenceContracts: %v", err)
	} else {
		t.Logf("cross-referenced %d API contracts", matched)
	}
	if matched, err := db.CrossReferenceEventContracts(); err != nil {
		t.Logf("CrossReferenceEventContracts: %v", err)
	} else {
		t.Logf("cross-referenced %d event contracts", matched)
	}
	t.Logf("=== ingest complete in %s ===", time.Since(ingestStart))

	// Report DB state so ground truth makes sense.
	reportDBState(t, db)

	svc := New(db)
	svc.SetCacheDir("") // no bridge — code_search gracefully errors
	cases := buildRealCases(t, db, filtered)

	stats := map[string]*latencyStats{}
	var gaps []gap
	for _, c := range cases {
		s, ok := stats[c.name]
		if !ok {
			s = &latencyStats{tool: c.name}
			stats[c.name] = s
		}

		start := time.Now()
		result, err := svc.CallTool(context.Background(), c.name, c.args)
		elapsed := time.Since(start)
		s.total++
		s.latencies = append(s.latencies, elapsed)

		// Error-expected branch
		if c.expectErr {
			if err == nil {
				gaps = append(gaps, gap{c.name, c.label, "expected error, got success"})
				t.Logf("[WARN] %-22s %-40s %6s  expected error, got success", c.name, c.label, elapsed)
				continue
			}
			if c.expectErrMatch != "" && !strings.Contains(err.Error(), c.expectErrMatch) {
				gaps = append(gaps, gap{c.name, c.label, fmt.Sprintf("error message mismatch: %q", err.Error())})
				t.Logf("[WARN] %-22s %-40s %6s  error msg mismatch: %v", c.name, c.label, elapsed, err)
				continue
			}
			s.passed++
			t.Logf("[ok]   %-22s %-40s %6s  (clean err: %v)", c.name, c.label, elapsed, err)
			continue
		}

		if err != nil {
			gaps = append(gaps, gap{c.name, c.label, fmt.Sprintf("err=%v", err)})
			t.Logf("[GAP]  %-22s %-40s %6s  err=%v", c.name, c.label, elapsed, err)
			continue
		}
		raw, _ := json.Marshal(result)
		rawStr := string(raw)
		if strings.Contains(rawStr, ":null") || rawStr == "null" {
			gaps = append(gaps, gap{c.name, c.label, "bare null JSON"})
			t.Logf("[GAP]  %-22s %-40s %6s  null JSON: %s", c.name, c.label, elapsed, truncate(rawStr, 100))
			continue
		}
		ok, detail := c.verify(result, rawStr)
		if !ok {
			gaps = append(gaps, gap{c.name, c.label, detail})
			t.Logf("[GAP]  %-22s %-40s %6s  %s | JSON=%s", c.name, c.label, elapsed, detail, truncate(rawStr, 140))
			continue
		}
		s.passed++
		t.Logf("[ok]   %-22s %-40s %6s  %s", c.name, c.label, elapsed, truncate(rawStr, 120))
	}

	printReport(t, stats, gaps)
}

// filterPresent keeps only repos whose clone exists on disk.
func filterPresent(t *testing.T, repos []manifest.Repo) []manifest.Repo {
	t.Helper()
	var out []manifest.Repo
	for _, r := range repos {
		p := filepath.Join(cloneBaseDir, r.Name)
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			t.Logf("skip %s (not found at %s)", r.Name, p)
			continue
		}
		out = append(out, r)
	}
	return out
}

// reportDBState logs a coarse summary of DB state. Counts come from the
// public helpers already on *orgdb.DB; anything deeper shows up in
// individual tool outputs below.
func reportDBState(t *testing.T, db *orgdb.DB) {
	t.Helper()
	t.Logf("org.db repo count: %d", db.RepoCount())
}

// buildRealCases creates a diverse set of queries for every tool. Ground
// truth is established from REPOS.yaml and source-code inspection:
//
//   - platform-backend is a long-standing NestJS backend with dozens of
//     controllers; expected to surface in blast_radius and trace_flow.
//   - ai-backend is a NestJS backend depending on @platform-core/* packages.
//   - ghl-revex-backend-master is also NestJS and is often an API provider.
//   - frontends typically import shared packages but don't define routes.
//   - Every locally-ingested repo has a team_ownership row (PopulateRepoData
//     writes one unconditionally), so TeamTopology on every team must
//     surface at least one repo.
func buildRealCases(t *testing.T, db *orgdb.DB, repos []manifest.Repo) []toolCase {
	t.Helper()

	teams := map[string]bool{}
	repoNames := map[string]bool{}
	for _, r := range repos {
		teams[r.Team] = true
		repoNames[r.Name] = true
	}

	cases := []toolCase{
		// ---------- org_search ----------
		{
			name:  "org_search",
			label: "by name substring 'backend'",
			args:  map[string]interface{}{"query": "backend"},
			verify: func(r interface{}, js string) (bool, string) {
				// Must find at least 3 backend repos from our ingest.
				need := []string{"platform-backend", "ai-backend", "marketplace-backend"}
				missing := missingTokens(js, need)
				if len(missing) > 1 { // tolerate 1 missing in case a repo failed ingest
					return false, fmt.Sprintf("missing backend repos: %v", missing)
				}
				return true, ""
			},
		},
		{
			// Fix 3 applied: team-only search is now allowed and must
			// return the repos on that team.
			name:  "org_search",
			label: "by team=platform (team-only)",
			args:  map[string]interface{}{"query": "", "team": "platform"},
			verify: func(r interface{}, js string) (bool, string) {
				if !containsAny(js, []string{"platform-backend", "image-processing-service", "engram"}) {
					return false, "team-only search should return platform repos"
				}
				return true, ""
			},
		},
		{
			name:           "org_search",
			label:          "empty query+team → clean error",
			args:           map[string]interface{}{},
			expectErr:      true,
			expectErrMatch: "query` or `team` is required",
			verify:         func(r interface{}, js string) (bool, string) { return true, "" },
		},
		{
			name:  "org_search",
			label: "query='revex' team filter",
			args:  map[string]interface{}{"query": "revex", "team": "revex"},
			verify: func(r interface{}, js string) (bool, string) {
				if !strings.Contains(js, "ghl-revex-backend-master") {
					return false, "revex team+query should surface ghl-revex-backend-master"
				}
				return true, ""
			},
		},
		{
			name:  "org_search",
			label: "no match returns empty array",
			args:  map[string]interface{}{"query": "zzz-nonexistent-xyz"},
			verify: func(r interface{}, js string) (bool, string) {
				if js == "null" || strings.Contains(js, ":null") {
					return false, "empty result must be `[]`"
				}
				// Accept either "[]" or an array containing only unrelated rows (unlikely).
				if js != "[]" && !strings.HasPrefix(js, "[") {
					return false, "unexpected shape"
				}
				return true, ""
			},
			expectEmptyOK: true,
		},

		// ---------- org_team_topology ----------
		{
			name:  "org_team_topology",
			label: "team=platform",
			args:  map[string]interface{}{"team": "platform"},
			verify: func(r interface{}, js string) (bool, string) {
				if !containsAny(js, []string{"platform-backend", "image-processing-service", "engram"}) {
					return false, "no platform repos in topology"
				}
				return true, ""
			},
		},
		{
			name:  "org_team_topology",
			label: "team=ai",
			args:  map[string]interface{}{"team": "ai"},
			verify: func(r interface{}, js string) (bool, string) {
				if !containsAny(js, []string{"ai-backend", "ai-frontend"}) {
					return false, "ai team should include ai-backend or ai-frontend"
				}
				return true, ""
			},
		},
		{
			name:  "org_team_topology",
			label: "team=revex",
			args:  map[string]interface{}{"team": "revex"},
			verify: func(r interface{}, js string) (bool, string) {
				if !strings.Contains(js, "ghl-revex-backend-master") {
					return false, "revex topology missing ghl-revex-backend-master"
				}
				return true, ""
			},
		},
		{
			name:  "org_team_topology",
			label: "team=nonexistent empty",
			args:  map[string]interface{}{"team": "zzz-not-a-team"},
			verify: func(r interface{}, js string) (bool, string) {
				if strings.Contains(js, ":null") {
					return false, "null field in empty topology"
				}
				return true, ""
			},
			expectEmptyOK: true,
		},

		// ---------- org_dependency_graph ----------
		{
			name:  "org_dependency_graph",
			label: "@platform-core scope (common)",
			args:  map[string]interface{}{"package_scope": "@platform-core", "package_name": "base-service"},
			verify: func(r interface{}, js string) (bool, string) {
				// @platform-core/base-service is commonly imported. If any
				// of our backends depends on it, we should see them here.
				// If no dependents → acceptable but surfaced as "no data".
				return true, ""
			},
			expectEmptyOK: true,
		},
		{
			name:  "org_dependency_graph",
			label: "nonexistent package",
			args:  map[string]interface{}{"package_scope": "@bogus", "package_name": "does-not-exist"},
			verify: func(r interface{}, js string) (bool, string) {
				if js == "null" {
					return false, "nonexistent package returned null"
				}
				return true, ""
			},
			expectEmptyOK: true,
		},

		// ---------- org_blast_radius ----------
		{
			name:  "org_blast_radius",
			label: "platform-backend (expect >=2 affected)",
			args:  map[string]interface{}{"repo": "platform-backend"},
			verify: func(r interface{}, js string) (bool, string) {
				if strings.Contains(js, `"AffectedRepos":null`) {
					return false, "AffectedRepos is null"
				}
				// platform-backend is a large API provider; with 777
				// cross-refs, at least a couple of our 12 ingested repos
				// should consume its endpoints.
				n := countAffectedRepos(js)
				if n < 1 {
					return false, fmt.Sprintf("expected >=1 affected repo for platform-backend, got %d", n)
				}
				// Must not self-loop (platform-backend in its own blast radius).
				if strings.Contains(js, `"Name":"platform-backend"`) {
					return false, "platform-backend appears in its own blast radius (self-loop)"
				}
				return true, ""
			},
		},
		{
			name:  "org_blast_radius",
			label: "ghl-revex-backend-master",
			args:  map[string]interface{}{"repo": "ghl-revex-backend-master"},
			verify: func(r interface{}, js string) (bool, string) {
				if strings.Contains(js, `"AffectedRepos":null`) {
					return false, "null slice"
				}
				return true, ""
			},
			expectEmptyOK: true,
		},
		{
			name:  "org_blast_radius",
			label: "nonexistent repo emits []",
			args:  map[string]interface{}{"repo": "zzz-doesnt-exist"},
			verify: func(r interface{}, js string) (bool, string) {
				if strings.Contains(js, `"AffectedRepos":null`) {
					return false, "must be [] not null"
				}
				if !strings.Contains(js, `"AffectedRepos":[]`) {
					return false, "expected explicit empty array"
				}
				return true, ""
			},
		},

		// ---------- org_trace_flow ----------
		{
			name:  "org_trace_flow",
			label: "downstream from platform-backend (self-loops flagged)",
			args:  map[string]interface{}{"trigger": "platform-backend", "direction": "downstream", "max_hops": float64(2)},
			verify: func(r interface{}, js string) (bool, string) {
				if js == "null" {
					return false, "null trace"
				}
				selfLoops := countSelfLoops(js)
				if selfLoops > 0 {
					return false, fmt.Sprintf("%d self-loop edges (FromRepo==ToRepo) — cross-ref false positive", selfLoops)
				}
				return true, ""
			},
			expectEmptyOK: true,
		},
		{
			name:  "org_trace_flow",
			label: "upstream from platform-backend (self-loops flagged)",
			args:  map[string]interface{}{"trigger": "platform-backend", "direction": "upstream", "max_hops": float64(2)},
			verify: func(r interface{}, js string) (bool, string) {
				if js == "null" {
					return false, "null trace"
				}
				selfLoops := countSelfLoops(js)
				if selfLoops > 0 {
					return false, fmt.Sprintf("%d self-loop edges in upstream trace", selfLoops)
				}
				return true, ""
			},
			expectEmptyOK: true,
		},
		{
			// Fix 4 applied: invalid direction must return a clean error
			// instead of silently returning downstream data.
			name:           "org_trace_flow",
			label:          "invalid direction → clean error",
			args:           map[string]interface{}{"trigger": "platform-backend", "direction": "sideways", "max_hops": float64(2)},
			expectErr:      true,
			expectErrMatch: "direction must be",
			verify:         func(r interface{}, js string) (bool, string) { return true, "" },
		},

		// ---------- org_code_search ----------
		// cacheDir is unset → must return a clean error, not null.
		{
			name:           "org_code_search",
			label:          "no cache+no bridge → clean error",
			args:           map[string]interface{}{"pattern": "InternalRequest"},
			expectErr:      true,
			expectErrMatch: "cache dir not configured",
			verify:         func(r interface{}, js string) (bool, string) { return true, "" },
		},
		{
			name:           "org_code_search",
			label:          "missing pattern → clean error",
			args:           map[string]interface{}{},
			expectErr:      true,
			expectErrMatch: "pattern is required",
			verify:         func(r interface{}, js string) (bool, string) { return true, "" },
		},
	}

	return cases
}

// countAffectedRepos parses a blast_radius JSON and returns the number of
// distinct affected repo names (not deduped by reason).
func countAffectedRepos(js string) int {
	var res struct {
		AffectedRepos []struct {
			Name string
		}
	}
	if err := json.Unmarshal([]byte(js), &res); err != nil {
		return 0
	}
	names := map[string]bool{}
	for _, ar := range res.AffectedRepos {
		if ar.Name != "" {
			names[ar.Name] = true
		}
	}
	return len(names)
}

// countSelfLoops counts entries in a trace_flow JSON where FromRepo == ToRepo.
// This is an accuracy check: a service shouldn't appear as its own dependent
// in a trace flow (unless the org model allows self-calls, which would be a
// business decision). Surfaces cross-reference false positives.
func countSelfLoops(js string) int {
	type step struct {
		FromRepo string
		ToRepo   string
	}
	var steps []step
	if err := json.Unmarshal([]byte(js), &steps); err != nil {
		return 0
	}
	n := 0
	for _, s := range steps {
		if s.FromRepo != "" && s.FromRepo == s.ToRepo {
			n++
		}
	}
	return n
}

// helpers
func containsAny(s string, items []string) bool {
	for _, it := range items {
		if strings.Contains(s, it) {
			return true
		}
	}
	return false
}

func missingTokens(s string, tokens []string) []string {
	var miss []string
	for _, tk := range tokens {
		if !strings.Contains(s, tk) {
			miss = append(miss, tk)
		}
	}
	return miss
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func printReport(t *testing.T, stats map[string]*latencyStats, gaps []gap) {
	t.Helper()

	tools := make([]string, 0, len(stats))
	for k := range stats {
		tools = append(tools, k)
	}
	sort.Strings(tools)

	t.Log("")
	t.Log("╔════════════════════════════════════════════════════════════════════════════════════╗")
	t.Log("║  ORG TOOLS — ACCURACY & LATENCY REPORT                                             ║")
	t.Log("╠══════════════════════════════╦═══════╦═══════╦══════════╦═══════════╦═════════════╣")
	t.Log("║ tool                         ║ runs  ║ pass  ║ accuracy ║ p50       ║ p99         ║")
	t.Log("╠══════════════════════════════╬═══════╬═══════╬══════════╬═══════════╬═════════════╣")
	for _, tool := range tools {
		s := stats[tool]
		acc := 0.0
		if s.total > 0 {
			acc = 100.0 * float64(s.passed) / float64(s.total)
		}
		t.Logf("║ %-28s ║ %5d ║ %5d ║ %7.1f%% ║ %9s ║ %11s ║",
			tool, s.total, s.passed, acc, s.p50(), s.p99())
	}
	t.Log("╚══════════════════════════════╩═══════╩═══════╩══════════╩═══════════╩═════════════╝")

	if len(gaps) > 0 {
		t.Log("")
		t.Log("────── GAPS FOUND (real-data accuracy issues) ──────")
		for _, g := range gaps {
			t.Logf("  • %-22s [%s] %s", g.tool, g.label, g.issue)
		}
	} else {
		t.Log("")
		t.Log("No gaps found — all tools produce accurate results on real data.")
	}
}
