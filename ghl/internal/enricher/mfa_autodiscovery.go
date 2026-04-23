// Package enricher — mfa_autodiscovery.go
//
// Dynamically discovers MFA app entries by scanning all indexed repos for
// Module Federation configs (module-federation.config.js/ts), Vite MFE configs,
// and Nuxt/Next config files. Eliminates the need for hand-maintained
// mfa_registry.yaml entries for new frontends.
//
// Discovery strategy:
//   1. `module-federation.config.(js|ts)` → SPMT app (federation_key = `name:` field)
//   2. `nuxt.config.(ts|js)` → SSR app (app key = repo slug)
//   3. `vite.config.(ts|js)` with federation plugin → SPMT app
//   4. `app.config.(ts|js)` or `vue.config.js` with remote/exposes → SPMT app
//
// The discovered entries are MERGED with the static embedded registry — static
// entries win on conflict (they have curated metadata like owner, product_area).

package enricher

import (
	"context"
	"regexp"
	"strings"
)

// MFADiscoveryResult is one dynamically-discovered MFA app entry.
type MFADiscoveryResult struct {
	Repo          string
	Kind          MFAAppKind
	FederationKey string // from `name:` in module-federation.config
	AppKey        string // derived app key (for standalone/ssr: repo slug)
	ConfigFile    string // path to the discovered config
	Evidence      string // the matching source line
}

var (
	reModuleFedName = regexp.MustCompile(`name\s*:\s*['"]([\w-]+)['"]`)
	reNuxtConfig    = regexp.MustCompile(`(?i)defineNuxtConfig|export\s+default\s+\{`)
)

// DiscoverMFAApps scans all indexed repos for Module Federation / Nuxt / Vite
// configs and returns dynamically-derived MFA app entries.
// Returns nil when searcher is nil or no configs are found.
func DiscoverMFAApps(ctx context.Context, searcher OrgSearcher) ([]MFADiscoveryResult, error) {
	if searcher == nil {
		return nil, nil
	}
	var results []MFADiscoveryResult
	seen := make(map[string]bool)

	// Strategy 1: module-federation.config.(js|ts) with `name:` field.
	hits, err := searcher.SearchAll(ctx, `name\s*:\s*['"]`, "*.{ts,js}")
	if err == nil {
		for _, hit := range hits {
			if !isModuleFedConfig(hit.FilePath) {
				continue
			}
			m := reModuleFedName.FindStringSubmatch(hit.Text)
			if m == nil {
				continue
			}
			key := hit.Repo + "|" + m[1]
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, MFADiscoveryResult{
				Repo:          hit.Repo,
				Kind:          MFAKindSPMT,
				FederationKey: m[1],
				AppKey:        m[1],
				ConfigFile:    hit.FilePath,
				Evidence:      hit.Text,
			})
		}
	}

	// Strategy 2: nuxt.config — one per repo max (SSR app).
	hits, err = searcher.SearchAll(ctx, `defineNuxtConfig|export\s+default\s+defineNuxtConfig`, "*.{ts,js}")
	if err == nil {
		perRepo := make(map[string]bool)
		for _, hit := range hits {
			if !strings.Contains(hit.FilePath, "nuxt.config") {
				continue
			}
			if perRepo[hit.Repo] {
				continue
			}
			perRepo[hit.Repo] = true
			key := hit.Repo + "|ssr|" + hit.Repo
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, MFADiscoveryResult{
				Repo:       hit.Repo,
				Kind:       MFAKindSSR,
				AppKey:     hit.Repo,
				ConfigFile: hit.FilePath,
				Evidence:   hit.Text,
			})
		}
	}

	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}

// isModuleFedConfig returns true for Module Federation config filenames.
func isModuleFedConfig(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "module-federation.config") ||
		strings.Contains(lower, "module-federation.ts") ||
		strings.Contains(lower, "module-federation.js")
}

// MergeDiscoveredIntoRegistry returns a NEW MFARegistry combining the static
// (embedded) entries with dynamically-discovered ones. Static entries win on
// key collision — they have curated metadata (owner, product_area) that the
// discovery layer can't infer from config files alone.
func MergeDiscoveredIntoRegistry(static *MFARegistry, discovered []MFADiscoveryResult) *MFARegistry {
	if static == nil && len(discovered) == 0 {
		return nil
	}
	merged := &MFARegistry{}
	if static != nil {
		merged.apps = append(merged.apps, static.apps...)
	}
	existingKeys := make(map[string]bool)
	for _, app := range merged.apps {
		existingKeys[app.Key] = true
	}
	for _, d := range discovered {
		if existingKeys[d.AppKey] {
			continue // static wins
		}
		existingKeys[d.AppKey] = true
		merged.apps = append(merged.apps, MFAAppEntry{
			Kind:          d.Kind,
			Key:           d.AppKey,
			DisplayName:   d.AppKey + " (auto-discovered)",
			GithubRepo:    d.Repo,
			FederationKey: d.FederationKey,
		})
	}
	merged.buildIndices()
	return merged
}
