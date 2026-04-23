package enricher

import (
	"strings"
)

// ImpactSeverity classifies how badly users are affected.
type ImpactSeverity string

const (
	SeverityCritical ImpactSeverity = "CRITICAL" // transaction/purchase blocked
	SeverityHigh     ImpactSeverity = "HIGH"      // feature broken, data loss risk
	SeverityMedium   ImpactSeverity = "MEDIUM"    // degraded UX, delayed side effects
	SeverityLow      ImpactSeverity = "LOW"       // cosmetic, notification delay
)

// AffectedSurface is one user-facing surface and what breaks there.
type AffectedSurface struct {
	MFAAppKey   string         // e.g. "communitiesApp"
	AppName     string         // e.g. "Communities App"
	UserAction  string         // e.g. "Purchasing a community offer"
	FailureMode string         // e.g. "Post-purchase drip sequences not triggered"
	Severity    ImpactSeverity
	Silent      bool   // true = user sees no error but data/flow is broken
	Source      string // "route-callers" | "event-chain" | "semantic" | "product-map"
}

// CustomerImpactReport is the final structured output per file.
// Aggregates ALL signals into exact, actionable customer impact.
type CustomerImpactReport struct {
	Repo     string
	FilePath string

	Product   string // e.g. "Communities"
	Module    string // e.g. "Checkout Flow"
	OwnerTeam string

	AffectedSurfaces []AffectedSurface

	BlastRadius  string  // human description of who is affected
	UserSegment  string  // e.g. "Community members + membership portal users"
	Confidence   float64 // 0.0–1.0
	SignalCount  int
	Signals      []string // which signal types fired

	HasSilentFailure bool
	HasBreakingDTO   bool
	MaxSeverity      ImpactSeverity
}

// BuildImpactReport aggregates all signals from a CustomerSurface into a
// structured CustomerImpactReport. Pure function — deterministic, no I/O.
func BuildImpactReport(cs CustomerSurface) CustomerImpactReport {
	r := CustomerImpactReport{
		Repo:      cs.Repo,
		FilePath:  cs.FilePath,
		Product:   cs.Product,
		OwnerTeam: cs.Owner,
	}

	seen := make(map[string]bool) // deduplicate by mfa_app_key
	signals := make(map[string]bool)

	// ── Signal 1: EventChainImpacts (highest fidelity — has explicit user_impact) ──
	for _, imp := range cs.EventChainImpacts {
		signals["event-chain"] = true
		for _, pa := range imp.ProductAreas {
			prod, mod := splitProductModule(pa.Product)
			if r.Product == "" || r.Product == UnknownProductLabel {
				r.Product = prod
				r.Module = mod
			}
			sev := deriveSeverityFromText(pa.UserImpact, cs.FilePath)
			silent := isWorkerImpact(imp.SubscriberService, pa.UserImpact)
			for _, key := range imp.MFAAppKeys {
				if seen[key] {
					continue
				}
				seen[key] = true
				r.AffectedSurfaces = append(r.AffectedSurfaces, AffectedSurface{
					MFAAppKey:   key,
					UserAction:  userActionFromProductArea(pa.Product),
					FailureMode: pa.UserImpact,
					Severity:    sev,
					Silent:      silent,
					Source:      "event-chain",
				})
				if silent {
					r.HasSilentFailure = true
				}
			}
		}
	}

	// ── Signal 2: RouteCallers ──
	for _, rc := range cs.RouteCallers {
		signals["route-callers"] = true
		sev := deriveSeverityFromPath(rc.PathPrefix, cs.FilePath)
		for _, caller := range rc.Callers {
			silent := isWorkerImpact("", caller.Notes)
			for _, key := range caller.MFAAppKeys {
				if seen[key] {
					continue
				}
				seen[key] = true
				r.AffectedSurfaces = append(r.AffectedSurfaces, AffectedSurface{
					MFAAppKey:   key,
					UserAction:  userActionFromNotes(caller.Notes),
					FailureMode: failureModeFromPath(rc.PathPrefix),
					Severity:    sev,
					Silent:      silent,
					Source:      "route-callers",
				})
				if silent {
					r.HasSilentFailure = true
				}
			}
		}
	}

	// ── Signal 3: SemanticProducts ──
	for _, sp := range cs.SemanticProducts {
		signals["semantic"] = true
		if r.Product == "" || r.Product == UnknownProductLabel {
			prod, mod := splitProductModule(sp.Domain)
			r.Product = prod
			r.Module = mod
			r.OwnerTeam = sp.Owner
		}
	}

	// ── Signal 4: ProductMap (lowest confidence) ──
	if r.Product == "" || r.Product == UnknownProductLabel {
		if cs.Product != "" && cs.Product != UnknownProductLabel {
			prod, mod := splitProductModule(cs.Product)
			r.Product = prod
			r.Module = mod
			r.OwnerTeam = cs.Owner
			signals["product-map"] = true
		}
	} else {
		if cs.Product != "" && cs.Product != UnknownProductLabel {
			signals["product-map"] = true
		}
	}

	// ── Compute MaxSeverity ──
	r.MaxSeverity = SeverityLow
	for _, s := range r.AffectedSurfaces {
		if compareSeverity(s.Severity, r.MaxSeverity) > 0 {
			r.MaxSeverity = s.Severity
		}
	}
	// Path-level override: checkout routes are always at least HIGH.
	if isCheckoutPath(cs.FilePath) && compareSeverity(r.MaxSeverity, SeverityHigh) < 0 {
		r.MaxSeverity = SeverityHigh
	}

	// ── Confidence scoring ──
	for sig := range signals {
		r.Signals = append(r.Signals, sig)
	}
	r.SignalCount = len(signals)
	r.Confidence = 0.4 + float64(r.SignalCount)*0.15
	if r.Confidence > 0.98 {
		r.Confidence = 0.98
	}

	// ── Blast radius ──
	r.BlastRadius = buildBlastRadius(cs, r.AffectedSurfaces)
	r.UserSegment = buildUserSegment(r.AffectedSurfaces)

	return r
}

