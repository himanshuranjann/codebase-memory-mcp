package enricher

import (
	"testing"
)

const minimalRouteCallersYAML = `
callers:
  - path_prefix: "/community-checkout/"
    description: "Community checkout orchestration API"
    callers:
      - repo: "ghl-revex-frontend"
        mfa_app_keys: ["communitiesApp"]
        call_patterns: ["POST /community-checkout/checkout"]
        notes: "Communities frontend triggers checkout for community offers"
      - repo: "ghl-membership-frontend"
        mfa_app_keys: ["membership-courses-portal"]
        call_patterns: ["POST /community-checkout/checkout"]
        notes: "Membership portal handles community offer checkout flows"

  - path_prefix: "/communities/"
    description: "Communities group and member APIs"
    callers:
      - repo: "ghl-revex-frontend"
        mfa_app_keys: ["communitiesApp", "communities-member-portal"]
        call_patterns: ["GET /communities/groups/:groupId"]
        notes: "Communities admin and member portal"

  - path_prefix: "/membership/"
    description: "Membership management APIs"
    callers:
      - repo: "ghl-membership-frontend"
        mfa_app_keys: ["membershipApp", "membership-courses-portal"]
        call_patterns: ["GET /membership/offers"]
        notes: "Primary membership SPA"
`

func loadTestRouteCallersRegistry(t *testing.T) *RouteCallersRegistry {
	t.Helper()
	reg, err := parseRouteCallersRegistry([]byte(minimalRouteCallersYAML))
	if err != nil {
		t.Fatalf("parseRouteCallersRegistry: %v", err)
	}
	return reg
}

func TestRouteCallersRegistry_LookupByRoute_ExactMatch(t *testing.T) {
	reg := loadTestRouteCallersRegistry(t)
	result := reg.LookupByRoute("/community-checkout/checkout")
	if result == nil {
		t.Fatal("expected match for /community-checkout/checkout, got nil")
	}
	if len(result.Callers) != 2 {
		t.Errorf("Callers len = %d, want 2", len(result.Callers))
	}
	// Should include both frontend repos
	repos := make(map[string]bool)
	for _, c := range result.Callers {
		repos[c.Repo] = true
	}
	if !repos["ghl-revex-frontend"] {
		t.Error("expected ghl-revex-frontend in callers")
	}
	if !repos["ghl-membership-frontend"] {
		t.Error("expected ghl-membership-frontend in callers")
	}
}

func TestRouteCallersRegistry_LookupByRoute_LongestPrefixWins(t *testing.T) {
	yaml := `
callers:
  - path_prefix: "/community/"
    description: "Short prefix"
    callers:
      - repo: "short-match"
        mfa_app_keys: []
        call_patterns: []
        notes: ""
  - path_prefix: "/community-checkout/"
    description: "Longer prefix"
    callers:
      - repo: "long-match"
        mfa_app_keys: []
        call_patterns: []
        notes: ""
`
	reg, err := parseRouteCallersRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseRouteCallersRegistry: %v", err)
	}
	result := reg.LookupByRoute("/community-checkout/checkout")
	if result == nil {
		t.Fatal("expected match, got nil")
	}
	if len(result.Callers) != 1 || result.Callers[0].Repo != "long-match" {
		t.Errorf("expected long-match to win, got %+v", result)
	}
}

func TestRouteCallersRegistry_LookupByRoute_UnknownRoute_ReturnsNil(t *testing.T) {
	reg := loadTestRouteCallersRegistry(t)
	if got := reg.LookupByRoute("/unknown/route/path"); got != nil {
		t.Errorf("expected nil for unknown route, got %+v", got)
	}
}

