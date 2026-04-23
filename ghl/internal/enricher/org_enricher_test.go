package enricher

import (
	"context"
	"testing"
)

type mockOrgSearcher struct {
	searchAllFunc   func(context.Context, string, string) ([]OrgSearchHit, error)
	listProjectsFunc func(context.Context) ([]string, error)
}

func (m *mockOrgSearcher) SearchAll(ctx context.Context, pattern, fileGlob string) ([]OrgSearchHit, error) {
	if m == nil || m.searchAllFunc == nil {
		return nil, nil
	}
	return m.searchAllFunc(ctx, pattern, fileGlob)
}

func (m *mockOrgSearcher) ListProjects(ctx context.Context) ([]string, error) {
	if m == nil || m.listProjectsFunc == nil {
		return nil, nil
	}
	return m.listProjectsFunc(ctx)
}

func newTestOrgMFARegistry() *MFARegistry {
	reg := &MFARegistry{
		apps: []MFAAppEntry{
			{Kind: MFAKindSPMT, Key: "communitiesApp", GithubRepo: "ghl-revex-frontend"},
			{Kind: MFAKindSPMT, Key: "communities-member-portal", GithubRepo: "ghl-revex-frontend"},
			{Kind: MFAKindSSR, Key: "membership-courses-portal", GithubRepo: "ghl-membership-frontend"},
		},
	}
	reg.buildIndices()
	return reg
}

func TestOrgEnricher_DiscoverRouteCallers_FindsFrontendRepo(t *testing.T) {
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, pattern, fileGlob string) ([]OrgSearchHit, error) {
			if pattern != `\/community-checkout` {
				t.Fatalf("pattern = %q, want %q", pattern, `\/community-checkout`)
			}
			if fileGlob != "*.{ts,vue,tsx,js,jsx}" {
				t.Fatalf("fileGlob = %q, want %q", fileGlob, "*.{ts,vue,tsx,js,jsx}")
			}
			return []OrgSearchHit{
				{
					Project:  "data-fleet-cache-repos-ghl-revex-frontend",
					Repo:     "ghl-revex-frontend",
					FilePath: "apps/communities/src/checkout.tsx",
					Line:     42,
					Text:     `await axios.post("/community-checkout/checkout", payload)`,
				},
			}, nil
		},
	}

	results, err := NewOrgEnricher(searcher, newTestOrgMFARegistry()).DiscoverRouteCallers(context.Background(), "/community-checkout/")
	if err != nil {
		t.Fatalf("DiscoverRouteCallers returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].PathPrefix != "/community-checkout/" {
		t.Fatalf("PathPrefix = %q, want %q", results[0].PathPrefix, "/community-checkout/")
	}
	if len(results[0].Callers) != 1 {
		t.Fatalf("len(results[0].Callers) = %d, want 1", len(results[0].Callers))
	}
	caller := results[0].Callers[0]
	if caller.Repo != "ghl-revex-frontend" {
		t.Fatalf("caller.Repo = %q, want ghl-revex-frontend", caller.Repo)
	}
	if len(caller.MFAAppKeys) != 2 {
		t.Fatalf("len(caller.MFAAppKeys) = %d, want 2", len(caller.MFAAppKeys))
	}
	if caller.MFAAppKeys[0] != "communities-member-portal" && caller.MFAAppKeys[1] != "communities-member-portal" {
		t.Fatalf("expected communities-member-portal in %v", caller.MFAAppKeys)
	}
	if caller.MFAAppKeys[0] != "communitiesApp" && caller.MFAAppKeys[1] != "communitiesApp" {
		t.Fatalf("expected communitiesApp in %v", caller.MFAAppKeys)
	}
}

