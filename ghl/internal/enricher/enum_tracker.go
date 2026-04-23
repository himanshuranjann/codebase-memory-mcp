// Package enricher — enum_tracker.go
//
// Extracts enum-like definitions and enum-value references from TypeScript source.
// Closes a gap in CBM's FTS5 index where dot-notation enum references
// (e.g. CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS) are invisible
// to search-code despite being widely referenced — the FTS tokenizer splits on
// dots and doesn't connect compound references back to their member names.
//
// Three enum-like patterns are detected:
//   1. `enum Foo {A = 'a'}` — TypeScript native enum
//   2. `class X { static TOPICS = {A: 'a'} }` — class-static pseudo-enum
//   3. `export const Foo = {A: 'a'} as const` — const-object pseudo-enum
//
// References are dot-chains of 3+ parts where the last component is
// UPPER_SNAKE_CASE — filters out normal method/field access.

package enricher

import (
	"regexp"
	"strings"
)

// EnumMember is one named value inside an enum-like structure.
type EnumMember struct {
	Name  string // e.g. "CHECKOUT_INTEGRATIONS"
	Value string // literal value if parseable; empty for computed/symbolic
	Line  int
}

// EnumDefinition describes an enum or enum-like construct.
type EnumDefinition struct {
	Name      string // e.g. "CheckoutWorkerNames" or "TOPICS"
	Kind      string // "enum" | "class_static" | "const_object"
	FilePath  string
	Members   []EnumMember
	StartLine int
	EndLine   int
}

// EnumReference describes a usage site of an enum-like member, e.g.
// `CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS`.
type EnumReference struct {
	FullReference string   // the complete dotted reference as written
	MemberName    string   // the final component — typically UPPER_SNAKE_CASE
	ContainerPath []string // components before the member (may be 1-N elements)
	FilePath      string
	Line          int
	Context       string // the source line containing the reference
}

var (
	// Matches `enum Name {...}` including export + const modifiers.
	// Captures: 1=name, 2=body
	reTSEnum = regexp.MustCompile(`(?ms)(?:export\s+)?(?:const\s+)?enum\s+(\w+)\s*\{([^}]*)\}`)

	// Matches `static NAME = { ... }` inside a class body.
	// Captures: 1=name, 2=body
	reClassStatic = regexp.MustCompile(`(?ms)static\s+(\w+)\s*(?::\s*\w+\s*)?=\s*\{([^}]*)\}`)

	// Matches `export const Name = { ... } as const` OR plain `const Name = {...}`.
	// Captures: 1=name, 2=body
	reConstObject = regexp.MustCompile(`(?ms)(?:export\s+)?const\s+(\w+)\s*(?::\s*\w+\s*)?=\s*\{([^}]*?)\}\s*(?:as\s+const)?`)

	// Inside an enum/object body: `NAME = 'value'` OR `NAME: 'value'` OR bare `NAME,`.
	// Captures: 1=member name, 2=literal value (may be empty)
	reEnumMember = regexp.MustCompile(`(?m)(\w+)\s*[=:]\s*['"]([^'"]*)['"]|(?m)\b([A-Z][A-Z0-9_]{2,})\s*[,}\n]`)

	// Dot-chain reference: 2+ identifier segments separated by dots, last segment UPPER_SNAKE_CASE.
	// Example: `CheckoutOrchestratorConfig.TOPICS.CHECKOUT_INTEGRATIONS` (3-seg)
	//       or `CheckoutStepsName.CHECKOUT_PUBLISH_TO_INTEGRATIONS` (2-seg).
	// The UPPER_SNAKE_CASE requirement on the last segment filters out
	// normal field/method access like `this.service.method()` or `obj.field`.
	reEnumRef = regexp.MustCompile(`\b([A-Za-z_]\w*(?:\.\w+)*)\.([A-Z][A-Z0-9_]{2,})\b`)
)

