package enricher

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// DTOSearcher searches org-wide for DTO import patterns.
// Satisfied at runtime by *searchtools.OrgSearch; use a mock in tests.
type DTOSearcher interface {
	SearchAll(ctx context.Context, pattern, fileGlob string) ([]DTOSearchHit, error)
}

// DTOSearchHit is one file-level match from a DTO import search.
type DTOSearchHit struct {
	Repo     string
	FilePath string
	Line     int
	Text     string
}

// DTOConsumerResult describes a repo/file that imports a changed DTO.
type DTOConsumerResult struct {
	ClassName    string   // e.g. "CommunityCheckoutDto"
	ConsumerRepo string   // e.g. "ghl-revex-frontend"
	FilePath     string   // the importing file
	ImportLine   string   // the actual import statement text
	MFAAppKeys   []string // MFA apps associated with that repo
	Severity     string   // "BREAKING" or "ADDITIVE"
}

// TraceDTOConsumers finds all repos that import the given DTO class names.
// When a DTO changes (field removed, type changed), all consumers are impacted.
// Returns nil when dtos is empty or searcher is nil.
func TraceDTOConsumers(ctx context.Context, dtos []DTOMetadata, mfaReg *MFARegistry, searcher DTOSearcher) ([]DTOConsumerResult, error) {
	if searcher == nil || len(dtos) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var results []DTOConsumerResult
	for _, dto := range dtos {
		pattern := fmt.Sprintf(`import.*%s`, regexp.QuoteMeta(dto.ClassName))
		hits, err := searcher.SearchAll(ctx, pattern, "*.{ts,tsx,js,vue}")
		if err != nil {
			continue
		}
		for _, hit := range hits {
			if isTestFile(hit.FilePath) {
				continue
			}
			if hit.FilePath == dto.FilePath {
				continue
			}
			key := dto.ClassName + "|" + hit.Repo + "|" + hit.FilePath
			if seen[key] {
				continue
			}
			seen[key] = true
			var mfaKeys []string
			if mfaReg != nil {
				for _, app := range mfaReg.LookupByRepo(hit.Repo) {
					mfaKeys = append(mfaKeys, app.Key)
				}
			}
			results = append(results, DTOConsumerResult{
				ClassName:    dto.ClassName,
				ConsumerRepo: hit.Repo,
				FilePath:     hit.FilePath,
				ImportLine:   hit.Text,
				MFAAppKeys:   mfaKeys,
				Severity:     classifyDTOSeverity(dto.ClassName),
			})
		}
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}

// classifyDTOSeverity returns "BREAKING" for request/response contract types.
func classifyDTOSeverity(className string) string {
	lower := strings.ToLower(className)
	for _, suffix := range []string{"dto", "request", "response", "payload"} {
		if strings.HasSuffix(lower, suffix) {
			return "BREAKING"
		}
	}
	return "ADDITIVE"
}

// isTestFile returns true for spec/test/mock files that should be excluded.
func isTestFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, ".spec.") ||
		strings.Contains(lower, ".test.") ||
		strings.Contains(lower, "__mocks__") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".test.ts")
}
