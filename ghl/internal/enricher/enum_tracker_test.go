package enricher

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ExtractEnumDefinitions
// ---------------------------------------------------------------------------

func TestExtractEnumDefinitions_TypeScriptEnum(t *testing.T) {
	src := `export enum CheckoutWorkerNames {
  CHECKOUT_INTEGRATIONS = 'checkout-integrations',
  CHECKOUT_NOTIFICATIONS = 'checkout-notifications',
  CHECKOUT_ACCESS_GRANT = 'checkout-access-grant',
}`
	got := ExtractEnumDefinitions(src, "apps/courses/workers/checkout-workers/constants.ts")
	if len(got) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(got))
	}
	if got[0].Name != "CheckoutWorkerNames" {
		t.Errorf("Name=%q", got[0].Name)
	}
	if got[0].Kind != "enum" {
		t.Errorf("Kind=%q", got[0].Kind)
	}
	if len(got[0].Members) != 3 {
		t.Errorf("Members len=%d, want 3", len(got[0].Members))
	}
	found := false
	for _, m := range got[0].Members {
		if m.Name == "CHECKOUT_INTEGRATIONS" && m.Value == "checkout-integrations" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CHECKOUT_INTEGRATIONS=checkout-integrations, got %+v", got[0].Members)
	}
}

func TestExtractEnumDefinitions_ClassStaticTOPICS(t *testing.T) {
	src := `export class CheckoutOrchestratorConfig {
  static TOPICS = {
    CHECKOUT_INTEGRATIONS: 'checkout-integrations',
    CHECKOUT_ORCHESTRATION: 'checkout-orchestration',
  };
}`
	got := ExtractEnumDefinitions(src, "config.ts")
	if len(got) == 0 {
		t.Fatal("expected 1 class-static enum, got none")
	}
	found := false
	for _, def := range got {
		if def.Name == "TOPICS" && def.Kind == "class_static" {
			found = true
			memberNames := make(map[string]bool)
			for _, m := range def.Members {
				memberNames[m.Name] = true
			}
			if !memberNames["CHECKOUT_INTEGRATIONS"] {
				t.Errorf("expected CHECKOUT_INTEGRATIONS in members, got %v", memberNames)
			}
		}
	}
	if !found {
		t.Errorf("expected TOPICS class_static enum, got %+v", got)
	}
}

func TestExtractEnumDefinitions_ConstObjectAsConst(t *testing.T) {
	src := `export const EventTypes = {
  USER_CREATED: 'user.created',
  USER_DELETED: 'user.deleted',
} as const;`
	got := ExtractEnumDefinitions(src, "events.ts")
	if len(got) != 1 {
		t.Fatalf("expected 1 const-object enum, got %d", len(got))
	}
	if got[0].Kind != "const_object" {
		t.Errorf("Kind=%q, want const_object", got[0].Kind)
	}
	if got[0].Name != "EventTypes" {
		t.Errorf("Name=%q, want EventTypes", got[0].Name)
	}
}

func TestExtractEnumDefinitions_EmptySource_ReturnsNil(t *testing.T) {
	if got := ExtractEnumDefinitions("", "x.ts"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ExtractEnumReferences
// ---------------------------------------------------------------------------

func TestExtractEnumReferences_DotChain_PR10133(t *testing.T) {
	// Exact pattern from PR #10133 community-checkout-orchestrator.config.ts
	src := `    new PublisherStep(
      CheckoutStepsName.CHECKOUT_PUBLISH_TO_INTEGRATIONS,
      CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS,
      CheckoutOrchestrationWorkerEvent.CHECKOUT_ORCHESTRATION_INTEGRATIONS,
    ),`
	got := ExtractEnumReferences(src, "community-checkout-orchestrator.config.ts")
	if len(got) < 3 {
		t.Fatalf("expected ≥3 refs, got %d: %+v", len(got), got)
	}
	seen := make(map[string]bool)
	for _, r := range got {
		seen[r.MemberName] = true
	}
	for _, want := range []string{"CHECKOUT_PUBLISH_TO_INTEGRATIONS", "CHECKOUT_INTEGRATIONS", "CHECKOUT_ORCHESTRATION_INTEGRATIONS"} {
		if !seen[want] {
			t.Errorf("expected MemberName %q in refs, got %v", want, seen)
		}
	}
}

func TestExtractEnumReferences_ContainerPath(t *testing.T) {
	src := `publish(CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS, payload);`
	got := ExtractEnumReferences(src, "x.ts")
	if len(got) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(got))
	}
	if got[0].FullReference != "CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS" {
		t.Errorf("FullReference=%q", got[0].FullReference)
	}
	if len(got[0].ContainerPath) != 2 ||
		got[0].ContainerPath[0] != "CheckoutOrchestratorConfig" ||
		got[0].ContainerPath[1] != "TOPICS" {
		t.Errorf("ContainerPath=%v", got[0].ContainerPath)
	}
	if got[0].MemberName != "CHECKOUT_INTEGRATIONS" {
		t.Errorf("MemberName=%q", got[0].MemberName)
	}
}

