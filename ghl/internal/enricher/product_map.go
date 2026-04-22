// Package enricher — product_map.go
//
// ProductMap resolves a (repo, file_path) pair to a human-readable product
// area + owning team. Powered by a hand-maintained YAML file (data/product_map.yaml).
//
// Why hand-maintained: product-area assignments change on the order of quarters,
// not commits. Auto-derivation from directory names is brittle and gives poor
// human-readable labels ("apps/iam" is not a product name; "Platform — IAM" is).
// The maintenance cost is ~30 minutes per quarter; the output quality is
// dramatically higher.
//
// Lookup semantics: longest-prefix match within the same repo. Repos are
// isolated (mappings for "platform-backend" never apply to "ghl-revex-backend").
// Missing coverage returns found=false — callers should label the surface
// "Unknown — no product mapping" rather than fabricating one.

package enricher

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProductMapping is a single entry in the product map. Matches YAML schema
// in data/product_map.yaml.
type ProductMapping struct {
	Repo       string `yaml:"repo"`
	PathPrefix string `yaml:"path_prefix"`
	Product    string `yaml:"product"`
	Owner      string `yaml:"owner"`
}

// ProductMap is the in-memory representation of data/product_map.yaml.
type ProductMap struct {
	Mappings []ProductMapping `yaml:"mappings"`
}

// ProductInfo is the result of a successful lookup.
type ProductInfo struct {
	Product string
	Owner   string
}

// LoadProductMap reads a YAML file at `path` and returns the parsed map.
// Returns a descriptive error on missing file or malformed YAML.
func LoadProductMap(path string) (*ProductMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("product_map: read %q: %w", path, err)
	}
	var pm ProductMap
	if err := yaml.Unmarshal(data, &pm); err != nil {
		return nil, fmt.Errorf("product_map: parse %q: %w", path, err)
	}
	return &pm, nil
}

// ProductForFile returns the product/owner for a (repo, filePath) pair using
// longest-prefix match within the repo. Returns found=false if no mapping
// exists for the repo or no prefix matches the path.
//
// The file path is treated as a plain string starting from the repo root
// (no leading slash). We match with strings.HasPrefix so mappings like
// "apps/iam/" correctly capture "apps/iam/workers/foo.ts" but not "apps/iamx/foo.ts".
func (pm *ProductMap) ProductForFile(repo, filePath string) (ProductInfo, bool) {
	if pm == nil {
		return ProductInfo{}, false
	}

	// Collect candidate mappings scoped to this repo.
	var candidates []ProductMapping
	for _, m := range pm.Mappings {
		if m.Repo != repo {
			continue
		}
		if strings.HasPrefix(filePath, m.PathPrefix) {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return ProductInfo{}, false
	}

	// Longest-prefix wins. Stable sort so equal-length prefixes preserve YAML order.
	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i].PathPrefix) > len(candidates[j].PathPrefix)
	})
	winner := candidates[0]
	return ProductInfo{Product: winner.Product, Owner: winner.Owner}, true
}

// writeFileBytes is a tiny test helper wrapper around os.WriteFile. Lives in
// production code so tests in the same package can share it without exporting
// os-level APIs publicly. Used only by _test.go files; zero production callers.
func writeFileBytes(path string, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}
