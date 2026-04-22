// Package enricher — fe_fetch_calls.go
//
// Extracts HTTP call sites from frontend source code (Vue SFC, TSX, JSX, TS, JS).
// Covers the four dominant patterns in GHL frontends:
//   - axios:    axios.get/post/put/patch/delete(URL, ...)
//   - fetch:    fetch(URL, { method: ... })
//   - $fetch:   $fetch(URL, ...)           (Nuxt 3 helper)
//   - useFetch: useFetch(URL, ...)         (Nuxt / Vue Query composables)
//
// Output is a []FetchCall — the enriched layer feeds these into the
// customer-surface MCP tool so that a backend route change can be traced
// back to the specific frontend components that consume it.
//
// Design tradeoffs:
//   - Regex-based (not AST). Rationale: tree-sitter queries inside Vue SFC
//     nested grammars are currently limited (see audit); regex is robust enough
//     for the 95% of GHL patterns and ships today. When/if the C-core adds
//     nested-grammar extraction, we can replace this with AST-backed logic
//     behind the same public API.
//   - Comments are stripped BEFORE pattern matching. Without this, the
//     extractor lights up on example code in JSDoc / inline notes.
//   - Line numbers are computed against the ORIGINAL source (post-strip
//     positions are re-mapped). Test coverage: see fe_fetch_calls_test.go.
//   - False-positive mitigation: we require a URL-like first argument
//     starting with '/' or a template string containing '/'. This rejects
//     axios.get(someVariable) which can't be statically URL-resolved.

package enricher

import (
	"regexp"
	"strings"
)

// FetchCall is one extracted call site.
type FetchCall struct {
	// Style identifies the caller pattern: "axios" | "fetch" | "$fetch" | "useFetch".
	Style string
	// Method is the HTTP verb in uppercase. Defaults to "GET" when the
	// caller style doesn't encode it (fetch() / $fetch() without explicit method).
	Method string
	// URLPattern is the literal first-argument URL, with template-literal
	// ${expr} segments preserved and string-concatenation segments collapsed
	// to '+' separators. Not a strict URL — a pattern suitable for indexing.
	URLPattern string
	// FilePath is the relative path of the source file. Passed through
	// verbatim from the caller.
	FilePath string
	// Line is the 1-indexed line number in the original source.
	Line int
}