func TestOrgEnricher_DiscoverRouteCallers_DeduplicatesRepos(t *testing.T) {
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, _, _ string) ([]OrgSearchHit, error) {
			hits := make([]OrgSearchHit, 0, 5)
			for i := 0; i < 5; i++ {
				hits = append(hits, OrgSearchHit{
					Project:  "data-fleet-cache-repos-ghl-revex-frontend",
					Repo:     "ghl-revex-frontend",
					FilePath: "apps/communities/src/checkout.tsx",
					Line:     i + 1,
					Text:     `fetch("/community-checkout/checkout")`,
				})
			}
			return hits, nil
		},
	}

	results, err := NewOrgEnricher(searcher, newTestOrgMFARegistry()).DiscoverRouteCallers(context.Background(), "/community-checkout/")
	if err != nil {
		t.Fatalf("DiscoverRouteCallers returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if len(results[0].Callers) != 1 {
		t.Fatalf("len(results[0].Callers) = %d, want 1", len(results[0].Callers))
	}
}

func TestOrgEnricher_DiscoverRouteCallers_SkipsBackendRepos(t *testing.T) {
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, _, _ string) ([]OrgSearchHit, error) {
			return []OrgSearchHit{
				{
					Project:  "data-fleet-cache-repos-ghl-revex-backend",
					Repo:     "ghl-revex-backend",
					FilePath: "apps/courses/src/community-checkout/community-checkout.controller.ts",
					Line:     10,
					Text:     `return "/community-checkout/checkout"`,
				},
			}, nil
		},
	}

	results, err := NewOrgEnricher(searcher, newTestOrgMFARegistry()).DiscoverRouteCallers(context.Background(), "/community-checkout/")
	if err != nil {
		t.Fatalf("DiscoverRouteCallers returned error: %v", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want nil", results)
	}
}

func TestOrgEnricher_DiscoverRouteCallers_NilSearcher_ReturnsNil(t *testing.T) {
	results, err := NewOrgEnricher(nil, newTestOrgMFARegistry()).DiscoverRouteCallers(context.Background(), "/community-checkout/")
	if err != nil {
		t.Fatalf("DiscoverRouteCallers returned error: %v", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want nil", results)
	}
}

func TestOrgEnricher_DiscoverTopicImpact_FindsSubscriber(t *testing.T) {
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, pattern, fileGlob string) ([]OrgSearchHit, error) {
			if pattern != "CHECKOUT_INTEGRATIONS" {
				t.Fatalf("pattern = %q, want CHECKOUT_INTEGRATIONS", pattern)
			}
			if fileGlob != "*.{ts,go,js}" {
				t.Fatalf("fileGlob = %q, want %q", fileGlob, "*.{ts,go,js}")
			}
			return []OrgSearchHit{
				{
					Project:  "data-fleet-cache-repos-ghl-revex-backend",
					Repo:     "ghl-revex-backend",
					FilePath: "apps/courses/src/workers/checkout-integrations.worker.ts",
					Line:     18,
					Text:     `@EventPattern(CHECKOUT_INTEGRATIONS)`,
				},
			}, nil
		},
	}

	impacts, err := NewOrgEnricher(searcher, newTestOrgMFARegistry()).DiscoverTopicImpact(context.Background(), []string{"CHECKOUT_INTEGRATIONS"})
	if err != nil {
		t.Fatalf("DiscoverTopicImpact returned error: %v", err)
	}
	if len(impacts) != 1 {
		t.Fatalf("len(impacts) = %d, want 1", len(impacts))
	}
	if impacts[0].TopicID != "CHECKOUT_INTEGRATIONS" {
		t.Fatalf("TopicID = %q, want CHECKOUT_INTEGRATIONS", impacts[0].TopicID)
	}
	if impacts[0].SubscriberRepo != "ghl-revex-backend" {
		t.Fatalf("SubscriberRepo = %q, want ghl-revex-backend", impacts[0].SubscriberRepo)
	}
}

