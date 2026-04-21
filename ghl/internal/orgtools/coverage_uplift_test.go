//go:build realdata

// Coverage-uplift benchmark — ingests the same 12 locally-cloned real GHL
// repos as the main realdata benchmark, then prints per-table counts for
// every new signal we now extract. Gives a concrete before/after number
// for the indexing work.
package orgtools

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/manifest"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/orgdb"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/pipeline"
)

func TestCoverageUpliftAfterIndexing(t *testing.T) {
	filtered := filterPresent(t, realRepos)
	if len(filtered) < 3 {
		t.Fatalf("need at least 3 local repos; found %d", len(filtered))
	}

	dbPath := filepath.Join(t.TempDir(), "org-uplift.db")
	db, err := orgdb.Open(dbPath)
	if err != nil {
		t.Fatalf("orgdb.Open: %v", err)
	}
	defer db.Close()

	start := time.Now()
	for _, repo := range filtered {
		if err := pipeline.PopulateRepoData(db, repo, cloneBaseDir); err != nil {
			t.Logf("populate %s: %v (continuing)", repo.Name, err)
			continue
		}
	}
	if _, err := db.InferPackageProviders(); err != nil {
		t.Logf("InferPackageProviders: %v", err)
	}
	if _, err := db.CrossReferenceContracts(); err != nil {
		t.Logf("CrossReferenceContracts: %v", err)
	}
	if _, err := db.CrossReferenceEventContracts(); err != nil {
		t.Logf("CrossReferenceEventContracts: %v", err)
	}
	t.Logf("ingest+cross-ref complete in %s (repos=%d)", time.Since(start), len(filtered))

	// Counts across every table we populate. The pre-change baseline
	// (what the code would have produced without this PR) is shown in
	// the "was" column as a hardcoded reference from the previous run.
	rows := []struct {
		label, tier string
		was         int
		got         int
	}{
		{"repos", "-", 12, db.RepoCount()},
		{"api_contracts (all)", "existing", 5595 + 2747, countAPIContracts(t, db)},
		{"event_contracts (pubsub classic)", "existing", 0, db.CountEventsByType("pubsub")},
		{"event_contracts (cloudtask)", "T1C", 0, db.CountEventsByType("cloudtask")},
		{"event_contracts (bullmq)", "T2E", 0, db.CountEventsByType("bullmq")},
		{"event_contracts (redis)", "T2F", 0, db.CountEventsByType("redis")},
		{"event_contracts (websocket)", "T3H", 0, db.CountEventsByType("websocket")},
		{"scheduled_jobs", "T1B", 0, db.CountScheduledJobs()},
		{"http_client_calls", "T2G", 0, db.CountHttpClientCalls()},
		{"grpc_methods", "T3I", 0, db.CountGrpcMethods()},
		{"graphql_ops", "T3J", 0, db.CountGraphQLOps()},
		{"deployments", "T3K", 0, db.CountDeployments()},
		{"service_mesh", "T3L", 0, db.CountServiceMesh()},
		{"shared_databases", "T3M", 0, db.CountSharedDatabases()},
	}

	t.Log("")
	t.Log("┌─────────────────────────────────────────────────┬─────────┬──────────┬──────────┬────────────┐")
	t.Log("│ table / event_type                              │ tier    │ was      │ now      │ delta      │")
	t.Log("├─────────────────────────────────────────────────┼─────────┼──────────┼──────────┼────────────┤")
	anyPositive := false
	for _, r := range rows {
		delta := r.got - r.was
		mark := " "
		if delta > 0 {
			mark = "+"
			anyPositive = true
		}
		t.Logf("│ %-47s │ %-7s │ %8d │ %8d │ %s%9d │", r.label, r.tier, r.was, r.got, mark, delta)
	}
	t.Log("└─────────────────────────────────────────────────┴─────────┴──────────┴──────────┴────────────┘")

	// Success criterion: at least 4 of the new tables (tier-1/2/3) show
	// uplift against the baseline. Which specific patterns surface
	// depends on what the 12 ingested repos happen to use — GHL repos
	// heavily use Helm / GraphQL / Mongo but rarely use BullMQ, so we
	// don't require every single signal type to fire.
	newTableCounts := []int{
		db.CountScheduledJobs(),
		db.CountHttpClientCalls(),
		db.CountGrpcMethods(),
		db.CountGraphQLOps(),
		db.CountDeployments(),
		db.CountServiceMesh(),
		db.CountSharedDatabases(),
		db.CountEventsByType("cloudtask"),
		db.CountEventsByType("bullmq"),
		db.CountEventsByType("redis"),
		db.CountEventsByType("websocket"),
	}
	nonzero := 0
	for _, n := range newTableCounts {
		if n > 0 {
			nonzero++
		}
	}
	if nonzero < 4 {
		t.Errorf("coverage uplift too narrow: only %d/11 new tables non-zero; expected >=4", nonzero)
	}
	// Must at minimum have Helm (every repo has it), data access, and
	// HTTP client calls — those are universal across GHL backends.
	if db.CountDeployments() == 0 {
		t.Error("expected >0 deployments rows (helm extractor)")
	}
	if db.CountSharedDatabases() == 0 {
		t.Error("expected >0 shared_databases rows (data access extractor)")
	}
	if db.CountHttpClientCalls() == 0 {
		t.Error("expected >0 http_client_calls rows (axios/httpService/fetch)")
	}
	if !anyPositive {
		t.Error("coverage uplift is all-zeros — the indexing changes are not being applied")
	}
}

func countAPIContracts(t *testing.T, db *orgdb.DB) int {
	t.Helper()
	// No public helper; peek via CountRepoContracts for a couple of repos.
	// For the benchmark a summary sum is enough.
	total := 0
	for _, r := range realRepos {
		total += db.CountRepoContracts(r.Name)
	}
	// CountRepoContracts double-counts provider+consumer rows when the
	// same repo is on both sides, but across distinct repos that rarely
	// happens — the approximation is fine for a before/after delta.
	return total
}

// sanity build guard — keep unused-import quiet when the file is compiled
// without the realdata tag.
var _ = manifest.Repo{}
