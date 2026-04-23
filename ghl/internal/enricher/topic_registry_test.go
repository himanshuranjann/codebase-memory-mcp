package enricher

import (
	"testing"
)

// ---------------------------------------------------------------------------
// TopicRegistry tests
// ---------------------------------------------------------------------------

func TestTopicRegistry_LookupByAlias_CheckoutIntegrations(t *testing.T) {
	yaml := `
topics:
  - id: "CHECKOUT_INTEGRATIONS"
    aliases:
      - "CHECKOUT_ORCHESTRATION_INTEGRATIONS"
      - "CheckoutOrchestrationWorkerEvent.CHECKOUT_ORCHESTRATION_INTEGRATIONS"
    description: "Checkout integrations worker"
    subscriber_service: "revex-membership-checkout-integrations-worker"
    subscriber_repo: "ghl-revex-backend"
    product_areas:
      - product: "Memberships — Checkout Flow"
        owner: "@revex-membership"
        user_impact: "Drips/triggers/analytics lost"
      - product: "Communities — Checkout Flow"
        owner: "@revex-communities"
        user_impact: "Post-purchase integrations lost"
    mfa_app_keys:
      - "membership-courses-portal"
      - "communities-member-portal"
`
	reg, err := parseTopicRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}

	impact := reg.LookupByTopicID("CHECKOUT_ORCHESTRATION_INTEGRATIONS")
	if impact == nil {
		t.Fatal("expected match on alias CHECKOUT_ORCHESTRATION_INTEGRATIONS, got nil")
	}
	if impact.TopicID != "CHECKOUT_INTEGRATIONS" {
		t.Errorf("TopicID = %q, want CHECKOUT_INTEGRATIONS", impact.TopicID)
	}
	if len(impact.ProductAreas) != 2 {
		t.Errorf("ProductAreas len = %d, want 2", len(impact.ProductAreas))
	}
}

func TestTopicRegistry_LookupByCanonicalID(t *testing.T) {
	yaml := `
topics:
  - id: "CHECKOUT_INTEGRATIONS"
    aliases: []
    subscriber_service: "worker"
    subscriber_repo: "repo"
    product_areas:
      - product: "Memberships"
        owner: "@team"
        user_impact: "impact"
    mfa_app_keys: []
`
	reg, err := parseTopicRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}
	impact := reg.LookupByTopicID("CHECKOUT_INTEGRATIONS")
	if impact == nil {
		t.Fatal("expected match on canonical ID, got nil")
	}
	if impact.SubscriberService != "worker" {
		t.Errorf("SubscriberService = %q, want worker", impact.SubscriberService)
	}
}

func TestTopicRegistry_LookupUnknown_ReturnsNil(t *testing.T) {
	yaml := `topics: []`
	reg, err := parseTopicRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}
	if got := reg.LookupByTopicID("NONEXISTENT_TOPIC"); got != nil {
		t.Errorf("expected nil for unknown topic, got %+v", got)
	}
}

func TestTopicRegistry_CaseInsensitiveLookup(t *testing.T) {
	yaml := `
topics:
  - id: "CHECKOUT_INTEGRATIONS"
    aliases:
      - "checkout_integrations"
    subscriber_service: "worker"
    subscriber_repo: "repo"
    product_areas: []
    mfa_app_keys: []
`
	reg, err := parseTopicRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}
	// lookup with different casing
	if reg.LookupByTopicID("checkout_integrations") == nil {
		t.Error("expected case-insensitive match on alias, got nil")
	}
	if reg.LookupByTopicID("CHECKOUT_INTEGRATIONS") == nil {
		t.Error("expected case-insensitive match on canonical ID, got nil")
	}
	if reg.LookupByTopicID("Checkout_Integrations") == nil {
		t.Error("expected case-insensitive match on mixed case, got nil")
	}
}

func TestResolveEventChainImpact_ProducerMatches(t *testing.T) {
	yaml := `
topics:
  - id: "CHECKOUT_INTEGRATIONS"
    aliases: []
    subscriber_service: "worker"
    subscriber_repo: "repo"
    product_areas:
      - product: "Memberships"
        owner: "@revex-membership"
        user_impact: "broken"
    mfa_app_keys: ["membership-courses-portal"]
`
	reg, err := parseTopicRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}
	events := []EventPatternCall{
		{Topic: "CHECKOUT_INTEGRATIONS", Role: "producer", Symbol: "SomeService", FilePath: "a.ts"},
	}
	impacts := ResolveEventChainImpact(events, reg)
	if len(impacts) != 1 {
		t.Fatalf("ResolveEventChainImpact len = %d, want 1", len(impacts))
	}
	if impacts[0].TopicID != "CHECKOUT_INTEGRATIONS" {
		t.Errorf("TopicID = %q", impacts[0].TopicID)
	}
	if len(impacts[0].MFAAppKeys) != 1 {
		t.Errorf("MFAAppKeys len = %d, want 1", len(impacts[0].MFAAppKeys))
	}
}

func TestResolveEventChainImpact_ConsumerOnly_ReturnsNil(t *testing.T) {
	yaml := `
topics:
  - id: "CHECKOUT_INTEGRATIONS"
    aliases: []
    subscriber_service: "worker"
    subscriber_repo: "repo"
    product_areas: []
    mfa_app_keys: []
`
	reg, err := parseTopicRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}
	// consumer-only events should not produce impacts
	events := []EventPatternCall{
		{Topic: "CHECKOUT_INTEGRATIONS", Role: "consumer", Symbol: "Worker", FilePath: "worker.ts"},
	}
	impacts := ResolveEventChainImpact(events, reg)
	if len(impacts) != 0 {
		t.Errorf("expected no impacts for consumer-only events, got %d", len(impacts))
	}
}

