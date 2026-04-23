package enricher

import (
	"context"
	"sort"
	"strings"
)

// OrgSearchHit is one file-level match returned by an org-wide code search.
type OrgSearchHit struct {
	Project  string // e.g. "data-fleet-cache-repos-ghl-revex-frontend"
	Repo     string // derived slug: "ghl-revex-frontend"
	FilePath string
	Line     int
	Text     string // the matching line content
}

// OrgSearcher searches code across ALL indexed projects in the fleet.
// Implemented at runtime by searchtools.OrgSearch.
type OrgSearcher interface {
	SearchAll(ctx context.Context, pattern, fileGlob string) ([]OrgSearchHit, error)
	ListProjects(ctx context.Context) ([]string, error)
}

// OrgEnricher performs org-wide dynamic discovery — replacing static YAML
// lookup with live code search across all indexed repos.
type OrgEnricher struct {
	searcher    OrgSearcher
	mfaRegistry *MFARegistry
}

// NewOrgEnricher constructs an OrgEnricher. Passing nil searcher is valid and
// causes all discovery methods to return nil.
func NewOrgEnricher(searcher OrgSearcher, mfaRegistry *MFARegistry) *OrgEnricher {
	return &OrgEnricher{searcher: searcher, mfaRegistry: mfaRegistry}
}

// DiscoverRouteCallers finds which frontend repos and MFA apps make HTTP calls
// to pathPrefix (e.g. "/community-checkout/"). Searches all frontend TypeScript,
// Vue, and JavaScript files for URL patterns containing the prefix.
// Returns nil when no callers are found or searcher is nil.
func (e *OrgEnricher) DiscoverRouteCallers(ctx context.Context, pathPrefix string) ([]RouteCallersResult, error) {
	if e == nil || e.searcher == nil {
		return nil, nil
	}
	// Strip trailing slash for search pattern; escape leading slash.
	trimmed := strings.TrimSuffix(strings.TrimPrefix(pathPrefix, "/"), "/")
	pattern := `\/` + trimmed
	hits, err := e.searcher.SearchAll(ctx, pattern, "*.{ts,vue,tsx,js,jsx}")
	if err != nil {
		return nil, err
	}

	// Group callers by repo — only frontend repos.
	type callerData struct {
		repo       string
		mfaKeys    []string
		files      []string
	}
	byRepo := make(map[string]*callerData)
	for _, hit := range hits {
		if !e.isFrontendRepo(hit.Repo) {
			continue
		}
		cd, ok := byRepo[hit.Repo]
		if !ok {
			cd = &callerData{repo: hit.Repo}
			byRepo[hit.Repo] = cd
			if e.mfaRegistry != nil {
				for _, app := range e.mfaRegistry.LookupByRepo(hit.Repo) {
					cd.mfaKeys = append(cd.mfaKeys, app.Key)
				}
			}
		}
		cd.files = append(cd.files, hit.FilePath)
	}

	if len(byRepo) == 0 {
		return nil, nil
	}

	// Deterministic repo ordering.
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	var callers []RouteCallerEntry
	for _, r := range repos {
		cd := byRepo[r]
		sort.Strings(cd.mfaKeys)
		callers = append(callers, RouteCallerEntry{
			Repo:         cd.repo,
			MFAAppKeys:   cd.mfaKeys,
			CallPatterns: dedupStrings(cd.files),
			Notes:        "Discovered via org-wide search",
		})
	}

	return []RouteCallersResult{{
		PathPrefix:  pathPrefix,
		Description: "Dynamically discovered callers",
		Callers:     callers,
	}}, nil
}

// DiscoverTopicImpact finds which services subscribe to any of topicIDs.
// Returns nil when no subscribers are found or searcher is nil.
func (e *OrgEnricher) DiscoverTopicImpact(ctx context.Context, topicIDs []string) ([]TopicImpact, error) {
	if e == nil || e.searcher == nil {
		return nil, nil
	}
	seenTopic := make(map[string]bool)
	var impacts []TopicImpact

	for _, topicID := range topicIDs {
		if seenTopic[topicID] {
			continue
		}
		seenTopic[topicID] = true
		hits, err := e.searcher.SearchAll(ctx, topicID, "*.{ts,go,js}")
		if err != nil {
			continue
		}
		reposSeen := make(map[string]bool)
		for _, hit := range hits {
			if isTestFile(hit.FilePath) {
				continue
			}
			if isProducerHit(hit.Text) {
				continue
			}
			if !isSubscriberHit(hit.Text) {
				continue
			}
			if reposSeen[hit.Repo] {
				continue
			}
			reposSeen[hit.Repo] = true

			var mfaKeys []string
			if e.mfaRegistry != nil {
				for _, app := range e.mfaRegistry.LookupByRepo(hit.Repo) {
					mfaKeys = append(mfaKeys, app.Key)
				}
			}
			impacts = append(impacts, TopicImpact{
				TopicID:           topicID,
				SubscriberService: deriveServiceName(hit.FilePath),
				SubscriberRepo:    hit.Repo,
				MFAAppKeys:        mfaKeys,
			})
		}
	}
	if len(impacts) == 0 {
		return nil, nil
	}
	return impacts, nil
}

// isFrontendRepo returns true when the repo is a user-facing frontend.
func (e *OrgEnricher) isFrontendRepo(repo string) bool {
	if strings.HasSuffix(repo, "-frontend") {
		return true
	}
	if e.mfaRegistry != nil && len(e.mfaRegistry.LookupByRepo(repo)) > 0 {
		return true
	}
	return false
}

// isProducerHit returns true when the match is in a publisher context.
func isProducerHit(text string) bool {
	patterns := []string{"pubSub.publish", "new PublisherStep", ".emit(", ".publish(", "PublisherStep("}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// isSubscriberHit returns true when the match is in a subscriber context.
func isSubscriberHit(text string) bool {
	patterns := []string{
		"@EventPattern", "EventPattern(", "@MessagePattern", "MessagePattern(",
		"pubSub.subscribe", ".subscribe(", "@Processor", "Consumer",
		"handler(", "listener(",
	}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// deriveServiceName extracts a service-name hint from a file path.
func deriveServiceName(filePath string) string {
	base := filePath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".ts")
	base = strings.TrimSuffix(base, ".go")
	base = strings.TrimSuffix(base, ".js")
	return base
}

func dedupStrings(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
