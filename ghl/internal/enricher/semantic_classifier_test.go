package enricher

import "testing"

func TestClassifySemanticProducts_CommunityCheckout(t *testing.T) {
	source := `
import { Controller } from '@nestjs/common'

@Controller('community-checkout')
export class CommunityCheckoutService {}
`

	got := ClassifySemanticProducts(source, "apps/courses/src/community-checkout/community-checkout.service.ts")
	top := requireTopSemanticProduct(t, got)

	if top.Domain != "Communities — Checkout" {
		t.Fatalf("top domain = %q, want %q", top.Domain, "Communities — Checkout")
	}
	if top.Confidence < 0.5 {
		t.Fatalf("top confidence = %0.2f, want >= 0.50", top.Confidence)
	}
}

func TestClassifySemanticProducts_MembershipCheckout(t *testing.T) {
	source := `
import { CheckoutProcess } from '../checkout-process'

export class CheckoutOrchestrationService {}
`

	got := ClassifySemanticProducts(source, "apps/memberships/src/checkout/checkout-orchestration.service.ts")
	top := requireTopSemanticProduct(t, got)

	if top.Domain != "Memberships — Checkout" {
		t.Fatalf("top domain = %q, want %q", top.Domain, "Memberships — Checkout")
	}
	if top.Confidence < 0.5 {
		t.Fatalf("top confidence = %0.2f, want >= 0.50", top.Confidence)
	}
}

func TestClassifySemanticProducts_NoMatch(t *testing.T) {
	source := `
export const noop = () => true
export function sum(a, b) { return a + b }
`

	got := ClassifySemanticProducts(source, "src/utils/noop.ts")
	if got != nil {
		t.Fatalf("expected nil result, got %#v", got)
	}
}

func TestClassifySemanticProducts_MultipleMatches_SortedByConfidence(t *testing.T) {
	source := `
import { CheckoutClient } from 'community-checkout'
import { FeedItem } from 'communities/feed'

@Controller('community-checkout')
export class CommunityCheckoutController {}
`

	got := ClassifySemanticProducts(source, "apps/communities/src/community-checkout/community-checkout.controller.ts")
	if len(got) < 2 {
		t.Fatalf("expected at least 2 semantic products, got %d", len(got))
	}
	if got[0].Confidence < got[1].Confidence {
		t.Fatalf("results not sorted by confidence descending: %#v", got)
	}
	if got[0].Domain != "Communities — Checkout" {
		t.Fatalf("top domain = %q, want %q", got[0].Domain, "Communities — Checkout")
	}
}

func TestClassifySemanticProducts_EmptySourceFilePathSignal(t *testing.T) {
	got := ClassifySemanticProducts("", "community-checkout.service.ts")
	top := requireTopSemanticProduct(t, got)

	if top.Domain != "Communities — Checkout" {
		t.Fatalf("top domain = %q, want %q", top.Domain, "Communities — Checkout")
	}
}

func TestClassifySemanticProducts_PR10133_CommunityCheckoutController(t *testing.T) {
	source := `
import { Controller } from '@nestjs/common'

@Controller('community-checkout')
export class CommunityCheckoutController {}
`

	got := ClassifySemanticProducts(source, "apps/courses/src/community-checkout/community-checkout.controller.ts")
	top := requireTopSemanticProduct(t, got)

	if top.Domain != "Communities — Checkout" {
		t.Fatalf("top domain = %q, want %q", top.Domain, "Communities — Checkout")
	}
	if top.Domain == "Courses — Authoring" {
		t.Fatalf("top domain unexpectedly matched legacy path classification: %q", top.Domain)
	}
}

func requireTopSemanticProduct(t *testing.T, got []SemanticProduct) SemanticProduct {
	t.Helper()

	if len(got) == 0 {
		t.Fatal("expected at least 1 semantic product")
	}

	return got[0]
}
