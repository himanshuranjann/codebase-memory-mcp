// Package searchtools — customer_surface.go
//
// Go-native MCP tool that composes enricher.BuildCustomerSurface across a
// batch of files. Used by the PR-impact-analyzer workflow to fuse (product area
// + Vue metadata + FE fetch calls) into a single per-file "customer surface"
// record that the downstream reviewer skill renders as a human-readable panel.
//
// Design choices (to keep this narrow and reviewable):
//   - Pure compute: caller passes sources inline. No SQLite open, no root_path
//     resolution, no filesystem walk. PR tooling already has file contents from
//     `git show` / `gh pr diff`.
//   - Product map loaded once from embedded YAML (see enricher.LoadDefaultProductMap).
//     An opt-in ProductMapPath override exists for local dev / test scenarios.
//   - Graceful degradation: unknown repo → each surface labelled "Unknown —
//     no product mapping"; empty files slice → Count=0; per-file enricher
//     errors are unreachable today (enricher returns nil error by design).

package searchtools

import (
	"context"
	"errors"
	"fmt"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/enricher"
)

// OrgEnricherConfig controls optional org-wide dynamic enrichment.
type OrgEnricherConfig struct {
	// Enabled turns on cross-repo dynamic search via OrgSearch.
	Enabled bool `json:"enabled,omitempty"`
	// CacheDir is the directory containing per-project .db files.
	CacheDir string `json:"cache_dir,omitempty"`
}

// CustomerSurfaceArgs is the JSON input to the customer-surface tool.
type CustomerSurfaceArgs struct {
	// Repo is the short repo slug used for product-map lookup, e.g. "ghl-crm-frontend".
	Repo string `json:"repo"`
	// Files is the list of (path, source) pairs to analyze. Paths are repo-root
	// relative; sources are the full file contents (may be empty for deleted files).
	Files []CustomerSurfaceFile `json:"files"`
	// ProductMapPath is an optional filesystem override of the embedded product map.
	// When empty (the default), the binary-embedded YAML is used.
	ProductMapPath string `json:"product_map_path,omitempty"`
	// MFARegistryPath is an optional filesystem override of the embedded MFA registry.
	// When empty (the default), the binary-embedded mfa_registry.yaml is used.
	// Pass a path only in local dev / test scenarios.
	MFARegistryPath string `json:"mfa_registry_path,omitempty"`
	// OrgEnricher optionally enables org-wide dynamic discovery (route callers
	// + topic subscribers + DTO consumers + internal-call impacts + mongo readers)
	// using cross-repo SQLite .db search at CacheDir.
	OrgEnricher OrgEnricherConfig `json:"org_enricher,omitempty"`
}

// CustomerSurfaceFile is one file in a customer-surface batch request.
type CustomerSurfaceFile struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// CustomerSurfaceResult is the JSON output: one surface record per input file,
// in input order. The count is provided explicitly so downstream tooling can
// assert on it without re-computing len.
type CustomerSurfaceResult struct {
	Repo     string                      `json:"repo"`
	Count    int                         `json:"count"`
	Surfaces []enricher.CustomerSurface  `json:"surfaces"`
}

// HandleCustomerSurface fuses product-area, Vue metadata, FE fetch-call
// extraction, NestJS route metadata, DTO contract fields, and MFA app
// association across a batch of files.
func HandleCustomerSurface(ctx context.Context, args CustomerSurfaceArgs) (*CustomerSurfaceResult, error) {
	if args.Repo == "" {
		return nil, errors.New("customer-surface: repo is required")
	}

	pm, err := loadProductMap(args.ProductMapPath)
	if err != nil {
		return nil, fmt.Errorf("customer-surface: load product map: %w", err)
	}

	mfaReg, err := loadMFARegistry(args.MFARegistryPath)
	if err != nil {
		return nil, fmt.Errorf("customer-surface: load mfa registry: %w", err)
	}

	topicReg, err := enricher.LoadDefaultTopicRegistry()
	if err != nil {
		return nil, fmt.Errorf("customer-surface: load topic registry: %w", err)
	}

	routeCallersReg, err := enricher.LoadDefaultRouteCallersRegistry()
	if err != nil {
		return nil, fmt.Errorf("customer-surface: load route callers registry: %w", err)
	}

	var orgEnricher *enricher.OrgEnricher
	if args.OrgEnricher.Enabled && args.OrgEnricher.CacheDir != "" {
		orgSearch := NewOrgSearch(args.OrgEnricher.CacheDir)
		// Auto-discover MFA apps from indexed repos and merge into the static
		// registry. Failures are non-fatal — we just fall back to the static reg.
		if discovered, derr := enricher.DiscoverMFAApps(ctx, orgSearch); derr == nil && len(discovered) > 0 {
			mfaReg = enricher.MergeDiscoveredIntoRegistry(mfaReg, discovered)
		}
		orgEnricher = enricher.NewOrgEnricher(orgSearch, mfaReg)
	}

	out := &CustomerSurfaceResult{
		Repo:     args.Repo,
		Surfaces: make([]enricher.CustomerSurface, 0, len(args.Files)),
	}

	for _, f := range args.Files {
		// Honor caller cancellation mid-batch — a PR with hundreds of files
		// should not keep burning CPU after a client drop.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cs, err := enricher.BuildCustomerSurface(enricher.BuildCustomerSurfaceArgs{
			Repo:                 args.Repo,
			FilePath:             f.Path,
			Source:               f.Source,
			ProductMap:           pm,
			MFARegistry:          mfaReg,
			TopicRegistry:        topicReg,
			RouteCallersRegistry: routeCallersReg,
			OrgEnricher:          orgEnricher,
			Ctx:                  ctx,
		})
		if err != nil {
			// Enricher currently never returns an error; if it ever does in the
			// future (e.g. a hard parse failure), skip the file rather than
			// failing the whole batch — individual file failures should not
			// poison a PR-wide analysis.
			continue
		}
		out.Surfaces = append(out.Surfaces, cs)
	}
	out.Count = len(out.Surfaces)
	return out, nil
}

// loadProductMap returns the embedded default unless an override path is set.
func loadProductMap(overridePath string) (*enricher.ProductMap, error) {
	if overridePath != "" {
		return enricher.LoadProductMap(overridePath)
	}
	return enricher.LoadDefaultProductMap()
}

// loadMFARegistry returns the embedded default unless an override path is set.
func loadMFARegistry(overridePath string) (*enricher.MFARegistry, error) {
	if overridePath != "" {
		return enricher.LoadMFARegistry(overridePath)
	}
	return enricher.LoadDefaultMFARegistry()
}
