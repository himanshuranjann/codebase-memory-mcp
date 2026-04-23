// Package enricher — mfa_registry.go
//
// Loads and queries the mfa_registry.yaml data file that maps GHL frontend
// apps to their production URLs, CDN slugs, and backend API prefixes.
//
// Three app kinds are supported:
//
//	spmt       — Agency/location admin apps loaded via Module Federation inside
//	             app.gohighlevel.com. Lookup by: github_repo, federation_key.
//	standalone — User-facing frontends on custom domains / CDN embeds.
//	             Lookup by: backend_api_prefixes (for bidirectional tracing).
//	ssr        — Nuxt 3 SSR apps on per-client domains (RevEx membership, etc).
//	             Same prefix-based lookup as standalone.
//
// Design:
//   - go:embed ships the YAML in the binary — zero runtime I/O in production.
//   - All lookups return copies (or empty slices) so callers cannot mutate
//     registry state.
//   - Missing coverage returns empty []MFAAppRef — callers surface the gap
//     explicitly, matching the "show unknowns" design principle used throughout
//     the enricher package.

package enricher

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// MFAAppKind discriminates the three frontend surface types.
type MFAAppKind string

const (
	MFAKindSPMT       MFAAppKind = "spmt"
	MFAKindStandalone MFAAppKind = "standalone"
	MFAKindSSR        MFAAppKind = "ssr"
)

// MFAAppEntry is one entry in mfa_registry.yaml. Fields are kind-specific:
// SPMT apps populate federation_key/cdn_slug/cdn_url_prod/route_prefixes/level;
// standalone and SSR apps populate deploy_target/url_pattern/backend_api_prefixes.
type MFAAppEntry struct {
	// Common fields (all kinds)
	Kind        MFAAppKind `yaml:"kind"`
	Key         string     `yaml:"key"`
	DisplayName string     `yaml:"display_name"`
	GithubRepo  string     `yaml:"github_repo"`
	ProductArea string     `yaml:"product_area"`
	Owner       string     `yaml:"owner"`
	UserType    string     `yaml:"user_type"`

	// SPMT-specific
	AppDir        string   `yaml:"app_dir"`
	FederationKey string   `yaml:"federation_key"`
	CDNSlug       string   `yaml:"cdn_slug"`
	CDNURLProd    string   `yaml:"cdn_url_prod"`
	RoutePrefixes []string `yaml:"route_prefixes"`
	Level         string   `yaml:"level"` // "location" | "agency"

	// Standalone / SSR specific
	DeployTarget      string   `yaml:"deploy_target"`
	URLPattern        string   `yaml:"url_pattern"`
	BackendAPIPrefixes []string `yaml:"backend_api_prefixes"`
	DeployNotes       string   `yaml:"deploy_notes"`
}

// MFAAppRef is the trimmed output shape included in CustomerSurface — enough
// for a reviewer to identify impact surface and open the right URL.
type MFAAppRef struct {
	Kind        MFAAppKind `json:"kind"`
	Key         string     `json:"key"`
	DisplayName string     `json:"display_name"`
	GithubRepo  string     `json:"github_repo"`
	ProductArea string     `json:"product_area"`
	Owner       string     `json:"owner"`
	UserType    string     `json:"user_type"`

	// SPMT: production remoteEntry URL
	CDNURLProd string `json:"cdn_url_prod,omitempty"`
	// SPMT: route prefix(es) visible in app.gohighlevel.com
	RoutePrefixes []string `json:"route_prefixes,omitempty"`
	// SPMT: admin level gate
	Level string `json:"level,omitempty"`

	// Standalone / SSR: the URL pattern end-users visit
	URLPattern string `json:"url_pattern,omitempty"`
	// Standalone / SSR: how the app is deployed
	DeployTarget string `json:"deploy_target,omitempty"`

	// How this ref was resolved (for transparency)
	MatchReason string `json:"match_reason"`
}

// mfaRegistryFile is the top-level shape of mfa_registry.yaml.
type mfaRegistryFile struct {
	Apps []MFAAppEntry `yaml:"apps"`
}

// MFARegistry is the in-memory registry. Build once via LoadDefaultMFARegistry
// or LoadMFARegistry and treat as read-only.
type MFARegistry struct {
	apps []MFAAppEntry

	// Lookup indices built on first load — O(1) hot path.
	byFederationKey map[string]*MFAAppEntry
	byRepo          map[string][]*MFAAppEntry
}

