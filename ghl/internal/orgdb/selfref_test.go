package orgdb

import (
	"testing"
)

// TestCrossReferenceContracts_SkipsSelfReferences verifies that when a repo
// has both a provider contract and a consumer contract that would match by
// prefix+route, the cross-reference pass does NOT link the repo to itself.
// Previously this produced self-loop rows that polluted blast_radius and
// trace_flow output.
func TestCrossReferenceContracts_SkipsSelfReferences(t *testing.T) {
	db := openTestDB(t)
	seedRepo(t, db, "platform-backend")
	seedRepo(t, db, "other-service")

	// platform-backend publishes POST /logs/audit
	if err := db.InsertAPIContract(APIContract{
		ProviderRepo:   "platform-backend",
		Method:         "POST",
		Path:           "/logs/audit",
		ProviderSymbol: "LogsController.audit",
		Confidence:     0.2,
	}); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	// platform-backend also has an InternalRequest to LOGS_API — this is
	// the shape that used to self-match.
	if err := db.InsertAPIContract(APIContract{
		ConsumerRepo:   "platform-backend",
		Method:         "POST",
		Path:           "/LOGS_API/audit",
		ConsumerSymbol: "SelfCaller.fetch",
		Confidence:     0.5,
	}); err != nil {
		t.Fatalf("insert self-consumer: %v", err)
	}
	// A legitimate cross-repo consumer — this MUST still be matched.
	if err := db.InsertAPIContract(APIContract{
		ConsumerRepo:   "other-service",
		Method:         "POST",
		Path:           "/LOGS_API/audit",
		ConsumerSymbol: "OtherCaller.fetch",
		Confidence:     0.5,
	}); err != nil {
		t.Fatalf("insert other-consumer: %v", err)
	}

	matched, err := db.CrossReferenceContracts()
	if err != nil {
		t.Fatalf("CrossReferenceContracts: %v", err)
	}
	// Only the other-service consumer should have been matched.
	if matched != 1 {
		t.Errorf("matched contracts: got %d, want 1 (self-ref must be skipped)", matched)
	}

	// Verify: the platform-backend consumer row still has no provider.
	rows, err := db.db.Query(`
		SELECT consumer_repo, provider_repo
		FROM api_contracts
		WHERE path = '/LOGS_API/audit'
		ORDER BY consumer_repo
	`)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var cons, prov string
		if err := rows.Scan(&cons, &prov); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[cons] = prov
	}
	if got["platform-backend"] != "" {
		t.Errorf("platform-backend self-ref was linked to provider %q (should be empty)", got["platform-backend"])
	}
	if got["other-service"] != "platform-backend" {
		t.Errorf("other-service should be linked to platform-backend, got %q", got["other-service"])
	}
}

// TestCrossReferenceEventContracts_SkipsSelfReferences verifies the same
// self-reference filter applies to event contracts (producer==consumer).
func TestCrossReferenceEventContracts_SkipsSelfReferences(t *testing.T) {
	db := openTestDB(t)
	seedRepo(t, db, "emitter-service")
	seedRepo(t, db, "worker-service")

	// emitter-service publishes "user.created"
	if err := db.InsertEventContract(EventContract{
		Topic:          "user.created",
		EventType:      "pubsub",
		ProducerRepo:   "emitter-service",
		ProducerSymbol: "Emitter.emit",
	}); err != nil {
		t.Fatalf("insert producer: %v", err)
	}
	// emitter-service also consumes "user.created" (self-subscribe)
	if err := db.InsertEventContract(EventContract{
		Topic:          "user.created",
		EventType:      "pubsub",
		ConsumerRepo:   "emitter-service",
		ConsumerSymbol: "Emitter.loop",
	}); err != nil {
		t.Fatalf("insert self-consumer: %v", err)
	}
	// worker-service also consumes "user.created" — the only legitimate match.
	if err := db.InsertEventContract(EventContract{
		Topic:          "user.created",
		EventType:      "pubsub",
		ConsumerRepo:   "worker-service",
		ConsumerSymbol: "Worker.onUserCreated",
	}); err != nil {
		t.Fatalf("insert other-consumer: %v", err)
	}

	matched, err := db.CrossReferenceEventContracts()
	if err != nil {
		t.Fatalf("CrossReferenceEventContracts: %v", err)
	}
	if matched != 1 {
		t.Errorf("matched events: got %d, want 1 (self-ref must be skipped)", matched)
	}
}

