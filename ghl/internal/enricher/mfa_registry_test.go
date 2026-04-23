package enricher

import (
	"testing"
)

// minimalRegistryYAML is a self-contained registry used across tests.
// It covers all three kinds so every lookup path is exercised.
const minimalRegistryYAML = `
apps:
  - kind: spmt
    key: conversationsApp
    display_name: Conversations
    github_repo: ghl-crm-frontend
    app_dir: apps/conversations
    federation_key: conversationsApp
    cdn_slug: crm/conversations-components
    cdn_url_prod: https://appcdn.leadconnectorhq.com/crm/conversations-components/remoteEntry.js
    route_prefixes:
      - /v2/location/:locationId/conversations
    level: location
    product_area: "CRM — Conversations"
    owner: "@crm-conversations"
    user_type: agency-admin

  - kind: spmt
    key: contactsApp
    display_name: Contacts
    github_repo: ghl-crm-frontend
    app_dir: apps/contacts
    federation_key: contactsApp
    cdn_slug: crm/contacts
    cdn_url_prod: https://appcdn.leadconnectorhq.com/crm/contacts/remoteEntry.js
    route_prefixes:
      - /v2/location/:locationId/contacts
    level: location
    product_area: "CRM — Contacts"
    owner: "@crm-contacts"
    user_type: agency-admin

  - kind: standalone
    key: funnel-website-renderer
    display_name: Funnel & Website Renderer
    github_repo: ghl-funnel-website
    deploy_target: cloud-run
    url_pattern: "{customDomain} | *.myfunnels.com"
    backend_api_prefixes:
      - /funnels/
      - /websites/
    product_area: "Funnels & Websites — Public Renderer"
    owner: "@leadgen-funnels"
    user_type: prospect

  - kind: ssr
    key: membership-courses-portal
    display_name: Membership & Courses Portal
    github_repo: ghl-membership-frontend
    deploy_target: cloud-run-per-instance
    url_pattern: "{courseName}.{agencyDomain}.com"
    backend_api_prefixes:
      - /membership/
      - /courses/
    product_area: "Memberships & Courses — Student Portal"
    owner: "@revex-membership"
    user_type: student
`

func loadTestRegistry(t *testing.T) *MFARegistry {
	t.Helper()
	reg, err := parseMFARegistry([]byte(minimalRegistryYAML))
	if err != nil {
		t.Fatalf("parseMFARegistry: %v", err)
	}
	return reg
}

func TestMFARegistry_ParsesAllKinds(t *testing.T) {
	reg := loadTestRegistry(t)
	apps := reg.AllApps()
	if len(apps) != 4 {
		t.Fatalf("AllApps() len = %d, want 4", len(apps))
	}
}

func TestMFARegistry_LookupByRepo_SPMT(t *testing.T) {
	reg := loadTestRegistry(t)
	apps := reg.LookupByRepo("ghl-crm-frontend")
	if len(apps) != 2 {
		t.Fatalf("LookupByRepo(ghl-crm-frontend) = %d, want 2", len(apps))
	}
	for _, a := range apps {
		if a.Kind != MFAKindSPMT {
			t.Errorf("app %q kind = %q, want spmt", a.Key, a.Kind)
		}
	}
}

func TestMFARegistry_LookupByRepo_EmptyForUnknown(t *testing.T) {
	reg := loadTestRegistry(t)
	apps := reg.LookupByRepo("no-such-repo")
	if len(apps) != 0 {
		t.Errorf("LookupByRepo(no-such-repo) = %d, want 0", len(apps))
	}
}

func TestMFARegistry_LookupByFederationKey_Found(t *testing.T) {
	reg := loadTestRegistry(t)
	app, ok := reg.LookupByFederationKey("conversationsApp")
	if !ok {
		t.Fatal("LookupByFederationKey(conversationsApp) not found")
	}
	if app.CDNURLProd != "https://appcdn.leadconnectorhq.com/crm/conversations-components/remoteEntry.js" {
		t.Errorf("CDNURLProd = %q", app.CDNURLProd)
	}
	if app.Level != "location" {
		t.Errorf("Level = %q, want location", app.Level)
	}
}

func TestMFARegistry_LookupByFederationKey_NotFound(t *testing.T) {
	reg := loadTestRegistry(t)
	_, ok := reg.LookupByFederationKey("nonExistentApp")
	if ok {
		t.Error("expected not found for nonExistentApp")
	}
}

