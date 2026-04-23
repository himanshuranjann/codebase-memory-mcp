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
	"context"
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
	// TopicRegistry maps pub/sub topic identifiers to downstream customer impact.
	// Nil disables event-chain impact enrichment.
	TopicRegistry *TopicRegistry
	// RouteCallersRegistry maps backend path prefixes to frontend callers/MFA keys.
	// Nil disables route-callers enrichment.
	RouteCallersRegistry *RouteCallersRegistry
	// OrgEnricher performs dynamic org-wide search for callers/subscribers.
	// Nil disables dynamic org-wide enrichment (static YAML only).
	OrgEnricher *OrgEnricher
	// Context for OrgEnricher searches. Required when OrgEnricher is set.
	Ctx context.Context
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

	// SemanticProducts classifies the file by what the code DOES (class names,
	// decorators, imports) rather than where it lives (file path). Populated for
	// any TypeScript file with non-empty source. Nil when source is empty.
	SemanticProducts []SemanticProduct

	// EventChainImpacts lists downstream customer impacts from pub/sub topics
	// published by this file. Derived from ExtractPublisherStepTopics +
	// TopicRegistry lookup. Nil when TopicRegistry is not provided.
	EventChainImpacts []TopicImpact

	// RouteCallers lists which frontend repos and MFA apps call the backend
	// routes exposed by this file. Derived from NestJS controller prefix +
	// routes + RouteCallersRegistry. Nil when RouteCallersRegistry is not provided.
	RouteCallers []RouteCallersResult

	// InternalCallImpacts lists downstream service-to-service call impacts
	// (e.g. this service calls OFFERS_SERVICE → offers team is impacted).
	InternalCallImpacts []InternalCallImpact

	// DTOConsumers lists repos that import the DTO classes defined in this file.
	DTOConsumers []DTOConsumerResult

	// MongoReaders lists repos/files that query the same MongoDB collections.
	MongoReaders []MongoReaderResult

	// ConsumerCascade describes downstream side effects if this file is a
	// consumer worker (emails, drips, webhooks, access grants).
	ConsumerCascade []ConsumerCascadeResult

	// EnumDefinitions are enum-like declarations defined in this file.
	// Covers TypeScript `enum`, class-static objects, and const-object-as-const
	// patterns. Enables cross-repo enum reference tracking that CBM's FTS5
	// index doesn't natively support (dot-notation references tokenize apart).
	EnumDefinitions []EnumDefinition

	// EnumReferences are dot-chain references like
	// `CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS` used in this
	// file. One entry per source line.
	EnumReferences []EnumReference

	// ImpactReport is the final structured customer-impact summary aggregating
	// all signals. Rendered by downstream tooling.
	ImpactReport CustomerImpactReport
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

	// 8. Semantic product classification — classifies by code semantics, not
	// file path. This catches files like community-checkout.controller.ts that
	// live under apps/courses/ but serve Communities, not Courses.
	if isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		cs.SemanticProducts = ClassifySemanticProducts(args.Source, args.FilePath)
	}

	// 9. Event-chain impact — resolves pub/sub topics published via
	// new PublisherStep(...) to downstream product areas + MFA app keys.
	if args.TopicRegistry != nil && isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		// Augment literal-string events with PublisherStep enum references.
		publisherTopics := ExtractPublisherStepTopics(args.Source)
		var allEvents []EventPatternCall
		allEvents = append(allEvents, cs.EventPatterns...)
		for _, tp := range publisherTopics {
			allEvents = append(allEvents, EventPatternCall{
				Topic:    tp,
				Role:     "producer",
				Symbol:   "",
				FilePath: args.FilePath,
			})
		}
		cs.EventChainImpacts = ResolveEventChainImpact(allEvents, args.TopicRegistry)
	}

	// 10. Route callers — which frontend repos/MFA apps call the NestJS routes
	// exposed by this controller. Uses controller prefix derived from the file
	// path and the extracted NestJS routes.
	if args.RouteCallersRegistry != nil && isControllerFile(args.FilePath) && len(cs.NestJSRoutes) > 0 {
		controllerPrefix := extractControllerPrefix(args.FilePath)
		cs.RouteCallers = ResolveRouteCallers(controllerPrefix, cs.NestJSRoutes, args.RouteCallersRegistry)
	}

	// 11. Org-wide dynamic enrichment — supplements static registries with
	// live code search across all indexed repos. Results are merged with the
	// static findings.
	if args.OrgEnricher != nil && args.Ctx != nil {
		// Dynamic route-callers discovery (for controller files).
		if isControllerFile(args.FilePath) && len(cs.NestJSRoutes) > 0 {
			prefix := "/" + strings.Trim(extractControllerPrefix(args.FilePath), "/") + "/"
			if dyn, err := args.OrgEnricher.DiscoverRouteCallers(args.Ctx, prefix); err == nil && dyn != nil {
				cs.RouteCallers = mergeRouteCallers(cs.RouteCallers, dyn)
			}
		}
		// Dynamic topic-impact discovery.
		if isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
			topics := ExtractPublisherStepTopics(args.Source)
			for _, ev := range cs.EventPatterns {
				if ev.Role == "producer" {
					topics = append(topics, ev.Topic)
				}
			}
			if len(topics) > 0 {
				if dyn, err := args.OrgEnricher.DiscoverTopicImpact(args.Ctx, topics); err == nil && dyn != nil {
					cs.EventChainImpacts = mergeTopicImpacts(cs.EventChainImpacts, dyn)
				}
			}
		}
	}

	// 12. InternalRequest chain tracing — what downstream services does this call.
	if args.OrgEnricher != nil && args.Ctx != nil && isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		if internalCalls, err := ExtractInternalRequests(args.Source); err == nil && len(internalCalls) > 0 {
			searcher := &orgSearcherAdapter{org: args.OrgEnricher}
			cs.InternalCallImpacts, _ = TraceInternalCallImpact(args.Ctx, internalCalls, args.MFARegistry, searcher)
		}
	}

	// 13. DTO consumer tracing — which repos import DTOs defined here.
	if args.OrgEnricher != nil && args.Ctx != nil && len(cs.DTOClasses) > 0 {
		searcher := &orgDTOAdapter{org: args.OrgEnricher}
		cs.DTOConsumers, _ = TraceDTOConsumers(args.Ctx, cs.DTOClasses, args.MFARegistry, searcher)
	}

	// 14. Mongo reader tracing — which repos read the same MongoDB collections.
	if args.OrgEnricher != nil && args.Ctx != nil && isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		models := ExtractMongoModels(args.Source, args.FilePath)
		if len(models) > 0 {
			searcher := &orgMongoAdapter{org: args.OrgEnricher}
			cs.MongoReaders, _ = TraceMongoReaders(args.Ctx, models, args.Repo, args.MFARegistry, searcher)
		}
	}

	// 15. Consumer cascade — downstream side effects if this file is a consumer.
	if isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" && len(cs.EventPatterns) > 0 {
		cs.ConsumerCascade = ResolveConsumerCascade(cs.EventPatterns, args.Source, args.TopicRegistry)
	}

	// 15a. Enum tracking — definitions + dot-chain references. Closes the
	// CBM FTS5 gap where `CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS`
	// isn't searchable as CHECKOUT_INTEGRATIONS.
	if isTypeScriptFile(args.FilePath) && strings.TrimSpace(args.Source) != "" {
		cs.EnumDefinitions = ExtractEnumDefinitions(args.Source, args.FilePath)
		cs.EnumReferences = ExtractEnumReferences(args.Source, args.FilePath)
	}

	// 16. Aggregate all signals into a structured ImpactReport.
	cs.ImpactReport = BuildImpactReport(cs)

	return cs, nil
}

