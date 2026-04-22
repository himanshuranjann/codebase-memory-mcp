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
}

// BuildCustomerSurface fuses product-area lookup, Vue extraction, and FE
// fetch-call extraction into a single record per file. Pure function —
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

	return cs, nil
}

// isVueFile returns true when the file path has a .vue extension (case-insensitive).
// Used to gate Vue component extraction; non-Vue files (.ts/.tsx/.js/.jsx)
// have no SFC block structure to parse.
func isVueFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".vue")
}