func TestMFARegistry_LookupByAPIPrefix_Standalone(t *testing.T) {
	reg := loadTestRegistry(t)
	apps := reg.LookupByAPIPrefix("/funnels/pages/42")
	if len(apps) != 1 {
		t.Fatalf("LookupByAPIPrefix(/funnels/pages/42) = %d, want 1", len(apps))
	}
	if apps[0].Key != "funnel-website-renderer" {
		t.Errorf("key = %q, want funnel-website-renderer", apps[0].Key)
	}
}

func TestMFARegistry_LookupByAPIPrefix_SSR(t *testing.T) {
	reg := loadTestRegistry(t)
	apps := reg.LookupByAPIPrefix("/membership/offers")
	if len(apps) != 1 {
		t.Fatalf("LookupByAPIPrefix(/membership/offers) = %d, want 1", len(apps))
	}
	if apps[0].Kind != MFAKindSSR {
		t.Errorf("kind = %q, want ssr", apps[0].Kind)
	}
}

func TestMFARegistry_LookupByAPIPrefix_NoMatch(t *testing.T) {
	reg := loadTestRegistry(t)
	apps := reg.LookupByAPIPrefix("/contacts/v2/list")
	// /contacts/ is NOT in backend_api_prefixes for any standalone/SSR entry.
	if len(apps) != 0 {
		t.Errorf("LookupByAPIPrefix(/contacts/v2/list) = %d, want 0", len(apps))
	}
}

func TestMFARegistry_LookupByAPIPrefix_IgnoresSPMT(t *testing.T) {
	reg := loadTestRegistry(t)
	// Even though SPMT apps exist, they should never appear in prefix lookups.
	apps := reg.LookupByAPIPrefix("/v2/location/123/conversations")
	if len(apps) != 0 {
		t.Errorf("expected no SPMT apps in prefix lookup, got %d", len(apps))
	}
}

func TestMFARegistry_ToRef_PopulatesFields(t *testing.T) {
	reg := loadTestRegistry(t)
	app, ok := reg.LookupByFederationKey("contactsApp")
	if !ok {
		t.Fatal("contactsApp not found")
	}
	ref := app.ToRef("repo:ghl-crm-frontend")
	if ref.Kind != MFAKindSPMT {
		t.Errorf("Kind = %q", ref.Kind)
	}
	if ref.MatchReason != "repo:ghl-crm-frontend" {
		t.Errorf("MatchReason = %q", ref.MatchReason)
	}
	if ref.CDNURLProd == "" {
		t.Error("CDNURLProd should be non-empty for SPMT app")
	}
}

func TestMFARegistry_NilRegistryLookups(t *testing.T) {
	var reg *MFARegistry
	if apps := reg.LookupByRepo("x"); len(apps) != 0 {
		t.Errorf("nil registry LookupByRepo should return empty")
	}
	if _, ok := reg.LookupByFederationKey("x"); ok {
		t.Error("nil registry LookupByFederationKey should return not found")
	}
	if apps := reg.LookupByAPIPrefix("/x"); apps != nil {
		t.Errorf("nil registry LookupByAPIPrefix should return nil")
	}
}

func TestMFARegistry_DefaultEmbedLoads(t *testing.T) {
	// Verifies the embedded mfa_registry.yaml is valid YAML and parses
	// without error. The registry should have a substantial number of apps.
	reg, err := LoadDefaultMFARegistry()
	if err != nil {
		t.Fatalf("LoadDefaultMFARegistry: %v", err)
	}
	apps := reg.AllApps()
	// We have 94 SPMT + 12 standalone + 5 SSR = 111 minimum.
	if len(apps) < 50 {
		t.Errorf("expected ≥50 apps in embedded registry, got %d", len(apps))
	}

	// Spot-check a known SPMT app.
	conv, ok := reg.LookupByFederationKey("conversationsApp")
	if !ok {
		t.Error("conversationsApp missing from embedded registry")
	} else if conv.Kind != MFAKindSPMT {
		t.Errorf("conversationsApp kind = %q, want spmt", conv.Kind)
	}

	// Spot-check a known standalone app.
	funnelApps := reg.LookupByAPIPrefix("/funnels/")
	if len(funnelApps) == 0 {
		t.Error("no standalone app matched /funnels/ in embedded registry")
	}
}
