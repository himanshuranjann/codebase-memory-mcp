package enricher

import (
	"testing"
)

func TestBuildImpactReport_EmptySurface_ReturnsLowConfidence(t *testing.T) {
	r := BuildImpactReport(CustomerSurface{})
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5", r.Confidence)
	}
	if r.MaxSeverity != SeverityLow {
		t.Errorf("MaxSeverity = %q, want LOW", r.MaxSeverity)
	}
}

func TestBuildImpactReport_CheckoutPath_SetsCritical(t *testing.T) {
	cs := CustomerSurface{
		FilePath: "apps/courses/src/community-checkout/community-checkout.controller.ts",
		RouteCallers: []RouteCallersResult{{
			PathPrefix: "/community-checkout/",
			Callers: []RouteCallerEntry{{Repo: "r", MFAAppKeys: []string{"communitiesApp"}}},
		}},
	}
	r := BuildImpactReport(cs)
	if r.MaxSeverity != SeverityCritical {
		t.Errorf("MaxSeverity = %q, want CRITICAL", r.MaxSeverity)
	}
}

func TestBuildImpactReport_WorkerFile_SetsSilent(t *testing.T) {
	cs := CustomerSurface{
		EventChainImpacts: []TopicImpact{{
			TopicID:           "CHECKOUT_INTEGRATIONS",
			SubscriberService: "notifications-worker",
			ProductAreas:      []TopicProductArea{{Product: "Memberships", UserImpact: "emails not sent"}},
			MFAAppKeys:        []string{"membership-courses-portal"},
		}},
	}
	r := BuildImpactReport(cs)
	if !r.HasSilentFailure {
		t.Errorf("expected HasSilentFailure=true for worker impact")
	}
}

func TestBuildImpactReport_ProductModuleSplit(t *testing.T) {
	tests := []struct {
		in          string
		wantProd    string
		wantModule  string
	}{
		{"Memberships — Checkout Flow", "Memberships", "Checkout Flow"},
		{"Communities — Member Portal", "Communities", "Member Portal"},
		{"Courses — Learner Access", "Courses", "Learner Access"},
		{"GoKollab — Workspace", "GoKollab", "Workspace"},
		{"Memberships", "Memberships", ""},
	}
	for _, tt := range tests {
		p, m := splitProductModule(tt.in)
		if p != tt.wantProd || m != tt.wantModule {
			t.Errorf("splitProductModule(%q) = (%q,%q), want (%q,%q)",
				tt.in, p, m, tt.wantProd, tt.wantModule)
		}
	}
}

func TestBuildImpactReport_DeduplicatesAffectedSurfaces(t *testing.T) {
	cs := CustomerSurface{
		FilePath: "x.controller.ts",
		EventChainImpacts: []TopicImpact{{
			ProductAreas: []TopicProductArea{{Product: "Memberships", UserImpact: "broken"}},
			MFAAppKeys:   []string{"dup-app"},
		}},
		RouteCallers: []RouteCallersResult{{
			PathPrefix: "/x/",
			Callers:    []RouteCallerEntry{{Repo: "r", MFAAppKeys: []string{"dup-app"}}},
		}},
	}
	r := BuildImpactReport(cs)
	seen := 0
	for _, s := range r.AffectedSurfaces {
		if s.MFAAppKey == "dup-app" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("expected dup-app to appear once, got %d", seen)
	}
}

func TestBuildImpactReport_ConfidenceScaling(t *testing.T) {
	// 0 signals
	r0 := BuildImpactReport(CustomerSurface{})
	// 1 signal: product map
	r1 := BuildImpactReport(CustomerSurface{Product: "Memberships"})
	// 2 signals: product map + semantic
	r2 := BuildImpactReport(CustomerSurface{
		Product:          "Memberships",
		SemanticProducts: []SemanticProduct{{Domain: "Memberships — Checkout"}},
	})
	// 3 signals: + event chain
	r3 := BuildImpactReport(CustomerSurface{
		Product:          "Memberships",
		SemanticProducts: []SemanticProduct{{Domain: "Memberships — Checkout"}},
		EventChainImpacts: []TopicImpact{{
			ProductAreas: []TopicProductArea{{Product: "Memberships — Checkout"}},
			MFAAppKeys:   []string{"x"},
		}},
	})

	if !(r0.Confidence < r1.Confidence && r1.Confidence < r2.Confidence && r2.Confidence < r3.Confidence) {
		t.Errorf("confidence should increase with signal count: r0=%v r1=%v r2=%v r3=%v",
			r0.Confidence, r1.Confidence, r2.Confidence, r3.Confidence)
	}
}