// TestQueryBlastRadius_ExcludesSelfReferences verifies that even if the DB
// contains legacy self-referenced contract rows, blast_radius never surfaces
// the repo itself as an affected consumer.
func TestQueryBlastRadius_ExcludesSelfReferences(t *testing.T) {
	db := openTestDB(t)
	seedRepo(t, db, "service-a")

	// Simulate a legacy bad row: service-a linked to itself.
	if err := db.InsertAPIContract(APIContract{
		ProviderRepo: "service-a",
		ConsumerRepo: "service-a",
		Method:       "GET",
		Path:         "/self/call",
	}); err != nil {
		t.Fatalf("insert self-ref: %v", err)
	}
	if err := db.InsertEventContract(EventContract{
		Topic:        "self.event",
		EventType:    "pubsub",
		ProducerRepo: "service-a",
		ConsumerRepo: "service-a",
	}); err != nil {
		t.Fatalf("insert event self-ref: %v", err)
	}

	result, err := db.QueryBlastRadius("service-a")
	if err != nil {
		t.Fatalf("QueryBlastRadius: %v", err)
	}
	for _, ar := range result.AffectedRepos {
		if ar.Name == "service-a" {
			t.Errorf("service-a appears in its own blast radius (reason=%s)", ar.Reason)
		}
	}
	if result.AffectedRepos == nil {
		t.Error("AffectedRepos must be empty slice not nil")
	}
}

// TestTraceFlow_ExcludesSelfLoops verifies both directions skip edges where
// FromRepo == ToRepo, even with legacy self-referenced rows.
func TestTraceFlow_ExcludesSelfLoops(t *testing.T) {
	db := openTestDB(t)
	seedRepo(t, db, "loop-service")
	seedRepo(t, db, "real-consumer")

	// Legacy self-reference
	if err := db.InsertAPIContract(APIContract{
		ProviderRepo: "loop-service",
		ConsumerRepo: "loop-service",
		Method:       "GET",
		Path:         "/self",
	}); err != nil {
		t.Fatalf("insert self: %v", err)
	}
	// Real downstream relationship
	if err := db.InsertAPIContract(APIContract{
		ProviderRepo: "loop-service",
		ConsumerRepo: "real-consumer",
		Method:       "GET",
		Path:         "/real",
	}); err != nil {
		t.Fatalf("insert real: %v", err)
	}

	for _, dir := range []string{"downstream", "upstream"} {
		steps, err := db.TraceFlow("loop-service", dir, 2)
		if err != nil {
			t.Fatalf("TraceFlow %s: %v", dir, err)
		}
		for _, s := range steps {
			if s.FromRepo == s.ToRepo {
				t.Errorf("%s: self-loop edge %s→%s leaked", dir, s.FromRepo, s.ToRepo)
			}
		}
	}
}

// TestQueryBlastRadius_PopulatesTeamViaOwnershipFallback closes the gap
// flagged during review: blast_radius now uses the same
// COALESCE(NULLIF(r.team,''), t.team) fallback as TeamTopology/SearchRepos.
func TestQueryBlastRadius_PopulatesTeamViaOwnershipFallback(t *testing.T) {
	db := openTestDB(t)
	// Consumer has no `repos.team` set, but does have a team_ownership row.
	if err := db.UpsertRepo(RepoRecord{
		Name:      "consumer-no-team",
		GitHubURL: "https://github.com/GoHighLevel/consumer-no-team.git",
		Team:      "",
		Type:      "backend",
	}); err != nil {
		t.Fatalf("upsert consumer: %v", err)
	}
	if err := db.UpsertTeamOwnership("consumer-no-team", "ownership-team", ""); err != nil {
		t.Fatalf("upsert ownership: %v", err)
	}
	seedRepo(t, db, "provider-service")

	if err := db.InsertAPIContract(APIContract{
		ProviderRepo: "provider-service",
		ConsumerRepo: "consumer-no-team",
		Method:       "GET",
		Path:         "/endpoint",
	}); err != nil {
		t.Fatalf("insert contract: %v", err)
	}

	result, err := db.QueryBlastRadius("provider-service")
	if err != nil {
		t.Fatalf("QueryBlastRadius: %v", err)
	}
	found := false
	for _, ar := range result.AffectedRepos {
		if ar.Name == "consumer-no-team" {
			if ar.Team != "ownership-team" {
				t.Errorf("team fallback failed: got %q, want %q", ar.Team, "ownership-team")
			}
			found = true
		}
	}
	if !found {
		t.Error("consumer-no-team should appear in blast radius")
	}
}
