package enricher

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RouteCallerEntry describes one frontend caller for a given backend path prefix.
type RouteCallerEntry struct {
	Repo         string   `yaml:"repo" json:"repo"`
	MFAAppKeys   []string `yaml:"mfa_app_keys" json:"mfa_app_keys"`
	CallPatterns []string `yaml:"call_patterns" json:"call_patterns"`
	Notes        string   `yaml:"notes" json:"notes"`
}

// RouteCallersResult is the lookup result for one matched path prefix.
type RouteCallersResult struct {
	PathPrefix  string             `json:"path_prefix"`
	Description string             `json:"description"`
	Callers     []RouteCallerEntry `json:"callers"`
}

// routeCallersEntry is one entry in route_callers.yaml.
type routeCallersEntry struct {
	PathPrefix  string             `yaml:"path_prefix"`
	Description string             `yaml:"description"`
	Callers     []RouteCallerEntry `yaml:"callers"`
}

// routeCallersFile is the top-level structure of route_callers.yaml.
type routeCallersFile struct {
	Callers []routeCallersEntry `yaml:"callers"`
}

// RouteCallersRegistry maps backend API path prefixes to frontend callers.
// Build once via LoadDefaultRouteCallersRegistry or parseRouteCallersRegistry.
type RouteCallersRegistry struct {
	// entries sorted by prefix length descending for longest-prefix match.
	entries []routeCallersEntry
}

// LookupByRoute returns the RouteCallersResult for the longest prefix that
// matches route. Route should be an absolute path like "/community-checkout/checkout".
// Returns nil when no prefix matches.
func (r *RouteCallersRegistry) LookupByRoute(route string) *RouteCallersResult {
	if r == nil {
		return nil
	}
	for _, e := range r.entries {
		if strings.HasPrefix(route, e.PathPrefix) {
			return &RouteCallersResult{
				PathPrefix:  e.PathPrefix,
				Description: e.Description,
				Callers:     e.Callers,
			}
		}
	}
	return nil
}

// ResolveRouteCallers looks up callers for every route in routes by synthesising
// the full path from controllerPrefix + route.Path (mirroring NestJS
// @Controller('prefix') + @Get/Post/... ('path') conventions).
// Returns deduplicated results — one RouteCallersResult per matched prefix.
// Returns nil/empty when reg is nil or no routes match any prefix.
func ResolveRouteCallers(controllerPrefix string, routes []RouteInfo, reg *RouteCallersRegistry) []RouteCallersResult {
	if reg == nil || len(routes) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var results []RouteCallersResult
	for _, rt := range routes {
		fullPath := "/" + strings.Trim(controllerPrefix, "/") + "/" + strings.Trim(rt.Path, "/")
		res := reg.LookupByRoute(fullPath)
		if res == nil {
			continue
		}
		if seen[res.PathPrefix] {
			continue
		}
		seen[res.PathPrefix] = true
		results = append(results, *res)
	}
	return results
}

// LoadDefaultRouteCallersRegistry returns the route callers registry embedded
// in the binary at build time.
func LoadDefaultRouteCallersRegistry() (*RouteCallersRegistry, error) {
	return parseRouteCallersRegistry(defaultRouteCallersYAML)
}

// LoadRouteCallersRegistry reads and parses a YAML file at path.
func LoadRouteCallersRegistry(path string) (*RouteCallersRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("route_callers: read %q: %w", path, err)
	}
	return parseRouteCallersRegistry(data)
}

// parseRouteCallersRegistry unmarshals raw YAML bytes into an indexed RouteCallersRegistry.
func parseRouteCallersRegistry(data []byte) (*RouteCallersRegistry, error) {
	var raw routeCallersFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("route_callers: parse YAML: %w", err)
	}
	entries := raw.Callers
	// Sort longest prefix first so LookupByRoute always returns the most specific match.
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].PathPrefix) > len(entries[j].PathPrefix)
	})
	return &RouteCallersRegistry{entries: entries}, nil
}
