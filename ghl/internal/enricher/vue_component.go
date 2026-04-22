// Package enricher — vue_component.go
//
// Extracts Vue Single-File Component metadata for customer-surface mapping:
//   - component name (from `<script setup>` + filename, from defineComponent
//     name, from Options API `name:`, or filename-stem fallback)
//     - script language (ts/js)
//   - template presence (is this a renderable component at all)
//   - i18n keys used in the template (links component → user-visible strings)
//
// Design tradeoffs:
//   - Regex-based block extraction over full tree-sitter parsing. Vue SFC
//     indexing via tree-sitter requires a nested-grammar pass (see audit doc).
//     Regex is good enough for the 95% of SFCs in GHL frontends and ships today.
//   - No props/emits/slots extraction yet — deferred to when the C-core
//     adds Vue-aware extraction. Adding it regex-first produces too many
//     false positives in complex TypeScript type-system code.
//   - Component-name resolution order:
//       1. explicit `name: 'Foo'` inside defineComponent or Options API
//       2. filename stem, kebab-case converted to PascalCase
//     This matches how Vue itself resolves registration names at runtime.

package enricher

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// VueComponentMetadata is the extracted shape of one .vue file.
type VueComponentMetadata struct {
	// FilePath is echoed from the caller verbatim.
	FilePath string
	// ComponentName is the resolved identifier (see resolution order above).
	ComponentName string
	// HasScriptSetup is true when a `<script setup>` block is present.
	HasScriptSetup bool
	// HasTemplate is true when a non-empty `<template>` block is present.
	HasTemplate bool
	// ScriptLang is "ts" or "js". Defaults to "js" when no lang attr set.
	ScriptLang string
	// I18nKeys is the list of keys passed to t(...) or $t(...) calls found
	// inside the template block. Source-ordered, duplicates preserved.
	I18nKeys []string
}

var (
	// Block finders. Non-greedy to handle multiple blocks of the same kind
	// (rare but legal in Vue SFCs, e.g. multiple <style> with different scopes).
	reTemplateBlock = regexp.MustCompile(`(?s)<template(?:\s[^>]*)?>(.*?)</template>`)
	reScriptBlock   = regexp.MustCompile(`(?s)<script(\s[^>]*)?>(.*?)</script>`)

	// Name extractors (inside <script> body).
	reDefineComponentName = regexp.MustCompile(`defineComponent\s*\(\s*\{[\s\S]*?\bname\s*:\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)
	reExportDefaultName   = regexp.MustCompile(`export\s+default\s*\{[\s\S]*?\bname\s*:\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)

	// Template-level i18n key extractors. Matches t('key') and $t('key'),
	// both single and double quoted. We deliberately skip backticks — those
	// allow ${} interpolation which turns the key dynamic (not indexable).
	reI18nCall = regexp.MustCompile(`\$?t\s*\(\s*['"]([^'"]+)['"]\s*\)`)

	// Script-setup detector. Matches <script setup>, <script setup lang="ts">,
	// etc. Position and attribute order don't matter.
	reScriptSetupAttr = regexp.MustCompile(`\bsetup\b`)
	// Lang-attr extractor (on the <script> tag open).
	reScriptLangAttr = regexp.MustCompile(`\blang\s*=\s*['"]([^'"]+)['"]`)
)

// ExtractVueComponent parses the given Vue SFC source and returns structured
// metadata. Returns an error when the source clearly isn't a Vue SFC
// (no <template> AND no <script> block).
func ExtractVueComponent(source, filePath string) (VueComponentMetadata, error) {
	if strings.TrimSpace(source) == "" {
		return VueComponentMetadata{}, errors.New("vue_component: empty source")
	}

	meta := VueComponentMetadata{FilePath: filePath}

	// Template block.
	if m := reTemplateBlock.FindStringSubmatch(source); m != nil {
		body := strings.TrimSpace(m[1])
		if body != "" {
			meta.HasTemplate = true
			meta.I18nKeys = extractI18nKeys(body)
		}
	}

	// Script block — open tag + body. Captures: 1=attrs, 2=body.
	scriptOpen := ""
	scriptBody := ""
	if m := reScriptBlock.FindStringSubmatch(source); m != nil {
		scriptOpen = m[1]
		scriptBody = m[2]
	}

	if scriptOpen == "" && scriptBody == "" && !meta.HasTemplate {
		return VueComponentMetadata{}, fmt.Errorf("vue_component: %s has no <template> or <script> block — not a Vue SFC", filePath)
	}

	meta.HasScriptSetup = scriptOpen != "" && reScriptSetupAttr.MatchString(scriptOpen)

	// ScriptLang: default "js", override if the open tag specifies lang=.
	meta.ScriptLang = "js"
	if lm := reScriptLangAttr.FindStringSubmatch(scriptOpen); lm != nil {
		meta.ScriptLang = strings.ToLower(lm[1])
	}

	// Component name: explicit > filename fallback.
	meta.ComponentName = resolveComponentName(scriptBody, filePath)

	return meta, nil
}

// resolveComponentName tries defineComponent({name}), then Options API
// export default {name}, then the filename stem in PascalCase.
func resolveComponentName(scriptBody, filePath string) string {
	if m := reDefineComponentName.FindStringSubmatch(scriptBody); m != nil {
		return m[1]
	}
	if m := reExportDefaultName.FindStringSubmatch(scriptBody); m != nil {
		return m[1]
	}
	return pascalFromFilename(filePath)
}

// pascalFromFilename extracts the basename stem and converts kebab/snake to
// PascalCase. "user-permissions.vue" → "UserPermissions".
// "UserView.vue" is already PascalCase → unchanged.
func pascalFromFilename(filePath string) string {
	base := filepath.Base(filePath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return ""
	}
	// Split on common separators, then PascalCase each segment.
	segments := splitIdentSegments(stem)
	var b strings.Builder
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		r := []rune(seg)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// splitIdentSegments splits on '-', '_', '.'. A stem that's already
// PascalCase (no separators) returns as a single segment — preserving its
// case. ("UserView" → ["UserView"] → "UserView".)
func splitIdentSegments(s string) []string {
	var out []string
	cur := &strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// extractI18nKeys finds all t('...') and $t('...') calls in order. Duplicates
// are preserved because the same key can appear multiple times in a template
// (e.g., tooltip and aria-label) — downstream callers decide whether to dedupe.
func extractI18nKeys(templateBody string) []string {
	matches := reI18nCall.FindAllStringSubmatch(templateBody, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}