// orgSearcherAdapter adapts *OrgEnricher's underlying searcher to the narrower
// tracer-specific search interfaces. This keeps each tracer decoupled from the
// full OrgSearcher interface.
type orgSearcherAdapter struct {
	org *OrgEnricher
}

func (a *orgSearcherAdapter) SearchAll(ctx context.Context, pattern, fileGlob string) ([]CallSearchHit, error) {
	hits, err := a.org.searcher.SearchAll(ctx, pattern, fileGlob)
	if err != nil {
		return nil, err
	}
	out := make([]CallSearchHit, len(hits))
	for i, h := range hits {
		out[i] = CallSearchHit{Repo: h.Repo, FilePath: h.FilePath, Line: h.Line, Text: h.Text}
	}
	return out, nil
}

// Duplicate SearchAll method signatures with different return types require
// separate adapter types because Go interfaces can't overload.
type orgDTOAdapter struct{ org *OrgEnricher }

func (a *orgDTOAdapter) SearchAll(ctx context.Context, pattern, fileGlob string) ([]DTOSearchHit, error) {
	hits, err := a.org.searcher.SearchAll(ctx, pattern, fileGlob)
	if err != nil {
		return nil, err
	}
	out := make([]DTOSearchHit, len(hits))
	for i, h := range hits {
		out[i] = DTOSearchHit{Repo: h.Repo, FilePath: h.FilePath, Line: h.Line, Text: h.Text}
	}
	return out, nil
}

