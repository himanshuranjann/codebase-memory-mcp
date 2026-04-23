package enricher

import (
	"testing"
)

func TestExtractConsumerSideEffects_EmptySource_ReturnsNil(t *testing.T) {
	if got := ExtractConsumerSideEffects(""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestExtractConsumerSideEffects_DetectsEmail(t *testing.T) {
	src := `await this.mailerService.sendMail({to: user.email})`
	got := ExtractConsumerSideEffects(src)
	if len(got) != 1 || got[0].Kind != SideEffectEmail {
		t.Errorf("got %+v", got)
	}
	if !got[0].IsSilent {
		t.Errorf("IsSilent should be true for worker side effects")
	}
}

func TestExtractConsumerSideEffects_DetectsWebhook(t *testing.T) {
	src := `await this.webhookService.triggerWebhook(payload)`
	got := ExtractConsumerSideEffects(src)
	if len(got) != 1 || got[0].Kind != SideEffectWebhook {
		t.Errorf("got %+v", got)
	}
}

func TestExtractConsumerSideEffects_DetectsDrip(t *testing.T) {
	src := `await this.dripService.addToDrip(contactId, sequence)`
	got := ExtractConsumerSideEffects(src)
	if len(got) == 0 || got[0].Kind != SideEffectDrip {
		t.Errorf("got %+v", got)
	}
}

func TestExtractConsumerSideEffects_DetectsAccessGrant(t *testing.T) {
	src := `await this.accessGrantService.grantAccess(memberId, offerId)`
	got := ExtractConsumerSideEffects(src)
	if len(got) == 0 {
		t.Fatal("no side effects detected")
	}
	found := false
	for _, e := range got {
		if e.Kind == SideEffectAccessGrant && e.Severity == "HIGH" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HIGH-severity access_grant, got %+v", got)
	}
}

func TestExtractConsumerSideEffects_DetectsAnalytics(t *testing.T) {
	src := `this.analyticsService.track('checkout.completed', payload)`
	got := ExtractConsumerSideEffects(src)
	if len(got) == 0 {
		t.Fatal("no side effects detected")
	}
	found := false
	for _, e := range got {
		if e.Kind == SideEffectAnalytics && e.Severity == "LOW" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected LOW-severity analytics, got %+v", got)
	}
}

func TestExtractConsumerSideEffects_DetectsMultiple(t *testing.T) {
	src := `
		await this.mailerService.sendMail(...)
		await this.dripService.createDrip(...)
		await this.webhookService.triggerWebhook(...)
	`
	got := ExtractConsumerSideEffects(src)
	if len(got) < 3 {
		t.Errorf("expected ≥3 side effects, got %d", len(got))
	}
}

func TestResolveConsumerCascade_ProducerEventsSkipped(t *testing.T) {
	events := []EventPatternCall{{Topic: "X", Role: "producer"}}
	got := ResolveConsumerCascade(events, "this.dripService.addToDrip()", nil)
	if got != nil {
		t.Errorf("expected nil for producer-only events, got %v", got)
	}
}

func TestResolveConsumerCascade_EmptyEvents_ReturnsNil(t *testing.T) {
	if got := ResolveConsumerCascade(nil, "source", nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestResolveConsumerCascade_ConsumerWithSideEffects(t *testing.T) {
	events := []EventPatternCall{{Topic: "CHECKOUT_INTEGRATIONS", Role: "consumer"}}
	src := `await this.dripService.addToDrip(id); await this.webhookService.triggerWebhook(p);`
	got := ResolveConsumerCascade(events, src, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if len(got[0].SideEffects) < 2 {
		t.Errorf("expected ≥2 side effects, got %d", len(got[0].SideEffects))
	}
}

func TestResolveConsumerCascade_NilTopicRegistry_StillWorks(t *testing.T) {
	events := []EventPatternCall{{Topic: "X", Role: "consumer"}}
	src := `this.dripService.addToDrip()`
	got := ResolveConsumerCascade(events, src, nil)
	if len(got) != 1 {
		t.Errorf("expected 1 result even with nil registry, got %d", len(got))
	}
}

func TestResolveConsumerCascade_MaxSeverityFromAccessGrant(t *testing.T) {
	events := []EventPatternCall{{Topic: "X", Role: "consumer"}}
	src := `this.accessGrantService.grantAccess(memberId, offerId); this.analyticsService.track('x');`
	got := ResolveConsumerCascade(events, src, nil)
	if len(got) != 1 || got[0].MaxSeverity != "HIGH" {
		t.Errorf("expected MaxSeverity=HIGH, got %+v", got)
	}
}

func TestResolveConsumerCascade_PR10133_CheckoutIntegrationsWorker(t *testing.T) {
	src := `
		@EventPattern(CheckoutOrchestrationWorkerEvent.CHECKOUT_ORCHESTRATION_INTEGRATIONS)
		async handleCheckoutIntegrations(payload) {
			await this.dripService.createDrip(payload.contactId, payload.offerId);
			await this.externalTriggerService.triggerWebhook(payload);
			await this.analyticsService.track('checkout.completed', payload);
		}
	`
	events := []EventPatternCall{{Topic: "CHECKOUT_INTEGRATIONS", Role: "consumer", Symbol: "CheckoutIntegrationsWorker"}}
	got := ResolveConsumerCascade(events, src, nil)
	if len(got) == 0 {
		t.Fatal("PR #10133: expected cascade results, got none")
	}
	kinds := make(map[SideEffectKind]bool)
	for _, se := range got[0].SideEffects {
		kinds[se.Kind] = true
		if !se.IsSilent {
			t.Errorf("PR #10133: side effect %v should be silent", se.Kind)
		}
	}
	for _, want := range []SideEffectKind{SideEffectDrip, SideEffectWebhook, SideEffectAnalytics} {
		if !kinds[want] {
			t.Errorf("PR #10133: expected side effect %v in results", want)
		}
	}
	if got[0].UserImpactSummary == "" {
		t.Errorf("PR #10133: UserImpactSummary should not be empty")
	}
}