// buildIndices constructs the in-memory lookup maps from the flat apps slice.
func (r *MFARegistry) buildIndices() {
	r.byFederationKey = make(map[string]*MFAAppEntry, len(r.apps))
	r.byRepo = make(map[string][]*MFAAppEntry, len(r.apps))
	for i := range r.apps {
		app := &r.apps[i]
		if app.FederationKey != "" {
			r.byFederationKey[app.FederationKey] = app
		}
		if app.GithubRepo != "" {
			r.byRepo[app.GithubRepo] = append(r.byRepo[app.GithubRepo], app)
		}
	}
}

// LookupByRepo returns all MFA apps whose github_repo matches the given slug
// (e.g., "ghl-crm-frontend"). Returns an empty slice (never nil) when not found.
func (r *MFARegistry) LookupByRepo(repo string) []MFAAppEntry {
	if r == nil {
		return nil
	}
	ptrs := r.byRepo[repo]
	if len(ptrs) == 0 {
		return []MFAAppEntry{}
	}
	out := make([]MFAAppEntry, len(ptrs))
	for i, p := range ptrs {
		out[i] = *p
	}
	return out
}

// LookupByFederationKey returns the SPMT app entry for the given Module
// Federation remote key (e.g., "conversationsApp"). Returns ok=false when the
// key does not exist in the registry.
func (r *MFARegistry) LookupByFederationKey(key string) (MFAAppEntry, bool) {
	if r == nil {
		return MFAAppEntry{}, false
	}
	if p, ok := r.byFederationKey[key]; ok {
		return *p, true
	}
	return MFAAppEntry{}, false
}

// LookupByAPIPrefix returns all standalone/SSR apps whose backend_api_prefixes
// contain a prefix that is a prefix-of (or equal to) the given API path.
// Example: path="/funnels/pages/123" matches prefix="/funnels/".
// This enables bidirectional tracing: backend route change → user-facing app.
func (r *MFARegistry) LookupByAPIPrefix(apiPath string) []MFAAppEntry {
	if r == nil {
		return nil
	}
	var matches []MFAAppEntry
	for i := range r.apps {
		app := &r.apps[i]
		if app.Kind != MFAKindStandalone && app.Kind != MFAKindSSR {
			continue
		}
		for _, prefix := range app.BackendAPIPrefixes {
			if strings.HasPrefix(apiPath, prefix) {
				matches = append(matches, *app)
				break
			}
		}
	}
	return matches
}

// AllApps returns a copy of all registry entries. Intended for tooling and
// diagnostics; hot-path callers should use the indexed lookups above.
func (r *MFARegistry) AllApps() []MFAAppEntry {
	if r == nil {
		return nil
	}
	out := make([]MFAAppEntry, len(r.apps))
	copy(out, r.apps)
	return out
}

// ToRef converts an MFAAppEntry to the trimmed MFAAppRef output shape, setting
// the MatchReason so downstream renderers can explain why this app was included.
func (e MFAAppEntry) ToRef(matchReason string) MFAAppRef {
	return MFAAppRef{
		Kind:          e.Kind,
		Key:           e.Key,
		DisplayName:   e.DisplayName,
		GithubRepo:    e.GithubRepo,
		ProductArea:   e.ProductArea,
		Owner:         e.Owner,
		UserType:      e.UserType,
		CDNURLProd:    e.CDNURLProd,
		RoutePrefixes: e.RoutePrefixes,
		Level:         e.Level,
		URLPattern:    e.URLPattern,
		DeployTarget:  e.DeployTarget,
		MatchReason:   matchReason,
	}
}

// LoadMFARegistry reads a YAML file at `path` and returns the parsed registry.
func LoadMFARegistry(path string) (*MFARegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mfa_registry: read %q: %w", path, err)
	}
	return parseMFARegistry(data)
}

// parseMFARegistry unmarshals raw YAML bytes into an indexed MFARegistry.
func parseMFARegistry(data []byte) (*MFARegistry, error) {
	var raw mfaRegistryFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("mfa_registry: parse YAML: %w", err)
	}
	r := &MFARegistry{apps: raw.Apps}
	r.buildIndices()
	return r, nil
}
