package enricher

import (
	"context"
	"testing"
)

type mockCallSearcher struct {
	hits []CallSearchHit
}

func (m *mockCallSearcher) SearchAll(_ context.Context, _, _ string) ([]CallSearchHit, error) {
	return m.hits, nil
}

func TestTraceInternalCallImpact_NilSearcher_ReturnsNil(t *testing.T) {
	calls := []InternalRequestCall{{ServiceName: "OFFERS_SERVICE", Route: "checkout"}}
	got, err := TraceInternalCallImpact(context.Background(), calls, nil, nil)
	if err != nil || got != nil {
		t.Errorf("nil searcher: got %v, err %v", got, err)
	}
}

func TestTraceInternalCallImpact_EmptyCalls_ReturnsNil(t *testing.T) {
	searcher := &mockCallSearcher{hits: []CallSearchHit{{Repo: "r", FilePath: "f.ts"}}}
	got, err := TraceInternalCallImpact(context.Background(), nil, nil, searcher)
	if err != nil || got != nil {
		t.Errorf("empty calls: got %v, err %v", got, err)
	}
}

func TestTraceInternalCallImpact_FindsControllerInTargetService(t *testing.T) {
	searcher := &mockCallSearcher{hits: []CallSearchHit{
		{Repo: "ghl-offers-backend", FilePath: "src/offers/offers.controller.ts",
			Text: "@Controller('offers')"},
	}}
	calls := []InternalRequestCall{{ServiceName: "OFFERS_SERVICE", Route: "checkout"}}
	results, err := TraceInternalCallImpact(context.Background(), calls, nil, searcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected 1 InternalCallImpact, got none")
	}
	if results[0].CalledService != "OFFERS_SERVICE" {
		t.Errorf("CalledService = %q", results[0].CalledService)
	}
}

func TestTraceInternalCallImpact_NoHits_ReturnsNil(t *testing.T) {
	searcher := &mockCallSearcher{hits: nil}
	calls := []InternalRequestCall{{ServiceName: "UNKNOWN_SERVICE", Route: "foo"}}
	results, _ := TraceInternalCallImpact(context.Background(), calls, nil, searcher)
	if results != nil {
		t.Errorf("expected nil for no hits, got %v", results)
	}
}

func TestTraceInternalCallImpact_MultipleServices_SearchedSeparately(t *testing.T) {
	searcher := &mockCallSearcher{hits: []CallSearchHit{
		{Repo: "ghl-offers-backend", FilePath: "offers.controller.ts"},
	}}
	calls := []InternalRequestCall{
		{ServiceName: "OFFERS_SERVICE", Route: "checkout"},
		{ServiceName: "CONTACTS_API", Route: "upsert"},
	}
	results, _ := TraceInternalCallImpact(context.Background(), calls, nil, searcher)
	// Both services should produce results (mock returns same hit for both)
	if len(results) < 1 {
		t.Errorf("expected results for multiple services, got %d", len(results))
	}
}

func TestTraceInternalCallImpact_DeduplicatesByServiceAndRoute(t *testing.T) {
	searcher := &mockCallSearcher{hits: []CallSearchHit{
		{Repo: "ghl-offers-backend", FilePath: "offers.controller.ts"},
	}}
	calls := []InternalRequestCall{
		{ServiceName: "OFFERS_SERVICE", Route: "checkout"},
		{ServiceName: "OFFERS_SERVICE", Route: "checkout"}, // duplicate
	}
	results, _ := TraceInternalCallImpact(context.Background(), calls, nil, searcher)
	if len(results) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(results))
	}
}

func TestServiceNameToRepoHint(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"OFFERS_SERVICE", "offers"},
		{"CONTACTS_API", "contacts"},
		{"REVEX_MEMBERSHIP", "revex-membership"},
		{"COMMUNITY_CHECKOUT_ORCHESTRATOR", "community-checkout"},
		{"CHECKOUT_WORKER", "checkout"},
	}
	for _, tt := range tests {
		got := serviceNameToRepoHint(tt.input)
		if got != tt.want {
			t.Errorf("serviceNameToRepoHint(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTraceInternalCallImpact_PR10133_CommunityCheckoutCallsOffers(t *testing.T) {
	searcher := &mockCallSearcher{hits: []CallSearchHit{
		{Repo: "ghl-offers-backend", FilePath: "src/offers/offers.controller.ts",
			Text: "@Controller('offers')"},
	}}
	calls := []InternalRequestCall{{ServiceName: "OFFERS_SERVICE", Route: "checkout"}}
	results, err := TraceInternalCallImpact(context.Background(), calls, nil, searcher)
	if err != nil {
		t.Fatalf("PR #10133: unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("PR #10133: expected InternalCallImpact, got none")
	}
	if results[0].CalledService != "OFFERS_SERVICE" {
		t.Errorf("PR #10133: CalledService = %q", results[0].CalledService)
	}
	found := false
	for _, f := range results[0].ControllerFiles {
		if f == "src/offers/offers.controller.ts" {
			found = true
		}
	}
	if !found {
		t.Errorf("PR #10133: expected controller file in ControllerFiles, got %v", results[0].ControllerFiles)
	}
}