type orgMongoAdapter struct{ org *OrgEnricher }

func (a *orgMongoAdapter) SearchAll(ctx context.Context, pattern, fileGlob string) ([]MongoSearchHit, error) {
	hits, err := a.org.searcher.SearchAll(ctx, pattern, fileGlob)
	if err != nil {
		return nil, err
	}
	out := make([]MongoSearchHit, len(hits))
	for i, h := range hits {
		out[i] = MongoSearchHit{Repo: h.Repo, FilePath: h.FilePath, Line: h.Line, Text: h.Text}
	}
	return out, nil
}

// mergeRouteCallers merges static-YAML results with dynamic-search results,
// deduplicating by (PathPrefix, Repo).
func mergeRouteCallers(staticR, dynamic []RouteCallersResult) []RouteCallersResult {
	seen := make(map[string]bool)
	var out []RouteCallersResult
	for _, group := range [][]RouteCallersResult{staticR, dynamic} {
		for _, r := range group {
			key := r.PathPrefix
			if seen[key] {
				// Merge callers into existing entry.
				for i := range out {
					if out[i].PathPrefix == key {
						out[i].Callers = mergeCallerEntries(out[i].Callers, r.Callers)
						break
					}
				}
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

func mergeCallerEntries(a, b []RouteCallerEntry) []RouteCallerEntry {
	seen := make(map[string]bool)
	for _, e := range a {
		seen[e.Repo] = true
	}
	for _, e := range b {
		if !seen[e.Repo] {
			seen[e.Repo] = true
			a = append(a, e)
		}
	}
	return a
}

func mergeTopicImpacts(staticI, dynamic []TopicImpact) []TopicImpact {
	seen := make(map[string]bool)
	var out []TopicImpact
	for _, t := range staticI {
		key := t.TopicID + "|" + t.SubscriberRepo
		seen[key] = true
		out = append(out, t)
	}
	for _, t := range dynamic {
		key := t.TopicID + "|" + t.SubscriberRepo
		if !seen[key] {
			seen[key] = true
			out = append(out, t)
		}
	}
	return out
}

// extractControllerPrefix derives the NestJS controller path prefix from a
// controller file path. It uses the filename stem (without .controller.ts).
// For example: "apps/courses/src/community-checkout/community-checkout.controller.ts"
// → "community-checkout". The @Controller decorator value may differ from the
// file name, but in GHL the convention is consistent and this serves as a fast
// heuristic for route-callers registry lookup.
func extractControllerPrefix(filePath string) string {
	parts := strings.Split(filePath, "/")
	name := parts[len(parts)-1]
	name = strings.TrimSuffix(name, ".controller.ts")
	name = strings.TrimSuffix(name, ".controller.js")
	return name
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