func TestBuildImpactReport_PR10133_CommunityCheckoutController(t *testing.T) {
	cs := CustomerSurface{
		Repo:     "ghl-revex-backend",
		FilePath: "apps/courses/src/community-checkout/community-checkout.controller.ts",
		SemanticProducts: []SemanticProduct{{
			Domain: "Communities — Checkout", Owner: "@revex-communities", Confidence: 0.9,
		}},
		RouteCallers: []RouteCallersResult{{
			PathPrefix:  "/community-checkout/",
			Description: "Community checkout orchestration API",
			Callers: []RouteCallerEntry{
				{Repo: "ghl-revex-frontend", MFAAppKeys: []string{"communitiesApp"},
					Notes: "Communities frontend triggers checkout for community offers"},
				{Repo: "ghl-membership-frontend", MFAAppKeys: []string{"membership-courses-portal"},
					Notes: "Membership portal handles community offer checkout flows"},
			},
		}},
		EventChainImpacts: []TopicImpact{{
			TopicID:           "CHECKOUT_INTEGRATIONS",
			SubscriberService: "revex-membership-checkout-integrations-worker",
			ProductAreas: []TopicProductArea{
				{Product: "Memberships — Checkout Flow", Owner: "@revex-membership",
					UserImpact: "Membership students lose drip sequences, external trigger webhooks, and analytics events post-purchase"},
				{Product: "Communities — Checkout Flow", Owner: "@revex-communities",
					UserImpact: "Community members lose post-purchase integrations (drips, webhooks, analytics) after community offer checkout"},
			},
			MFAAppKeys: []string{"membership-courses-portal", "communities-member-portal"},
		}},
	}
	r := BuildImpactReport(cs)

	if r.Product == "" || r.Product == UnknownProductLabel {
		t.Errorf("Product should be set, got %q", r.Product)
	}
	if len(r.AffectedSurfaces) < 2 {
		t.Errorf("expected ≥2 affected surfaces, got %d", len(r.AffectedSurfaces))
	}
	if r.MaxSeverity != SeverityCritical && r.MaxSeverity != SeverityHigh {
		t.Errorf("MaxSeverity = %q, want CRITICAL or HIGH", r.MaxSeverity)
	}
	if !r.HasSilentFailure {
		t.Errorf("expected HasSilentFailure=true (worker background failures)")
	}
	if r.Confidence < 0.7 {
		t.Errorf("Confidence = %v, want ≥ 0.7", r.Confidence)
	}

	signalSet := make(map[string]bool)
	for _, s := range r.Signals {
		signalSet[s] = true
	}
	for _, want := range []string{"event-chain", "route-callers", "semantic"} {
		if !signalSet[want] {
			t.Errorf("expected signal %q in %v", want, r.Signals)
		}
	}

	mfaKeys := make(map[string]bool)
	for _, s := range r.AffectedSurfaces {
		mfaKeys[s.MFAAppKey] = true
	}
	if !mfaKeys["communitiesApp"] {
		t.Errorf("expected communitiesApp in AffectedSurfaces: %v", mfaKeys)
	}
	if !mfaKeys["membership-courses-portal"] {
		t.Errorf("expected membership-courses-portal in AffectedSurfaces: %v", mfaKeys)
	}

	silentFound := false
	for _, s := range r.AffectedSurfaces {
		if s.Silent {
			silentFound = true
		}
	}
	if !silentFound {
		t.Errorf("expected at least one Silent=true surface")
	}
}
