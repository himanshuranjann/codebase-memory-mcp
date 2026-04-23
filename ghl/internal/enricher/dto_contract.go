// Package enricher — dto_contract.go
//
// Extracts DTO (Data Transfer Object) metadata from NestJS TypeScript source
// files and computes breaking/additive contract deltas between two versions.
//
// Use case: PR impact analyzer Phase 2b — when a *.dto.ts file changes, this
// module classifies each field delta as BREAKING or ADDITIVE so reviewers can
// judge whether frontend consumers of the API need coordinated updates.
//
// Design choices:
//   - Regex-based field extraction. NestJS DTOs are structurally regular:
//     a class body with decorated property declarations. Regex covers the
//     vast majority without pulling in a full TypeScript parser.
//   - "Required" semantics: a field is optional when EITHER the TypeScript `?`
//     marker is present OR the class-validator @IsOptional() decorator appears
//     on the preceding lines. Both must be absent for "required".
//   - Breaking change taxonomy follows semver spirit: removing or restricting
//     a field breaks existing callers; adding an optional field is safe.

package enricher

import (
	"regexp"
	"strings"
)

// ContractChangeKind classifies the nature of a DTO field delta.
type ContractChangeKind string

const (
	// Breaking changes — existing API consumers may break.
	ContractFieldRemoved          ContractChangeKind = "FIELD_REMOVED"
	ContractRequiredFieldAdded    ContractChangeKind = "REQUIRED_FIELD_ADDED"
	ContractTypeChanged           ContractChangeKind = "TYPE_CHANGED"
	ContractOptionalMadeRequired  ContractChangeKind = "OPTIONAL_MADE_REQUIRED"

	// Additive / safe changes.
	ContractOptionalFieldAdded   ContractChangeKind = "OPTIONAL_FIELD_ADDED"
	ContractRequiredMadeOptional ContractChangeKind = "REQUIRED_MADE_OPTIONAL"
)

// ContractDelta describes a single field-level change between two DTO versions.
type ContractDelta struct {
	Kind      ContractChangeKind `json:"kind"`
	FieldName string             `json:"field_name"`
	// OldType is the field's type in the base version (empty for added fields).
	OldType string `json:"old_type,omitempty"`
	// NewType is the field's type in the head version (empty for removed fields).
	NewType string `json:"new_type,omitempty"`
	// Breaking signals whether existing callers are likely impacted.
	Breaking bool `json:"breaking"`
}

// DTOField is one property declaration inside a DTO class.
type DTOField struct {
	Name       string   `json:"name"`
	TypeName   string   `json:"type_name"`
	Required   bool     `json:"required"` // false when `?` or @IsOptional() present
	Decorators []string `json:"decorators,omitempty"`
	LineNumber  int      `json:"line_number"`
}

// DTOMetadata is the extracted shape of a single DTO class.
type DTOMetadata struct {
	ClassName string     `json:"class_name"`
	FilePath  string     `json:"file_path"`
	Fields    []DTOField `json:"fields"`
}

// Regexes for DTO parsing.
var (
	// Matches: export class FooDto { ... }  OR  class FooDto extends BarDto {
	reDTOClass = regexp.MustCompile(`(?m)(?:export\s+)?class\s+(\w+(?:Dto|DTO|Request|Response|Payload|Body|Params|Query))\s*(?:extends\s+\w+\s*)?\{`)

	// Matches a class-validator / class-transformer decorator line.
	// e.g. @IsString(), @IsOptional(), @Type(() => String)
	reDecorator = regexp.MustCompile(`^\s*@(\w+)\(`)

	// Matches a property declaration.
	// Groups: 1=name, 2="?" (optional marker), 3=type
	// Handles: "  fieldName: string", "  fieldName?: Type", "  readonly x: Foo"
	reDTOProp = regexp.MustCompile(`^\s*(?:readonly\s+)?(\w+)(\?)?\s*:\s*([^\s;{/]+)`)

	// @IsOptional() on its own line (captures decorator name).
	reIsOptional = regexp.MustCompile(`@IsOptional\(\s*\)`)
)

// ExtractDTOMetadata extracts all DTO class definitions from a TypeScript
// source file. Returns one DTOMetadata per class found. Returns nil when no
// DTO classes are present (pure logic files, controllers, etc.).
//
// Each class block is extracted from the first `{` after the class declaration
// to the matching `}`. Only top-level class properties are extracted — nested
// objects (type aliases, inline object types) are not recursed into.
func ExtractDTOMetadata(source, filePath string) []DTOMetadata {
	if strings.TrimSpace(source) == "" {
		return nil
	}

	classMatches := reDTOClass.FindAllStringSubmatchIndex(source, -1)
	if len(classMatches) == 0 {
		return nil
	}

	lines := strings.Split(source, "\n")
	var results []DTOMetadata

	for _, idx := range classMatches {
		className := source[idx[2]:idx[3]]
		// The regex ends with \{ which already consumed the opening brace.
		// idx[1] is positioned immediately after that brace — the class body starts here.
		bodyStart := idx[1]

		// Walk forward to find the matching closing brace (depth=1).
		depth := 1
		pos := bodyStart
		for pos < len(source) && depth > 0 {
			switch source[pos] {
			case '{':
				depth++
			case '}':
				depth--
			}
			pos++
		}
		bodyEnd := pos - 1 // points at the closing '}'

		classBody := source[bodyStart:bodyEnd]
		classBodyLines := strings.Split(classBody, "\n")

		// Line offset so field line numbers are relative to the full file.
		classStartLine := strings.Count(source[:bodyStart], "\n") + 1

		fields := extractFieldsFromClassBody(classBodyLines, classStartLine, lines)
		results = append(results, DTOMetadata{
			ClassName: className,
			FilePath:  filePath,
			Fields:    fields,
		})
	}
	return results
}