// ExtractEnumDefinitions finds enum-like declarations in source.
// Returns nil when source is empty or no patterns match.
func ExtractEnumDefinitions(source, filePath string) []EnumDefinition {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	var defs []EnumDefinition
	seen := make(map[string]bool)

	// Kind 1: TypeScript enum
	for _, m := range reTSEnum.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		body := source[m[4]:m[5]]
		key := "enum|" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		defs = append(defs, EnumDefinition{
			Name:      name,
			Kind:      "enum",
			FilePath:  filePath,
			Members:   extractMembers(body),
			StartLine: lineAt(source, m[0]),
			EndLine:   lineAt(source, m[1]),
		})
	}

	// Kind 2: class static object — only if the NAME is UPPER_CASE (filters out
	// static methods and non-enum-like statics).
	for _, m := range reClassStatic.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		if !isUpperCaseIdentifier(name) {
			continue
		}
		body := source[m[4]:m[5]]
		key := "class_static|" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		defs = append(defs, EnumDefinition{
			Name:      name,
			Kind:      "class_static",
			FilePath:  filePath,
			Members:   extractMembers(body),
			StartLine: lineAt(source, m[0]),
			EndLine:   lineAt(source, m[1]),
		})
	}

	// Kind 3: const object-as-const — also only UPPER_CASE or PascalCase convention.
	for _, m := range reConstObject.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		// Must end with `as const` or the body must be all-UPPER_SNAKE members.
		matched := source[m[0]:m[1]]
		if !strings.Contains(matched, "as const") {
			// Allow if body has at least one UPPER_SNAKE member (heuristic)
			if !hasUpperSnakeMember(source[m[4]:m[5]]) {
				continue
			}
		}
		key := "const_object|" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		defs = append(defs, EnumDefinition{
			Name:      name,
			Kind:      "const_object",
			FilePath:  filePath,
			Members:   extractMembers(source[m[4]:m[5]]),
			StartLine: lineAt(source, m[0]),
			EndLine:   lineAt(source, m[1]),
		})
	}

	if len(defs) == 0 {
		return nil
	}
	return defs
}

// ExtractEnumReferences finds dot-chain references where the last component is
// UPPER_SNAKE_CASE. Each reference is reported once per source line.
// Returns nil when source is empty or no references are found.
func ExtractEnumReferences(source, filePath string) []EnumReference {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	var refs []EnumReference
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		for _, m := range reEnumRef.FindAllStringSubmatch(line, -1) {
			if len(m) < 3 {
				continue
			}
			full := m[0]
			container := m[1]
			member := m[2]
			// Enforce: member must be UPPER_SNAKE_CASE (already enforced by regex
			// — but re-validate to stay safe if regex changes).
			if !isUpperSnakeCase(member) {
				continue
			}
			containerPath := strings.Split(container, ".")
			refs = append(refs, EnumReference{
				FullReference: full,
				MemberName:    member,
				ContainerPath: containerPath,
				FilePath:      filePath,
				Line:          i + 1,
				Context:       strings.TrimSpace(line),
			})
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// ── helpers ─────────────────────────────────────────────────────────────────

func extractMembers(body string) []EnumMember {
	var members []EnumMember
	seen := make(map[string]bool)
	// Pattern 1: `NAME = 'value'` or `NAME: 'value'`
	reKV := regexp.MustCompile(`(\w+)\s*[=:]\s*['"]([^'"]*)['"]`)
	for _, m := range reKV.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if !isUpperCaseIdentifier(name) {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		members = append(members, EnumMember{Name: name, Value: m[2]})
	}
	// Pattern 2: bare UPPER_CASE entries (no value)
	reBare := regexp.MustCompile(`\b([A-Z][A-Z0-9_]{2,})\s*[,}\n]`)
	for _, m := range reBare.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		members = append(members, EnumMember{Name: name})
	}
	return members
}

func isUpperCaseIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return true
}

func isUpperSnakeCase(s string) bool {
	if len(s) < 2 {
		return false
	}
	hasLetter := false
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
			continue
		}
		if r == '_' || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return hasLetter
}

func hasUpperSnakeMember(body string) bool {
	re := regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\s*[=:]`)
	return re.MatchString(body)
}

func lineAt(source string, pos int) int {
	if pos > len(source) {
		pos = len(source)
	}
	return strings.Count(source[:pos], "\n") + 1
}
