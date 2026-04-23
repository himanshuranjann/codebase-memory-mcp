package enricher

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// defaultProductMapYAML ships with the binary. Single source of truth —
// changing data/product_map.yaml and rebuilding is the only supported way
// to refresh the map. No runtime reload, no filesystem dependency in prod.
//
//go:embed data/product_map.yaml
var defaultProductMapYAML []byte

// defaultMFARegistryYAML ships with the binary alongside product_map.yaml.
// Maps all GHL frontend apps (SPMT MFAs, standalone user-facing apps, SSR
// apps) to their CDN URLs, route prefixes, and backend API prefixes.
//
//go:embed data/mfa_registry.yaml
var defaultMFARegistryYAML []byte

// semanticTaxonomyData ships with the binary alongside other enricher taxonomies.
//
//go:embed data/semantic_taxonomy.yaml
var semanticTaxonomyData []byte

// defaultTopicRegistryYAML maps pub/sub topic identifiers to downstream customer impact.
//
//go:embed data/topic_registry.yaml
var defaultTopicRegistryYAML []byte

// defaultRouteCallersYAML maps backend API path prefixes to frontend repos and MFA app keys.
//
//go:embed data/route_callers.yaml
var defaultRouteCallersYAML []byte

// LoadDefaultProductMap returns the product map embedded into the binary at
// build time. Parsed once per process — callers should treat the returned
// pointer as read-only. Returns a descriptive error only if the embedded YAML
// is malformed, which would be a build-time bug, not a runtime condition.
func LoadDefaultProductMap() (*ProductMap, error) {
	var pm ProductMap
	if err := yaml.Unmarshal(defaultProductMapYAML, &pm); err != nil {
		return nil, fmt.Errorf("enricher: parse embedded product_map.yaml: %w", err)
	}
	return &pm, nil
}

// LoadDefaultMFARegistry returns the MFA registry embedded into the binary at
// build time. Returns a descriptive error only if the embedded YAML is
// malformed (a build-time bug). Callers should treat the returned pointer as
// read-only.
func LoadDefaultMFARegistry() (*MFARegistry, error) {
	r, err := parseMFARegistry(defaultMFARegistryYAML)
	if err != nil {
		return nil, fmt.Errorf("enricher: parse embedded mfa_registry.yaml: %w", err)
	}
	return r, nil
}
