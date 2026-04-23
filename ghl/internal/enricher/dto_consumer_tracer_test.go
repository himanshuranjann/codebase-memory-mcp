package enricher

import (
	"context"
	"testing"
)

// mockDTOSearcher satisfies DTOSearcher for tests.
type mockDTOSearcher struct {
	hits []DTOSearchHit
	err  error
}

func (m *mockDTOSearcher) SearchAll(_ context.Context, _, _ string) ([]DTOSearchHit, error) {
	return m.hits, m.err
}

func TestTraceDTOConsumers_NilSearcher_ReturnsNil(t *testing.T) {
	dtos := []DTOMetadata{{ClassName: "CommunityCheckoutDto"}}
	got, err := TraceDTOConsumers(context.Background(), dtos, nil, nil)
	if err != nil || got != nil {
		t.Errorf("nil searcher: got %v, err %v", got, err)
	}
}

func TestTraceDTOConsumers_EmptyDTOs_ReturnsNil(t *testing.T) {
	searcher := &mockDTOSearcher{hits: []DTOSearchHit{{Repo: "r", FilePath: "f.ts"}}}
	got, err := TraceDTOConsumers(context.Background(), nil, nil, searcher)
	if err != nil || got != nil {
		t.Errorf("empty dtos: got %v, err %v", got, err)
	}
}

func TestTraceDTOConsumers_FindsImporterAcrossRepos(t *testing.T) {
	searcher := &mockDTOSearcher{hits: []DTOSearchHit{
		{Repo: "ghl-revex-frontend", FilePath: "src/checkout.service.ts", Line: 5,
			Text: "import { CommunityCheckoutDto } from '../dto/community-checkout.dto'"},
	}}
	dtos := []DTOMetadata{{ClassName: "CommunityCheckoutDto", FilePath: "apps/dto/community-checkout.dto.ts"}}
	results, err := TraceDTOConsumers(context.Background(), dtos, nil, searcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].ConsumerRepo != "ghl-revex-frontend" {
		t.Errorf("ConsumerRepo = %q", results[0].ConsumerRepo)
	}
}

func TestTraceDTOConsumers_SkipsTestFiles(t *testing.T) {
	searcher := &mockDTOSearcher{hits: []DTOSearchHit{
		{Repo: "ghl-revex-frontend", FilePath: "src/checkout.spec.ts", Text: "import { CommunityCheckoutDto }"},
	}}
	dtos := []DTOMetadata{{ClassName: "CommunityCheckoutDto", FilePath: "other/file.ts"}}
	results, _ := TraceDTOConsumers(context.Background(), dtos, nil, searcher)
	if len(results) != 0 {
		t.Errorf("expected test file to be skipped, got %d results", len(results))
	}
}

func TestTraceDTOConsumers_SkipsOwnFile(t *testing.T) {
	ownFile := "apps/dto/community-checkout.dto.ts"
	searcher := &mockDTOSearcher{hits: []DTOSearchHit{
		{Repo: "ghl-revex-backend", FilePath: ownFile, Text: "class CommunityCheckoutDto"},
	}}
	dtos := []DTOMetadata{{ClassName: "CommunityCheckoutDto", FilePath: ownFile}}
	results, _ := TraceDTOConsumers(context.Background(), dtos, nil, searcher)
	if len(results) != 0 {
		t.Errorf("expected own file to be skipped, got %d results", len(results))
	}
}

func TestTraceDTOConsumers_MultipleDTOs_SearchedSeparately(t *testing.T) {
	calls := 0
	searcher := &multiCallDTOSearcher{fn: func(pattern, _ string) []DTOSearchHit {
		calls++
		if pattern == `import.*CheckoutDto` {
			return []DTOSearchHit{{Repo: "repo-a", FilePath: "a.ts"}}
		}
		return []DTOSearchHit{{Repo: "repo-b", FilePath: "b.ts"}}
	}}
	dtos := []DTOMetadata{
		{ClassName: "CheckoutDto", FilePath: "dto/checkout.dto.ts"},
		{ClassName: "OfferDto", FilePath: "dto/offer.dto.ts"},
	}
	results, _ := TraceDTOConsumers(context.Background(), dtos, nil, searcher)
	if calls < 2 {
		t.Errorf("expected at least 2 searcher calls (one per DTO), got %d", calls)
	}
	if len(results) < 2 {
		t.Errorf("expected results from both DTOs, got %d", len(results))
	}
}

func TestTraceDTOConsumers_DeduplicatesByRepoAndFile(t *testing.T) {
	searcher := &mockDTOSearcher{hits: []DTOSearchHit{
		{Repo: "ghl-revex-frontend", FilePath: "src/checkout.ts", Text: "import..."},
		{Repo: "ghl-revex-frontend", FilePath: "src/checkout.ts", Text: "import..."},
		{Repo: "ghl-revex-frontend", FilePath: "src/checkout.ts", Text: "import..."},
	}}
	dtos := []DTOMetadata{{ClassName: "CommunityCheckoutDto", FilePath: "other.ts"}}
	results, _ := TraceDTOConsumers(context.Background(), dtos, nil, searcher)
	if len(results) != 1 {
		t.Errorf("expected deduplicated to 1 result, got %d", len(results))
	}
}

func TestClassifyDTOSeverity(t *testing.T) {
	tests := []struct {
		className string
		want      string
	}{
		{"CommunityCheckoutDto", "BREAKING"},
		{"CheckoutRequest", "BREAKING"},
		{"CheckoutResponse", "BREAKING"},
		{"CheckoutPayload", "BREAKING"},
		{"CheckoutOptions", "ADDITIVE"},
		{"SomeConfig", "ADDITIVE"},
	}
	for _, tt := range tests {
		got := classifyDTOSeverity(tt.className)
		if got != tt.want {
			t.Errorf("classifyDTOSeverity(%q) = %q, want %q", tt.className, got, tt.want)
		}
	}
}

func TestTraceDTOConsumers_PR10133_CommunityCheckoutDto(t *testing.T) {
	searcher := &mockDTOSearcher{hits: []DTOSearchHit{
		{Repo: "ghl-revex-frontend", FilePath: "src/community-checkout/checkout.service.ts",
			Text: "import { CommunityCheckoutDto } from '@api/dto/community-checkout.dto'"},
	}}
	dtos := []DTOMetadata{{
		ClassName: "CommunityCheckoutDto",
		FilePath:  "apps/courses/src/community-checkout/dto/community-checkout.dto.ts",
	}}
	results, err := TraceDTOConsumers(context.Background(), dtos, nil, searcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("PR #10133: expected DTO consumer results, got none")
	}
	if results[0].Severity != "BREAKING" {
		t.Errorf("Severity = %q, want BREAKING", results[0].Severity)
	}
	if results[0].ConsumerRepo != "ghl-revex-frontend" {
		t.Errorf("ConsumerRepo = %q, want ghl-revex-frontend", results[0].ConsumerRepo)
	}
}

// multiCallDTOSearcher lets tests inspect per-call patterns.
type multiCallDTOSearcher struct {
	fn func(pattern, fileGlob string) []DTOSearchHit
}

func (m *multiCallDTOSearcher) SearchAll(_ context.Context, pattern, fileGlob string) ([]DTOSearchHit, error) {
	return m.fn(pattern, fileGlob), nil
}
