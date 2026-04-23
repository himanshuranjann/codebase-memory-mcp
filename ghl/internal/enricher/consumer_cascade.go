package enricher

import (
	"regexp"
	"strings"
)

// SideEffectKind classifies what a consumer worker produces for users.
type SideEffectKind string

const (
	SideEffectEmail        SideEffectKind = "email"
	SideEffectDrip         SideEffectKind = "drip_sequence"
	SideEffectWebhook      SideEffectKind = "webhook"
	SideEffectAnalytics    SideEffectKind = "analytics"
	SideEffectAccessGrant  SideEffectKind = "access_grant"
	SideEffectNotification SideEffectKind = "notification"
	SideEffectUnknown      SideEffectKind = "unknown"
)

// ConsumerSideEffect describes one side effect a consumer worker produces.
type ConsumerSideEffect struct {
	Kind        SideEffectKind
	Description string
	IsSilent    bool   // workers run in background — user sees no error
	Severity    string // "HIGH" | "MEDIUM" | "LOW"
}

// ConsumerCascadeResult describes the full cascade impact of a changed consumer file.
type ConsumerCascadeResult struct {
	Topic             string
	Role              string
	FilePath          string
	SideEffects       []ConsumerSideEffect
	MaxSeverity       string
	UserImpactSummary string
}

// sideEffectRule matches source patterns to classify worker side effects.
type sideEffectRule struct {
	Kind        SideEffectKind
	Pattern     *regexp.Regexp
	Description string
	Severity    string
}

var sideEffectRules = []sideEffectRule{
	{
		SideEffectAccessGrant,
		regexp.MustCompile(`grantAccess|AccessGrant|membershipAccess|grantMembership|accessGrantService`),
		"User access grant broken — purchased product not accessible",
		"HIGH",
	},
	{
		SideEffectEmail,
		regexp.MustCompile(`sendEmail|sendMail|mailerService|MailerService|nodemailer|@nestjs/mailer`),
		"Welcome/confirmation emails not sent to user",
		"MEDIUM",
	},
	{
		SideEffectDrip,
		regexp.MustCompile(`dripService|DripService|addToDrip|createDrip|drip.*sequence`),
		"Drip sequences not started for user",
		"MEDIUM",
	},
	{
		SideEffectWebhook,
		regexp.MustCompile(`webhookService|WebhookService|triggerWebhook|sendWebhook|externalTrigger|ExternalTrigger`),
		"External webhook triggers not fired",
		"MEDIUM",
	},
	{
		SideEffectNotification,
		regexp.MustCompile(`notificationService|NotificationService|pushNotification|fcmService|sendPush`),
		"Push notifications not sent",
		"MEDIUM",
	},
	{
		SideEffectAnalytics,
		regexp.MustCompile(`analyticsService|AnalyticsService|\.track\(|segment\.track|mixpanel|amplitude`),
		"Analytics events not tracked",
		"LOW",
	},
}

// ExtractConsumerSideEffects analyzes a consumer worker's source code to
// determine what side effects it produces. Returns nil for empty source.
func ExtractConsumerSideEffects(source string) []ConsumerSideEffect {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	var effects []ConsumerSideEffect
	for _, rule := range sideEffectRules {
		if rule.Pattern.MatchString(source) {
			effects = append(effects, ConsumerSideEffect{
				Kind:        rule.Kind,
				Description: rule.Description,
				IsSilent:    true,
				Severity:    rule.Severity,
			})
		}
	}
	return effects
}

// ResolveConsumerCascade resolves downstream side effects for consumer events
// found in a changed file. Producer events are skipped.
// Returns nil when no consumer events produce detectable side effects.
func ResolveConsumerCascade(consumerEvents []EventPatternCall, source string, topicReg *TopicRegistry) []ConsumerCascadeResult {
	var results []ConsumerCascadeResult
	for _, ev := range consumerEvents {
		if ev.Role != "consumer" {
			continue
		}
		effects := ExtractConsumerSideEffects(source)
		if len(effects) == 0 {
			continue
		}

		maxSev := "LOW"
		for _, e := range effects {
			switch e.Severity {
			case "HIGH":
				maxSev = "HIGH"
			case "MEDIUM":
				if maxSev == "LOW" {
					maxSev = "MEDIUM"
				}
			}
		}

		var descs []string
		for _, e := range effects {
			descs = append(descs, e.Description)
		}
		summary := strings.Join(descs, "; ")

		// Augment with topic registry user_impact if available.
		if topicReg != nil {
			if impact := topicReg.LookupByTopicID(ev.Topic); impact != nil {
				for _, pa := range impact.ProductAreas {
					if pa.UserImpact != "" {
						summary = pa.UserImpact + " (" + summary + ")"
						break
					}
				}
			}
		}

		results = append(results, ConsumerCascadeResult{
			Topic:             ev.Topic,
			Role:              ev.Role,
			FilePath:          ev.FilePath,
			SideEffects:       effects,
			MaxSeverity:       maxSev,
			UserImpactSummary: summary,
		})
	}
	if len(results) == 0 {
		return nil
	}
	return results
}