func TestExtractPublisherStepTopics_ExtractsEnumConstant(t *testing.T) {
	source := `
new PublisherStep(
  CheckoutStepsName.CHECKOUT_PUBLISH_TO_INTEGRATIONS,
  CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS,
  CheckoutOrchestrationWorkerEvent.CHECKOUT_ORCHESTRATION_INTEGRATIONS,
)
`
	topics := ExtractPublisherStepTopics(source)
	if len(topics) == 0 {
		t.Fatal("expected at least 1 topic extracted, got 0")
	}
	// Should contain the last segment of each enum reference
	found := false
	for _, tp := range topics {
		if tp == "CHECKOUT_INTEGRATIONS" || tp == "CHECKOUT_ORCHESTRATION_INTEGRATIONS" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CHECKOUT_INTEGRATIONS or CHECKOUT_ORCHESTRATION_INTEGRATIONS in %v", topics)
	}
}

// PR #10133 exact case: community-checkout-orchestrator.config.ts
// contains: new PublisherStep(name, CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS, ...)
// → should resolve to both Memberships and Communities product areas
func TestTopicRegistry_PR10133_CommunityCheckoutOrchestratorConfig(t *testing.T) {
	source := `
new PublisherStep(
  CheckoutStepsName.CHECKOUT_PUBLISH_TO_INTEGRATIONS,
  CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS,
  CheckoutOrchestrationWorkerEvent.CHECKOUT_ORCHESTRATION_INTEGRATIONS,
)
`
	registryYAML := `
topics:
  - id: "CHECKOUT_INTEGRATIONS"
    aliases:
      - "CHECKOUT_ORCHESTRATION_INTEGRATIONS"
    description: "Checkout integrations worker"
    subscriber_service: "revex-membership-checkout-integrations-worker"
    subscriber_repo: "ghl-revex-backend"
    product_areas:
      - product: "Memberships — Checkout Flow"
        owner: "@revex-membership"
        user_impact: "Drips/triggers/analytics lost"
      - product: "Communities — Checkout Flow"
        owner: "@revex-communities"
        user_impact: "Post-purchase integrations lost"
    mfa_app_keys:
      - "membership-courses-portal"
      - "communities-member-portal"
`
	reg, err := parseTopicRegistry([]byte(registryYAML))
	if err != nil {
		t.Fatalf("parseTopicRegistry: %v", err)
	}

	// Step 1: extract topic IDs from PublisherStep source
	extracted := ExtractPublisherStepTopics(source)
	if len(extracted) == 0 {
		t.Fatal("expected topics extracted from PublisherStep source, got none")
	}

	// Step 2: synthesize EventPatternCall records from extracted topics
	var events []EventPatternCall
	for _, tp := range extracted {
		events = append(events, EventPatternCall{
			Topic:    tp,
			Role:     "producer",
			Symbol:   "CommunityCheckoutOrchestratorConfig",
			FilePath: "apps/courses/src/checkout-process/config/community-checkout-orchestrator.config.ts",
		})
	}

	// Step 3: resolve chain impact
	impacts := ResolveEventChainImpact(events, reg)
	if len(impacts) == 0 {
		t.Fatal("expected chain impact for PR #10133 community checkout, got none")
	}

	// Must surface both Memberships and Communities
	products := make(map[string]bool)
	for _, imp := range impacts {
		for _, pa := range imp.ProductAreas {
			products[pa.Product] = true
		}
	}
	if !products["Memberships — Checkout Flow"] {
		t.Errorf("expected Memberships — Checkout Flow in impacts, got %v", products)
	}
	if !products["Communities — Checkout Flow"] {
		t.Errorf("expected Communities — Checkout Flow in impacts, got %v", products)
	}

	// Must include both MFA app keys
	appKeys := make(map[string]bool)
	for _, imp := range impacts {
		for _, k := range imp.MFAAppKeys {
			appKeys[k] = true
		}
	}
	if !appKeys["membership-courses-portal"] {
		t.Errorf("expected membership-courses-portal in MFAAppKeys, got %v", appKeys)
	}
	if !appKeys["communities-member-portal"] {
		t.Errorf("expected communities-member-portal in MFAAppKeys, got %v", appKeys)
	}
}

func TestTopicRegistry_NilRegistry_SafeLookup(t *testing.T) {
	var reg *TopicRegistry
	if got := reg.LookupByTopicID("anything"); got != nil {
		t.Errorf("nil registry LookupByTopicID should return nil, got %+v", got)
	}
	impacts := ResolveEventChainImpact([]EventPatternCall{{Topic: "x", Role: "producer"}}, nil)
	if len(impacts) != 0 {
		t.Errorf("nil registry ResolveEventChainImpact should return empty, got %v", impacts)
	}
}

func TestTopicRegistry_DefaultEmbedLoads(t *testing.T) {
	reg, err := LoadDefaultTopicRegistry()
	if err != nil {
		t.Fatalf("LoadDefaultTopicRegistry: %v", err)
	}
	// Should have at least the key checkout integration topic
	impact := reg.LookupByTopicID("CHECKOUT_INTEGRATIONS")
	if impact == nil {
		t.Error("expected CHECKOUT_INTEGRATIONS in default registry, got nil")
	}
	if impact != nil && len(impact.ProductAreas) < 2 {
		t.Errorf("CHECKOUT_INTEGRATIONS should have ≥2 product areas, got %d", len(impact.ProductAreas))
	}
}