func TestResolveRouteCallers_ControllerPrefixCombined(t *testing.T) {
	reg := loadTestRouteCallersRegistry(t)
	routes := []RouteInfo{
		{Method: "Post", Path: "checkout"},
	}
	results := ResolveRouteCallers("community-checkout", routes, reg)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result, got 0")
	}
	// Flatten MFA app keys
	appKeys := make(map[string]bool)
	for _, r := range results {
		for _, c := range r.Callers {
			for _, k := range c.MFAAppKeys {
				appKeys[k] = true
			}
		}
	}
	if !appKeys["communitiesApp"] {
		t.Errorf("expected communitiesApp in results, got %v", appKeys)
	}
	if !appKeys["membership-courses-portal"] {
		t.Errorf("expected membership-courses-portal in results, got %v", appKeys)
	}
}

func TestResolveRouteCallers_NoMatches_ReturnsEmpty(t *testing.T) {
	reg := loadTestRouteCallersRegistry(t)
	routes := []RouteInfo{
		{Method: "Get", Path: "something-unknown"},
	}
	results := ResolveRouteCallers("completely-unknown-controller", routes, reg)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestRouteCallersRegistry_NilRegistry_Safe(t *testing.T) {
	var reg *RouteCallersRegistry
	if got := reg.LookupByRoute("/anything"); got != nil {
		t.Errorf("nil registry LookupByRoute should return nil, got %+v", got)
	}
	results := ResolveRouteCallers("ctrl", []RouteInfo{{Method: "Get", Path: "x"}}, nil)
	if len(results) != 0 {
		t.Errorf("nil registry ResolveRouteCallers should return empty")
	}
}

func TestRouteCallersRegistry_DefaultEmbedLoads(t *testing.T) {
	reg, err := LoadDefaultRouteCallersRegistry()
	if err != nil {
		t.Fatalf("LoadDefaultRouteCallersRegistry: %v", err)
	}
	// Should find callers for the community-checkout route
	result := reg.LookupByRoute("/community-checkout/checkout")
	if result == nil {
		t.Error("expected /community-checkout/ in default registry, got nil")
	}
}

// PR #10133 exact case: @Controller('community-checkout') + @Post('checkout')
// → /community-checkout/checkout → callers include communitiesApp + membership-courses-portal
func TestRouteCallersRegistry_PR10133_CommunityCheckout_FindsCallers(t *testing.T) {
	reg := loadTestRouteCallersRegistry(t)
	routes := []RouteInfo{
		{Method: "Post", Path: "checkout"},
	}
	results := ResolveRouteCallers("community-checkout", routes, reg)
	if len(results) == 0 {
		t.Fatal("PR #10133: expected callers for community-checkout/checkout, got none")
	}
	appKeys := make(map[string]bool)
	for _, r := range results {
		for _, c := range r.Callers {
			for _, k := range c.MFAAppKeys {
				appKeys[k] = true
			}
		}
	}
	if !appKeys["communitiesApp"] {
		t.Errorf("PR #10133: communitiesApp not found in %v", appKeys)
	}
	if !appKeys["membership-courses-portal"] {
		t.Errorf("PR #10133: membership-courses-portal not found in %v", appKeys)
	}
}

func TestResolveRouteCallers_DeduplicatesMFAAppKeys(t *testing.T) {
	// Two routes, both matching the same prefix → should not duplicate callers
	yaml := `
callers:
  - path_prefix: "/community-checkout/"
    description: "checkout"
    callers:
      - repo: "ghl-revex-frontend"
        mfa_app_keys: ["communitiesApp"]
        call_patterns: []
        notes: ""
`
	reg, err := parseRouteCallersRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseRouteCallersRegistry: %v", err)
	}
	routes := []RouteInfo{
		{Method: "Post", Path: "checkout"},
		{Method: "Get", Path: "status"},
	}
	results := ResolveRouteCallers("community-checkout", routes, reg)
	// Both routes match /community-checkout/ — result should not have duplicate path entries
	seen := make(map[string]bool)
	for _, r := range results {
		if seen[r.PathPrefix] {
			t.Errorf("duplicate PathPrefix %q in results", r.PathPrefix)
		}
		seen[r.PathPrefix] = true
	}
}
