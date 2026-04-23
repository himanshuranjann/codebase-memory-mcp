// Package enricher — customer_surface.go
//
// Composite enricher that fuses ProductMap + Vue metadata + FE fetch calls
// into a single CustomerSurface record. This is the data shape the MCP
// composite tool (`codebase-memory_customer-surface`) returns to downstream
// customer-impact analyzers.
//
// Design:
//   - Pure computation, no I/O. Source and ProductMap are passed in.
//     MCP tool handlers do the I/O (SQLite lookups, file reads).
//   - Graceful degradation: a missing product mapping yields a labelled
//     "Unknown — no product mapping" surface rather than an error. Backend-
//     only files yield records with empty component fields. Empty source
//     yields a minimal record with just identity + product.
//   - Existing enricher output types (FetchCall, VueComponentMetadata,
//     ProductInfo) are reused verbatim — no new struct wrapping them.
//
// Callers (MCP tool handlers) iterate a list of (repo, file, source) tuples
// and collect the []CustomerSurface output. The customer-impact analyzer
// skill then renders the final PR-surface panel from this structured data.

package enricher

import (
	"strings"
)

// UnknownProductLabel is the sentinel used when no product mapping exists for
// a file. Rendered verbatim in user-facing output so the gap is visible
// (per the "show unknowns explicitly" design principle).
const UnknownProductLabel = "Unknown — no product mapping"

// BuildCustomerSurfaceArgs are the inputs to BuildCustomerSurface.
type BuildCustomerSurfaceArgs struct {
	// Repo is the short repo slug (e.g., "platform-backend", "ghl-crm-frontend").
	// Used for ProductMap lookup.
	Repo string
	// FilePath is the repo-root-relative file path (no leading slash).
	FilePath string
	// Source is the full file contents (may be empty for deleted files).
	Source string
	// ProductMap is the loaded product map. Nil is treated as empty → Unknown.
	ProductMap *ProductMap
	// MFARegistry is the loaded MFA app registry. Nil disables MFA enrichment.
	// When provided, SPMT lookup is done by repo; standalone/SSR lookup is done
	// by matching NestJS controller paths against backend_api_prefixes.
	MFARegistry *MFARegistry
}

// CustomerSurface is the fused per-file output.
type CustomerSurface struct {
	// Identity
	Repo     string
	FilePath string

	// Product area (from ProductMap lookup, or UnknownProductLabel)
	Product string
	Owner   string // empty when Product is Unknown

	// Vue component metadata (zero values for non-Vue files)
	ComponentName  string
	HasScriptSetup bool
	HasTemplate    bool
	ScriptLang     string // "ts" | "js" | "" (non-Vue)

	// User-facing strings (from Vue template i18n scan)
	I18nKeys []string

	// HTTP call sites (works on Vue, TSX, TS, JS)
	FetchCalls []FetchCall

	// NestJS controller routes (populated for *.controller.ts files)
	NestJSRoutes []RouteInfo

	// DTO contract fields (populated for *.dto.ts / *.dto.js files)
	DTOClasses []DTOMetadata

	// Event patterns this file produces or consumes (populated for backend TS)
	EventPatterns []EventPatternCall

	// MFA apps associated with this file — either by repo (for FE files) or
	// by backend API prefix match (for controller/service files).
	// Empty slice means no match; nil means MFARegistry was not provided.
	MFAApps []MFAAppRef
}