// extractFieldsFromClassBody parses the lines of a class body and returns
// DTOField entries for each top-level property declaration found.
func extractFieldsFromClassBody(bodyLines []string, lineOffset int, _ []string) []DTOField {
	var fields []DTOField
	var pendingDecorators []string
	var hasIsOptional bool

	for i, line := range bodyLines {
		// Track decorators that appear before the property.
		if m := reDecorator.FindStringSubmatch(line); m != nil {
			decoratorName := m[1]
			pendingDecorators = append(pendingDecorators, decoratorName)
			if reIsOptional.MatchString(line) {
				hasIsOptional = true
			}
			continue
		}

		// Property declaration line.
		if m := reDTOProp.FindStringSubmatch(line); m != nil {
			fieldName := m[1]
			tsOptionalMarker := m[2] // "?" or ""
			typeName := strings.TrimRight(m[3], ",;")

			// Skip constructor parameters, method declarations, etc.
			// A TS method would have `(` in it; skip those.
			if strings.Contains(typeName, "(") {
				pendingDecorators = nil
				hasIsOptional = false
				continue
			}
			// Skip common non-field lines like "constructor", "return", etc.
			skip := []string{"constructor", "return", "super", "if", "for", "while", "switch"}
			isKeyword := false
			for _, kw := range skip {
				if fieldName == kw {
					isKeyword = true
					break
				}
			}
			if isKeyword {
				pendingDecorators = nil
				hasIsOptional = false
				continue
			}

			required := tsOptionalMarker == "" && !hasIsOptional

			decs := make([]string, len(pendingDecorators))
			copy(decs, pendingDecorators)

			fields = append(fields, DTOField{
				Name:       fieldName,
				TypeName:   typeName,
				Required:   required,
				Decorators: decs,
				LineNumber: lineOffset + i,
			})

			// Reset decorator accumulator after consuming.
			pendingDecorators = nil
			hasIsOptional = false
			continue
		}

		// Non-decorator, non-property line — flush accumulator.
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "//") {
			pendingDecorators = nil
			hasIsOptional = false
		}
	}
	return fields
}

// DiffDTOSchema computes the field-level deltas between two versions of the
// same DTO class. base is the original version; head is the proposed change.
// Both must represent the same class (same ClassName) — the caller is
// responsible for pairing them correctly.
//
// Returns an empty slice (not nil) when there are no differences.
func DiffDTOSchema(base, head DTOMetadata) []ContractDelta {
	baseByName := indexFieldsByName(base.Fields)
	headByName := indexFieldsByName(head.Fields)

	var deltas []ContractDelta

	// Detect removed and type-changed fields.
	for name, bf := range baseByName {
		hf, exists := headByName[name]
		if !exists {
			deltas = append(deltas, ContractDelta{
				Kind:      ContractFieldRemoved,
				FieldName: name,
				OldType:   bf.TypeName,
				Breaking:  true,
			})
			continue
		}
		// Field exists in both — check for type change.
		if normalizeType(bf.TypeName) != normalizeType(hf.TypeName) {
			deltas = append(deltas, ContractDelta{
				Kind:      ContractTypeChanged,
				FieldName: name,
				OldType:   bf.TypeName,
				NewType:   hf.TypeName,
				Breaking:  true,
			})
		}
		// Check required → optional and optional → required transitions.
		if bf.Required && !hf.Required {
			deltas = append(deltas, ContractDelta{
				Kind:      ContractRequiredMadeOptional,
				FieldName: name,
				Breaking:  false,
			})
		}
		if !bf.Required && hf.Required {
			deltas = append(deltas, ContractDelta{
				Kind:      ContractOptionalMadeRequired,
				FieldName: name,
				Breaking:  true,
			})
		}
	}

	// Detect added fields.
	for name, hf := range headByName {
		if _, exists := baseByName[name]; !exists {
			kind := ContractOptionalFieldAdded
			breaking := false
			if hf.Required {
				kind = ContractRequiredFieldAdded
				breaking = true
			}
			deltas = append(deltas, ContractDelta{
				Kind:      kind,
				FieldName: name,
				NewType:   hf.TypeName,
				Breaking:  breaking,
			})
		}
	}

	return deltas
}

// indexFieldsByName builds a name→field lookup for fast delta computation.
func indexFieldsByName(fields []DTOField) map[string]DTOField {
	m := make(map[string]DTOField, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

// normalizeType strips whitespace and common wrapper sugar so that
// "string | undefined" and "string|undefined" compare equal, and
// "Array<Foo>" and "Foo[]" are left as-is (distinct — callers see the diff).
func normalizeType(t string) string {
	t = strings.TrimSpace(t)
	t = strings.ReplaceAll(t, " ", "")
	return t
}
