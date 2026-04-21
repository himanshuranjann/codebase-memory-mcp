package orgtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSearch_TeamOnlyWorks verifies the removal of the "query is required"
// gate: a caller who passes only `team` should get repos on that team.
func TestSearch_TeamOnlyWorks(t *testing.T) {
	svc, db := newService(t)
	seedRepo(t, db, "ai-backend", "ai", "backend")
	seedRepo(t, db, "platform-backend", "platform", "backend")

	result, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{
		"query": "",
		"team":  "ai",
	})
	if err != nil {
		t.Fatalf("CallTool team-only: %v", err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "ai-backend") {
		t.Errorf("team-only search missed ai-backend; JSON=%s", string(raw))
	}
	if strings.Contains(string(raw), "platform-backend") {
		t.Errorf("team-only search leaked platform-backend; JSON=%s", string(raw))
	}
}

// TestSearch_NeitherQueryNorTeamErrors verifies we still reject a call with
// nothing to filter on — a full unconstrained dump is not useful and would
// be slow on a 480-repo org.db.
func TestSearch_NeitherQueryNorTeamErrors(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.CallTool(context.Background(), "org_search", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when neither query nor team provided")
	}
	if !strings.Contains(err.Error(), "query") || !strings.Contains(err.Error(), "team") {
		t.Errorf("error should mention both query and team; got: %v", err)
	}
}

// TestTraceFlow_RejectsInvalidDirection verifies the handler short-circuits
// on bad inputs instead of silently returning downstream results.
func TestTraceFlow_RejectsInvalidDirection(t *testing.T) {
	svc, db := newService(t)
	seedRepo(t, db, "trigger-repo", "team", "backend")

	_, err := svc.CallTool(context.Background(), "org_trace_flow", map[string]interface{}{
		"trigger":   "trigger-repo",
		"direction": "sideways",
		"max_hops":  float64(2),
	})
	if err == nil {
		t.Fatal("expected error for invalid direction")
	}
	if !strings.Contains(err.Error(), "direction") {
		t.Errorf("error should mention direction; got: %v", err)
	}
}

// TestTraceFlow_AcceptsValidDirections verifies the allow-list still lets
// empty/upstream/downstream through.
func TestTraceFlow_AcceptsValidDirections(t *testing.T) {
	svc, db := newService(t)
	seedRepo(t, db, "trigger-repo", "team", "backend")

	for _, dir := range []string{"", "upstream", "downstream"} {
		_, err := svc.CallTool(context.Background(), "org_trace_flow", map[string]interface{}{
			"trigger":   "trigger-repo",
			"direction": dir,
			"max_hops":  float64(1),
		})
		if err != nil {
			t.Errorf("direction=%q should be accepted; got err=%v", dir, err)
		}
	}
}