// BuildCustomerSurface fuses product-area lookup, Vue extraction, FE
// fetch-call extraction, NestJS metadata, DTO extraction, and MFA app
// association into a single record per file. Pure function —
// no file I/O, no network, deterministic given same inputs.
//
// Returns a record (never nil) even when inputs are degenerate (empty source,
// nil ProductMap, etc.). Errors are returned only for unrecoverable conditions;
// the current implementation has none — all partial results degrade
// gracefully.
func BuildCustomerSurface(args BuildCustomerSurfaceArgs) (CustomerSurface, error) {
	cs := CustomerSurface{
		Repo:     args.Repo,
		FilePath: args.FilePath,
	}

	// 1. Product area lookup (nil ProductMap is tolerated).
	if info, found := args.ProductMap.ProductForFile(args.Repo, args.FilePath); found {
		cs.Product = info.Product
		cs.Owner = info.Owner
	} else {
		cs.Product = UnknownProductLabel
		cs.Owner = ""
	}

	// 2. Vue component extraction — only for .vue files AND non-empty source.
	// ExtractVueComponent returns an error when the source has neither
	// <template> nor <script>; we treat that as "not a Vue file" and skip.
	if isVueFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		if meta, err := ExtractVueComponent(args.Source, args.FilePath); err == nil {
			cs.ComponentName = meta.ComponentName
			cs.HasScriptSetup = meta.HasScriptSetup
			cs.HasTemplate = meta.HasTemplate
			cs.ScriptLang = meta.ScriptLang
			cs.I18nKeys = meta.I18nKeys
		}
		// If ExtractVueComponent errored, the file has a .vue extension but
		// no SFC blocks — leave component fields zero-valued.
	}

	// 3. FE fetch-call extraction — works on any source with JS/TS patterns.
	// Empty source yields nil from the extractor; assign directly.
	cs.FetchCalls = ExtractFEFetchCalls(args.Source, args.FilePath)

	// 4. NestJS controller extraction — only for *.controller.ts files.
	if isControllerFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		if nestMeta, err := ExtractNestJSMetadata(args.Source, args.FilePath); err == nil {
			cs.NestJSRoutes = nestMeta.Routes
		}
	}

	// 5. DTO contract extraction — only for *.dto.ts files.
	if isDTOFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		cs.DTOClasses = ExtractDTOMetadata(args.Source, args.FilePath)
	}

	// 6. Event pattern extraction — for any backend TypeScript file.
	if isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		cs.EventPatterns = ExtractEventPatterns(args.Source, args.FilePath)
	}

	// 7. MFA app association (requires registry).
	if args.MFARegistry != nil {
		cs.MFAApps = resolveMFAApps(args.Repo, args.FilePath, cs.NestJSRoutes, cs.FetchCalls, args.MFARegistry)
	}

	return cs, nil
}

// resolveMFAApps builds the MFAAppRef slice for a given file by combining two
// lookup strategies:
//
//  1. Repo-based (for frontend files): all SPMT apps whose github_repo matches
//     the file's repo are included — they are directly impacted by any change.
//
//  2. Route-based (for backend controller files): the controller's route paths
//     are matched against backend_api_prefixes in standalone/SSR app entries.
//     This surfaces user-facing apps that call the changed backend route.
//
//  3. Fetch-call-based (for frontend files calling backend APIs): the extracted
//     URL patterns from FE fetch calls are matched against backend_api_prefixes
//     to surface the backend services' standalone/SSR consumer apps.
func resolveMFAApps(repo, _ string, routes []RouteInfo, fetchCalls []FetchCall, reg *MFARegistry) []MFAAppRef {
	seen := make(map[string]struct{})
	var refs []MFAAppRef

	addRef := func(entry MFAAppEntry, reason string) {
		if _, ok := seen[entry.Key]; ok {
			return
		}
		seen[entry.Key] = struct{}{}
		refs = append(refs, entry.ToRef(reason))
	}

	// Strategy 1: repo-based lookup for SPMT apps.
	for _, app := range reg.LookupByRepo(repo) {
		if app.Kind == MFAKindSPMT {
			addRef(app, "repo:"+repo)
		}
	}

	// Strategy 2: NestJS controller routes → standalone/SSR consumers.
	for _, route := range routes {
		// Build a candidate path: the route path itself is usually a sub-path;
		// look up by its prefix to find which user-facing apps call it.
		for _, app := range reg.LookupByAPIPrefix("/" + strings.TrimLeft(route.Path, "/")) {
			addRef(app, "nestjs-route:"+route.Path)
		}
	}

	// Strategy 3: FE fetch calls → standalone/SSR consumers of the same API.
	for _, fc := range fetchCalls {
		if !strings.HasPrefix(fc.URLPattern, "/") {
			continue
		}
		for _, app := range reg.LookupByAPIPrefix(fc.URLPattern) {
			addRef(app, "fetch-call:"+fc.URLPattern)
		}
	}

	return refs
}

// isVueFile returns true when the file path has a .vue extension (case-insensitive).
func isVueFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".vue")
}

// isControllerFile returns true for NestJS controller files.
func isControllerFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".controller.ts") || strings.HasSuffix(lower, ".controller.js")
}

// isDTOFile returns true for NestJS DTO files.
func isDTOFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".dto.ts") || strings.HasSuffix(lower, ".dto.js")
}

// isTypeScriptFile returns true for TypeScript/JavaScript source files.
func isTypeScriptFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") ||
		strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".jsx")
}