// Precompiled patterns. Each matches one call style; the Method comes from
// either the verb in the match (axios) or "GET" default + optional method field
// parsing (fetch / $fetch / useFetch).
var (
	reAxiosMethod = regexp.MustCompile(`\baxios\.(get|post|put|patch|delete)\s*\(\s*(['"` + "`" + `])([^'"` + "`" + `]+)['"` + "`" + `]`)
	reFetch       = regexp.MustCompile(`\bfetch\s*\(\s*(['"` + "`" + `])([^'"` + "`" + `]+)['"` + "`" + `]`)
	reDollarFetch = regexp.MustCompile(`\$fetch\s*\(\s*(['"` + "`" + `])([^'"` + "`" + `]+)['"` + "`" + `]`)
	reUseFetch    = regexp.MustCompile(`\buseFetch\s*\(\s*(['"` + "`" + `])([^'"` + "`" + `]+)['"` + "`" + `]`)

	// Comment strippers. Line comments: // ... to end of line.
	// Block comments: /* ... */ possibly multi-line.
	// We replace with spaces of equal length to preserve original line positions.
	reLineComment  = regexp.MustCompile(`//[^\n]*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// ExtractFEFetchCalls scans the source text and returns all detected fetch
// calls in source order. Returns nil (not an empty slice) when the input
// is empty or whitespace-only — this matches Go idiom for "no results
// distinguishable from empty."
//
// Thread-safe: regex objects are package-level and compiled once.
func ExtractFEFetchCalls(source, filePath string) []FetchCall {
	if strings.TrimSpace(source) == "" {
		return nil
	}

	// Strip comments in-place by replacing their bytes with spaces. This
	// keeps byte offsets (and therefore line numbers) stable relative to
	// the original source.
	stripped := stripComments(source)

	var calls []FetchCall

	// Collect matches from each pattern. Each emits a distinct Style.
	// The `fetch` regex alone can match inside `$fetch` (word boundary is true
	// between $ and f). Filter those out explicitly — $fetch matches are
	// attributed to the dedicated $fetch pattern below.
	fetchHits := findMatches(stripped, source, filePath, reFetch, "fetch", false)
	fetchHits = filterFetchNotPrecededByDollar(stripped, source, fetchHits)
	calls = append(calls, findMatches(stripped, source, filePath, reAxiosMethod, "axios", true)...)
	calls = append(calls, fetchHits...)
	calls = append(calls, findMatches(stripped, source, filePath, reDollarFetch, "$fetch", false)...)
	calls = append(calls, findMatches(stripped, source, filePath, reUseFetch, "useFetch", false)...)

	if len(calls) == 0 {
		return nil
	}

	// Sort by line ascending so callers see source order across the four
	// patterns. Slow insertion-sort is fine — call counts per file are small.
	sortByLine(calls)
	return calls
}

// findMatches runs a single regex against `stripped` and returns one FetchCall
// per hit. `methodInCapture=true` means the first capture group is the HTTP
// verb (axios case); otherwise Method defaults to "GET".
//
// stripped: comment-stripped source used for matching (positions unchanged
// relative to original).
// original: original source, used only to count line numbers.
func findMatches(stripped, original, filePath string, re *regexp.Regexp, style string, methodInCapture bool) []FetchCall {
	indexes := re.FindAllStringSubmatchIndex(stripped, -1)
	if len(indexes) == 0 {
		return nil
	}
	out := make([]FetchCall, 0, len(indexes))
	for _, idx := range indexes {
		// idx is a flat slice: [matchStart, matchEnd, g1Start, g1End, g2Start, g2End, ...]
		var method, url string
		if methodInCapture {
			// axios: g1=verb, g2=quote, g3=url
			method = strings.ToUpper(stripped[idx[2]:idx[3]])
			url = stripped[idx[6]:idx[7]]
		} else {
			// fetch/$fetch/useFetch: g1=quote, g2=url
			method = "GET"
			url = stripped[idx[4]:idx[5]]
		}
		out = append(out, FetchCall{
			Style:      style,
			Method:     method,
			URLPattern: url,
			FilePath:   filePath,
			Line:       lineOf(original, idx[0]),
		})
	}
	return out
}

// filterFetchNotPrecededByDollar removes any "fetch" call whose match position
// in `stripped` is immediately preceded by '$'. This guards against the
// `\bfetch` regex matching inside `$fetch(...)`, which Go's RE2 would otherwise
// accept (word boundary is true between '$' and 'f').
//
// We look up each hit's byte offset in `stripped` by finding the first
// occurrence of its URL substring at its known line. Cheaper and simpler:
// use the caller's `Line` to slice into the source and check the character
// just before "fetch" on that line. Since fetchHits were produced by
// reFetch against `stripped`, we can re-scan `stripped` per hit.
func filterFetchNotPrecededByDollar(stripped, original string, hits []FetchCall) []FetchCall {
	if len(hits) == 0 {
		return hits
	}
	// Use the original source (== stripped for our purposes since stripping
	// only replaces comment bytes with spaces) to look up the token's context.
	out := hits[:0]
	for _, h := range hits {
		// Find the "fetch(" token on this line (post-strip).
		lineStart := lineStartIndex(stripped, h.Line)
		if lineStart < 0 {
			out = append(out, h) // conservative: keep if we can't resolve
			continue
		}
		lineEnd := lineStart + strings.IndexByte(stripped[lineStart:], '\n')
		if lineEnd < lineStart {
			lineEnd = len(stripped)
		}
		// On this line, find "fetch(" — if its preceding char is '$', drop.
		rel := strings.Index(stripped[lineStart:lineEnd], "fetch(")
		if rel <= 0 {
			// At position 0 or not found — accept the hit (no '$' before).
			out = append(out, h)
			continue
		}
		prev := stripped[lineStart+rel-1]
		if prev == '$' {
			continue // drop — belongs to $fetch
		}
		out = append(out, h)
	}
	_ = original // reserved for future use (e.g., richer context checks)
	return out
}

// lineStartIndex returns the byte offset in `src` where `line` (1-indexed)
// begins, or -1 if `line` exceeds the line count.
func lineStartIndex(src string, line int) int {
	if line < 1 {
		return -1
	}
	if line == 1 {
		return 0
	}
	pos := 0
	remaining := line - 1
	for remaining > 0 {
		next := strings.IndexByte(src[pos:], '\n')
		if next < 0 {
			return -1
		}
		pos += next + 1
		remaining--
	}
	return pos
}

// lineOf returns the 1-indexed line number of byte offset `pos` in `src`.
// Scans linearly; fine for call counts per file (~tens at most).
func lineOf(src string, pos int) int {
	if pos < 0 {
		return 1
	}
	if pos > len(src) {
		pos = len(src)
	}
	return 1 + strings.Count(src[:pos], "\n")
}

// stripComments replaces the contents of // and /* */ comments with spaces,
// preserving byte offsets so line-number computations remain valid on the
// original source.
func stripComments(src string) string {
	// Order matters: block comments first (they can contain // safely),
	// then line comments.
	s := reBlockComment.ReplaceAllStringFunc(src, spaceOfSameLength)
	s = reLineComment.ReplaceAllStringFunc(s, spaceOfSameLength)
	return s
}

// spaceOfSameLength returns a run of spaces and newlines that matches the
// input byte-for-byte on length, preserving newlines so line numbers don't
// drift when a multi-line /* */ is stripped.
func spaceOfSameLength(in string) string {
	b := make([]byte, len(in))
	for i := 0; i < len(in); i++ {
		if in[i] == '\n' {
			b[i] = '\n'
		} else {
			b[i] = ' '
		}
	}
	return string(b)
}

// sortByLine performs a stable in-place insertion sort on the slice by Line.
// Stable so calls with the same Line preserve their within-regex discovery order.
func sortByLine(calls []FetchCall) {
	for i := 1; i < len(calls); i++ {
		j := i
		for j > 0 && calls[j-1].Line > calls[j].Line {
			calls[j-1], calls[j] = calls[j], calls[j-1]
			j--
		}
	}
}
