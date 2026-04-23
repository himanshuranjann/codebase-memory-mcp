package enricher

import (
	"context"
	"testing"
)

type mfaDiscoveryMockSearcher struct {
	hitsByPattern map[string][]OrgSearchHit
}

func (m *mfaDiscoveryMockSearcher) SearchAll(_ context.Context, pattern, _ string) ([]OrgSearchHit, error) {
	for p, hits := range m.hitsByPattern {
		if p == pattern {
			return hits, nil
		}
	}
	// fallback: return everything when no exact match (simpler tests).
	var all []OrgSearchHit
	for _, hits := range m.hitsByPattern {
		all = append(all, hits...)
	}
	return all, nil
}

func (m *mfaDiscoveryMockSearcher) ListProjects(_ context.Context) ([]string, error) {
	return nil, nil
}

func TestDiscoverMFAApps_NilSearcher_ReturnsNil(t *testing.T) {
	got, _ := DiscoverMFAApps(context.Background(), nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestDiscoverMFAApps_DetectsModuleFederationConfig(t *testing.T) {
	searcher := &mfaDiscoveryMockSearcher{
		hitsByPattern: map[string][]OrgSearchHit{
			`name\s*:\s*['"]`: {
				{Repo: "ghl-revex-frontend", FilePath: "apps/communities/module-federation.config.ts",
					Text: `  name: "communitiesApp",`},
			},
		},
	}
	got, _ := DiscoverMFAApps(context.Background(), searcher)
	if len(got) != 1 {
		t.Fatalf("expected 1 discovery, got %d", len(got))
	}
	if got[0].Kind != MFAKindSPMT {
		t.Errorf("Kind = %q, want spmt", got[0].Kind)
	}
	if got[0].FederationKey != "communitiesApp" {
		t.Errorf("FederationKey = %q, want communitiesApp", got[0].FederationKey)
	}
}

func TestDiscoverMFAApps_DetectsNuxtConfig(t *testing.T) {
	searcher := &mfaDiscoveryMockSearcher{
		hitsByPattern: map[string][]OrgSearchHit{
			`defineNuxtConfig|export\s+default\s+defineNuxtConfig`: {
				{Repo: "ghl-membership-frontend", FilePath: "nuxt.config.ts",
					Text: `export default defineNuxtConfig({`},
			},
		},
	}
	got, _ := DiscoverMFAApps(context.Background(), searcher)
	if len(got) == 0 {
		t.Fatal("expected nuxt config to be detected")
	}
	found := false
	for _, d := range got {
		if d.Kind == MFAKindSSR && d.Repo == "ghl-membership-frontend" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SSR result for ghl-membership-frontend, got %+v", got)
	}
}

func TestDiscoverMFAApps_DeduplicatesByRepoAndKey(t *testing.T) {
	searcher := &mfaDiscoveryMockSearcher{
		hitsByPattern: map[string][]OrgSearchHit{
			`name\s*:\s*['"]`: {
				{Repo: "r1", FilePath: "apps/a/module-federation.config.ts", Text: `name: "appA"`},
				{Repo: "r1", FilePath: "apps/a/module-federation.config.ts", Text: `name: "appA"`},
			},
		},
	}
	got, _ := DiscoverMFAApps(context.Background(), searcher)
	if len(got) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(got))
	}
}

func TestMergeDiscoveredIntoRegistry_StaticWinsOnKeyCollision(t *testing.T) {
	static := &MFARegistry{
		apps: []MFAAppEntry{
			{Kind: MFAKindSPMT, Key: "communitiesApp", GithubRepo: "ghl-revex-frontend",
				Owner: "@revex-communities", DisplayName: "Communities App"},
		},
	}
	static.buildIndices()
	discovered := []MFADiscoveryResult{
		{Repo: "ghl-revex-frontend", Kind: MFAKindSPMT, AppKey: "communitiesApp",
			FederationKey: "communitiesApp"},
	}
	merged := MergeDiscoveredIntoRegistry(static, discovered)
	apps := merged.LookupByRepo("ghl-revex-frontend")
	if len(apps) != 1 {
		t.Fatalf("expected 1 app for ghl-revex-frontend, got %d", len(apps))
	}
	if apps[0].Owner != "@revex-communities" {
		t.Errorf("static Owner should be preserved, got %q", apps[0].Owner)
	}
	if apps[0].DisplayName != "Communities App" {
		t.Errorf("static DisplayName should be preserved, got %q", apps[0].DisplayName)
	}
}

func TestMergeDiscoveredIntoRegistry_AddsNewApps(t *testing.T) {
	static := &MFARegistry{}
	static.buildIndices()
	discovered := []MFADiscoveryResult{
		{Repo: "ghl-new-frontend", Kind: MFAKindSPMT, AppKey: "newApp",
			FederationKey: "newApp"},
	}
	merged := MergeDiscoveredIntoRegistry(static, discovered)
	apps := merged.LookupByRepo("ghl-new-frontend")
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Key != "newApp" {
		t.Errorf("Key = %q, want newApp", apps[0].Key)
	}
}

func TestMergeDiscoveredIntoRegistry_NilStatic(t *testing.T) {
	discovered := []MFADiscoveryResult{
		{Repo: "r", Kind: MFAKindSPMT, AppKey: "k", FederationKey: "k"},
	}
	merged := MergeDiscoveredIntoRegistry(nil, discovered)
	if merged == nil {
		t.Fatal("expected merged registry, got nil")
	}
	if len(merged.LookupByRepo("r")) != 1 {
		t.Errorf("expected 1 app, got %d", len(merged.LookupByRepo("r")))
	}
}