// ── helpers ───────────────────────────────────────────────────────────────────

func splitProductModule(productArea string) (product, module string) {
	parts := strings.SplitN(productArea, " — ", 2)
	product = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		module = strings.TrimSpace(parts[1])
	}
	return
}

func deriveSeverityFromText(userImpact, filePath string) ImpactSeverity {
	lower := strings.ToLower(userImpact)
	if isCheckoutPath(filePath) {
		return SeverityCritical
	}
	for _, kw := range []string{"broken", "fail", "not granted", "not created", "cannot access"} {
		if strings.Contains(lower, kw) {
			return SeverityHigh
		}
	}
	for _, kw := range []string{"delayed", "not updated", "stale", "not sent", "not tracked", "not receive"} {
		if strings.Contains(lower, kw) {
			return SeverityMedium
		}
	}
	return SeverityLow
}

func deriveSeverityFromPath(pathPrefix, filePath string) ImpactSeverity {
	lower := strings.ToLower(pathPrefix + filePath)
	for _, kw := range []string{"checkout", "payment", "purchase", "access-grant"} {
		if strings.Contains(lower, kw) {
			return SeverityCritical
		}
	}
	return SeverityMedium
}

func isCheckoutPath(filePath string) bool {
	lower := strings.ToLower(filePath)
	for _, kw := range []string{"checkout", "payment", "purchase", "access-grant"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isWorkerImpact(subscriberService, notes string) bool {
	lower := strings.ToLower(subscriberService + " " + notes)
	for _, kw := range []string{"worker", "notification", "email", "drip", "webhook", "post-purchase", "analytics"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func userActionFromProductArea(productArea string) string {
	lower := strings.ToLower(productArea)
	switch {
	case strings.Contains(lower, "checkout"):
		return "Purchasing an offer"
	case strings.Contains(lower, "member portal"):
		return "Accessing the member portal"
	case strings.Contains(lower, "courses"):
		return "Accessing course content"
	case strings.Contains(lower, "communities"):
		return "Using the communities portal"
	default:
		return "Using the product"
	}
}

func userActionFromNotes(notes string) string {
	if notes == "" {
		return "Using the product"
	}
	// Truncate long notes to a user-action summary.
	parts := strings.SplitN(notes, " — ", 2)
	return strings.TrimSpace(parts[0])
}

func failureModeFromPath(pathPrefix string) string {
	lower := strings.ToLower(pathPrefix)
	switch {
	case strings.Contains(lower, "checkout"):
		return "Checkout flow broken — purchase cannot be completed"
	case strings.Contains(lower, "community"):
		return "Community feature broken"
	case strings.Contains(lower, "membership"):
		return "Membership feature broken"
	case strings.Contains(lower, "course"):
		return "Course access broken"
	default:
		return "API endpoint broken"
	}
}

func compareSeverity(a, b ImpactSeverity) int {
	order := map[ImpactSeverity]int{
		SeverityLow:      0,
		SeverityMedium:   1,
		SeverityHigh:     2,
		SeverityCritical: 3,
	}
	return order[a] - order[b]
}

func buildBlastRadius(cs CustomerSurface, surfaces []AffectedSurface) string {
	if len(surfaces) == 0 {
		return "Unknown blast radius — no signal matched"
	}
	var parts []string
	seen := make(map[string]bool)
	for _, s := range surfaces {
		action := s.UserAction
		if !seen[action] {
			seen[action] = true
			parts = append(parts, "users "+strings.ToLower(action))
		}
	}
	return "Affects " + strings.Join(parts, " and ")
}

func buildUserSegment(surfaces []AffectedSurface) string {
	apps := make(map[string]bool)
	for _, s := range surfaces {
		apps[s.MFAAppKey] = true
	}
	var keys []string
	for k := range apps {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, ", ")
}