func TestOrgEnricher_DiscoverTopicImpact_SkipsProducers(t *testing.T) {
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, _, _ string) ([]OrgSearchHit, error) {
			return []OrgSearchHit{
				{
					Project:  "data-fleet-cache-repos-ghl-revex-backend",
					Repo:     "ghl-revex-backend",
					FilePath: "apps/courses/src/checkout-process/config/community-checkout-orchestrator.config.ts",
					Line:     12,
					Text:     `new PublisherStep(stepName, CHECKOUT_INTEGRATIONS, eventName)`,
				},
			}, nil
		},
	}

	impacts, err := NewOrgEnricher(searcher, newTestOrgMFARegistry()).DiscoverTopicImpact(context.Background(), []string{"CHECKOUT_INTEGRATIONS"})
	if err != nil {
		t.Fatalf("DiscoverTopicImpact returned error: %v", err)
	}
	if impacts != nil {
		t.Fatalf("impacts = %+v, want nil", impacts)
	}
}

func TestOrgEnricher_DiscoverTopicImpact_MultipleTopics(t *testing.T) {
	var searched []string
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, pattern, _ string) ([]OrgSearchHit, error) {
			searched = append(searched, pattern)
			switch pattern {
			case "CHECKOUT_INTEGRATIONS":
				return []OrgSearchHit{
					{
						Project:  "data-fleet-cache-repos-ghl-revex-backend",
						Repo:     "ghl-revex-backend",
						FilePath: "apps/courses/src/workers/checkout-integrations.worker.ts",
						Line:     10,
						Text:     `@EventPattern(CHECKOUT_INTEGRATIONS)`,
					},
					{
						Project:  "data-fleet-cache-repos-ghl-revex-backend",
						Repo:     "ghl-revex-backend",
						FilePath: "apps/courses/src/workers/checkout-integrations.worker.ts",
						Line:     11,
						Text:     `handler(CHECKOUT_INTEGRATIONS)`,
					},
				}, nil
			case "COMMUNITY_MEMBERSHIP_SYNC":
				return []OrgSearchHit{
					{
						Project:  "data-fleet-cache-repos-ghl-membership-frontend",
						Repo:     "ghl-membership-frontend",
						FilePath: "apps/portal/src/subscribers/community-sync.ts",
						Line:     7,
						Text:     `listener(COMMUNITY_MEMBERSHIP_SYNC)`,
					},
				}, nil
			default:
				return nil, nil
			}
		},
	}

	impacts, err := NewOrgEnricher(searcher, newTestOrgMFARegistry()).DiscoverTopicImpact(context.Background(), []string{
		"CHECKOUT_INTEGRATIONS",
		"COMMUNITY_MEMBERSHIP_SYNC",
		"CHECKOUT_INTEGRATIONS",
	})
	if err != nil {
		t.Fatalf("DiscoverTopicImpact returned error: %v", err)
	}
	if len(searched) != 2 {
		t.Fatalf("len(searched) = %d, want 2", len(searched))
	}
	if len(impacts) != 2 {
		t.Fatalf("len(impacts) = %d, want 2", len(impacts))
	}
}

func TestOrgEnricher_EmptyHits_ReturnsNil(t *testing.T) {
	searcher := &mockOrgSearcher{
		searchAllFunc: func(_ context.Context, _, _ string) ([]OrgSearchHit, error) {
			return nil, nil
		},
	}
	org := NewOrgEnricher(searcher, newTestOrgMFARegistry())

	callers, err := org.DiscoverRouteCallers(context.Background(), "/community-checkout/")
	if err != nil {
		t.Fatalf("DiscoverRouteCallers returned error: %v", err)
	}
	if callers != nil {
		t.Fatalf("callers = %+v, want nil", callers)
	}

	impacts, err := org.DiscoverTopicImpact(context.Background(), []string{"CHECKOUT_INTEGRATIONS"})
	if err != nil {
		t.Fatalf("DiscoverTopicImpact returned error: %v", err)
	}
	if impacts != nil {
		t.Fatalf("impacts = %+v, want nil", impacts)
	}
}
