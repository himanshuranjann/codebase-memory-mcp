package enricher

import (
	"context"
	"strings"
)

// CallSearcher searches org-wide for controller patterns.
// Satisfied at runtime by *searchtools.OrgSearch; use a mock in tests.
type CallSearcher interface {
	SearchAll(ctx context.Context, pattern, fileGlob string) ([]CallSearchHit, error)
}

// CallSearchHit is one file-level match from a controller search.
type CallSearchHit struct {
	Repo     string
	FilePath string
	Line     int
	Text     string
}

// InternalCallImpact describes the customer impact path through a service-to-service call.
type InternalCallImpact struct {
	CalledService   string   // e.g. "OFFERS_SERVICE"
	CalledRoute     string   // e.g. "checkout"
	CallerRepo      string   // repo making the call (for context)
	OwnerTeam       string   // derived from service name: "offers"
	MFAAppKeys      []string // MFA apps served by the called service
	ControllerFiles []string // controller files found in the called service
}

// TraceInternalCallImpact resolves customer impact for internal service-to-service
// calls. For each InternalRequestCall it searches all repos for NestJS controller
// files that handle the target service's routes.
// Returns nil when calls is empty or searcher is nil.
func TraceInternalCallImpact(ctx context.Context, calls []InternalRequestCall, mfaReg *MFARegistry, searcher CallSearcher) ([]InternalCallImpact, error) {
	if searcher == nil || len(calls) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var results []InternalCallImpact
	for _, call := range calls {
		key := call.ServiceName + "|" + call.Route
		if seen[key] {
			continue
		}
		hint := serviceNameToRepoHint(call.ServiceName)
		// Search for controller files in the target service.
		pattern := `@Controller.*` + hint + `|Controller\('` + hint
		hits, err := searcher.SearchAll(ctx, pattern, "*.controller.ts")
		if err != nil || len(hits) == 0 {
			continue
		}
		seen[key] = true
		impact := InternalCallImpact{
			CalledService: call.ServiceName,
			CalledRoute:   call.Route,
			OwnerTeam:     hint,
		}
		repoSeen := make(map[string]bool)
		for _, hit := range hits {
			if !repoSeen[hit.Repo] {
				repoSeen[hit.Repo] = true
				impact.ControllerFiles = append(impact.ControllerFiles, hit.FilePath)
				if mfaReg != nil {
					for _, app := range mfaReg.LookupByRepo(hit.Repo) {
						impact.MFAAppKeys = append(impact.MFAAppKeys, app.Key)
					}
				}
			}
		}
		results = append(results, impact)
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}

// serviceNameToRepoHint converts SERVICE_NAME enum values to searchable repo hints.
// Examples: "OFFERS_SERVICE" → "offers", "COMMUNITY_CHECKOUT_ORCHESTRATOR" → "community-checkout-orchestrator"
func serviceNameToRepoHint(serviceName string) string {
	lower := strings.ToLower(serviceName)
	lower = strings.TrimSuffix(lower, "_service")
	lower = strings.TrimSuffix(lower, "_api")
	lower = strings.TrimSuffix(lower, "_orchestrator")
	lower = strings.TrimSuffix(lower, "_worker")
	return strings.ReplaceAll(lower, "_", "-")
}