func TestExtractEnumReferences_DeduplicatesWithinFile(t *testing.T) {
	src := `const a = Foo.BAR.VALUE;
const b = Foo.BAR.VALUE;
const c = Foo.BAR.VALUE;`
	got := ExtractEnumReferences(src, "x.ts")
	// Each occurrence is a distinct reference (different line), but MemberName dedupe set should still have one entry
	if len(got) != 3 {
		t.Errorf("expected 3 line-level refs, got %d", len(got))
	}
	lines := make(map[int]bool)
	for _, r := range got {
		lines[r.Line] = true
	}
	if len(lines) != 3 {
		t.Errorf("expected 3 distinct lines, got %v", lines)
	}
}

func TestExtractEnumReferences_IgnoresShortReferences(t *testing.T) {
	// Plain `foo.bar` (2-part) is NOT an enum reference — too many false positives.
	src := `this.service.method();
obj.field;`
	got := ExtractEnumReferences(src, "x.ts")
	if len(got) != 0 {
		t.Errorf("expected 0 refs for 2-part dot chains, got %d: %+v", len(got), got)
	}
}

func TestExtractEnumReferences_RequiresUpperCaseMember(t *testing.T) {
	// Enum members are conventionally UPPER_SNAKE_CASE. Filter out camelCase to reduce false positives.
	src := `foo.bar.someMethod();
foo.bar.CHECKOUT_INTEGRATIONS;`
	got := ExtractEnumReferences(src, "x.ts")
	if len(got) != 1 {
		t.Errorf("expected 1 ref (only the UPPER_CASE one), got %d: %+v", len(got), got)
	}
	if got[0].MemberName != "CHECKOUT_INTEGRATIONS" {
		t.Errorf("got MemberName %q", got[0].MemberName)
	}
}

func TestExtractEnumReferences_EmptySource_ReturnsNil(t *testing.T) {
	if got := ExtractEnumReferences("", "x.ts"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Integration: PR #10133 full orchestrator config source
// ---------------------------------------------------------------------------

func TestExtractEnumReferences_PR10133_FullOrchestratorSource(t *testing.T) {
	src := `import { PublisherStep, ValidationStep, AccessGrantStep } from '@platform-core/orchestrator';
import { CheckoutOrchestrationWorkerEvent } from '@platform-core/events';
import { CheckoutOrchestratorConfig } from '../CheckoutOrchestratorConfig';

export class CommunityCheckoutOrchestratorConfig extends CheckoutOrchestratorConfig {
  static STEPS = [
    new ValidationStep(),
    new AccessGrantStep(),
    new PublisherStep(
      CheckoutStepsName.CHECKOUT_PUBLISH_TO_INTEGRATIONS,
      CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS,
      CheckoutOrchestrationWorkerEvent.CHECKOUT_ORCHESTRATION_INTEGRATIONS,
    ),
  ];
}`
	refs := ExtractEnumReferences(src, "community-checkout-orchestrator.config.ts")

	memberNames := make(map[string]bool)
	for _, r := range refs {
		memberNames[r.MemberName] = true
	}

	required := []string{
		"CHECKOUT_PUBLISH_TO_INTEGRATIONS",
		"CHECKOUT_INTEGRATIONS",
		"CHECKOUT_ORCHESTRATION_INTEGRATIONS",
	}
	for _, want := range required {
		if !memberNames[want] {
			t.Errorf("PR #10133: expected enum ref %q, got %v", want, memberNames)
		}
	}

	// Verify the critical middle reference has correct container path (for topic registry lookup)
	var checkoutIntegrations *EnumReference
	for i := range refs {
		if refs[i].MemberName == "CHECKOUT_INTEGRATIONS" {
			checkoutIntegrations = &refs[i]
			break
		}
	}
	if checkoutIntegrations == nil {
		t.Fatal("CHECKOUT_INTEGRATIONS reference not found")
	}
	if len(checkoutIntegrations.ContainerPath) < 2 ||
		checkoutIntegrations.ContainerPath[0] != "CheckoutOrchestratorConfig" {
		t.Errorf("CHECKOUT_INTEGRATIONS ContainerPath=%v, want [CheckoutOrchestratorConfig, TOPICS]",
			checkoutIntegrations.ContainerPath)
	}
}
