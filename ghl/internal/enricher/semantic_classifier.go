package enricher

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// SemanticProduct is a semantic classification candidate derived from source
// code signals instead of repo/path prefixes.
type SemanticProduct struct {
	Domain     string
	Owner      string
	Confidence float64
	Evidence   []string
}

type semanticTaxonomy struct {
	Rules []semanticRule `yaml:"rules"`
}

type semanticRule struct {
	Domain   string               `yaml:"domain"`
	Owner    string               `yaml:"owner"`
	Weight   int                  `yaml:"weight"`
	Patterns semanticRulePatterns `yaml:"patterns"`
}

type semanticRulePatterns struct {
	ClassNameContains []string `yaml:"class_name_contains"`
	ImportContains    []string `yaml:"import_contains"`
	ControllerPrefix  []string `yaml:"controller_prefix"`
	TopicEnumContains []string `yaml:"topic_enum_contains"`
}

type semanticSignals struct {
	ClassNames      []string
	ControllerPaths []string
	ImportPaths     []string
	FileName        string
	RawSource       string
}

var (
	reSemanticClassName  = regexp.MustCompile(`export\s+class\s+(\w+)`)
	reSemanticController = regexp.MustCompile(`@Controller\(\s*['"]([\w\-/]+)['"]`)
	reSemanticImport     = regexp.MustCompile(`from\s+['"]([\./\w\-]+)['"]`)
	reNonAlphaNumeric    = regexp.MustCompile(`[^a-z0-9]+`)

	semanticTaxonomyOnce      sync.Once
	semanticTaxonomyDataCache *semanticTaxonomy
	semanticTaxonomyErr       error
)

// ClassifySemanticProducts ranks likely product domains for a source file using
// semantic code signals. Pure function: no I/O, deterministic for same inputs.
func ClassifySemanticProducts(source, filePath string) []SemanticProduct {
	taxonomy, err := loadSemanticTaxonomy()
	if err != nil {
		return nil
	}

	signals := extractSemanticSignals(source, filePath)
	var products []SemanticProduct

	for _, rule := range taxonomy.Rules {
		product, ok := classifySemanticRule(rule, signals)
		if ok {
			products = append(products, product)
		}
	}

	if len(products) == 0 {
		return nil
	}

	sort.SliceStable(products, func(i, j int) bool {
		return products[i].Confidence > products[j].Confidence
	})

	return products
}

func loadSemanticTaxonomy() (*semanticTaxonomy, error) {
	semanticTaxonomyOnce.Do(func() {
		var taxonomy semanticTaxonomy
		if err := yaml.Unmarshal(semanticTaxonomyData, &taxonomy); err != nil {
			semanticTaxonomyErr = fmt.Errorf("enricher: parse embedded semantic_taxonomy.yaml: %w", err)
			return
		}
		semanticTaxonomyDataCache = &taxonomy
	})

	return semanticTaxonomyDataCache, semanticTaxonomyErr
}

func extractSemanticSignals(source, filePath string) semanticSignals {
	return semanticSignals{
		ClassNames:      collectSubmatches(reSemanticClassName, source),
		ControllerPaths: collectSubmatches(reSemanticController, source),
		ImportPaths:     collectSubmatches(reSemanticImport, source),
		FileName:        fileNameWithoutExtension(filePath),
		RawSource:       source,
	}
}

func collectSubmatches(re *regexp.Regexp, source string) []string {
	matches := re.FindAllStringSubmatch(source, -1)
	if len(matches) == 0 {
		return nil
	}

	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, match[1])
		}
	}

	return values
}

func fileNameWithoutExtension(filePath string) string {
	base := path.Base(filePath)
	if base == "." || base == "/" {
		return ""
	}

	return strings.TrimSuffix(base, path.Ext(base))
}

func classifySemanticRule(rule semanticRule, signals semanticSignals) (SemanticProduct, bool) {
	definedPatternTypes := 0
	score := 0
	var evidence []string

	if len(rule.Patterns.ClassNameContains) > 0 {
		definedPatternTypes++
		if ev, ok := matchClassNameContains(rule.Patterns.ClassNameContains, signals); ok {
			score += rule.Weight
			evidence = append(evidence, ev)
		}
	}

	if len(rule.Patterns.ImportContains) > 0 {
		definedPatternTypes++
		if ev, ok := matchImportContains(rule.Patterns.ImportContains, signals); ok {
			score += rule.Weight
			evidence = append(evidence, ev)
		}
	}

	if len(rule.Patterns.ControllerPrefix) > 0 {
		definedPatternTypes++
		if ev, ok := matchControllerPrefix(rule.Patterns.ControllerPrefix, signals); ok {
			score += rule.Weight
			evidence = append(evidence, ev)
		}
	}

	if len(rule.Patterns.TopicEnumContains) > 0 {
		definedPatternTypes++
		if ev, ok := matchTopicEnumContains(rule.Patterns.TopicEnumContains, signals); ok {
			score += rule.Weight
			evidence = append(evidence, ev)
		}
	}

	if score == 0 || definedPatternTypes == 0 {
		return SemanticProduct{}, false
	}

	confidence := float64(score) / float64(rule.Weight*definedPatternTypes)
	if confidence > 1.0 {
		confidence = 1.0
	}

	return SemanticProduct{
		Domain:     rule.Domain,
		Owner:      rule.Owner,
		Confidence: confidence,
		Evidence:   evidence,
	}, true
}

func matchClassNameContains(patterns []string, signals semanticSignals) (string, bool) {
	fileName := normalizeAlphaNumeric(signals.FileName)
	for _, pattern := range patterns {
		normalizedPattern := normalizeAlphaNumeric(pattern)
		if normalizedPattern == "" {
			continue
		}

		for _, className := range signals.ClassNames {
			if strings.Contains(normalizeAlphaNumeric(className), normalizedPattern) {
				return fmt.Sprintf("class_name_contains %q matched class %q", pattern, className), true
			}
		}

		if fileName != "" && strings.Contains(fileName, normalizedPattern) {
			return fmt.Sprintf("class_name_contains %q matched file %q", pattern, signals.FileName), true
		}
	}

	return "", false
}

func matchImportContains(patterns []string, signals semanticSignals) (string, bool) {
	for _, pattern := range patterns {
		normalizedPattern := strings.ToLower(strings.TrimSpace(pattern))
		if normalizedPattern == "" {
			continue
		}

		for _, importPath := range signals.ImportPaths {
			if strings.Contains(strings.ToLower(importPath), normalizedPattern) {
				return fmt.Sprintf("import_contains %q matched import %q", pattern, importPath), true
			}
		}
	}

	return "", false
}

func matchControllerPrefix(patterns []string, signals semanticSignals) (string, bool) {
	for _, pattern := range patterns {
		normalizedPattern := normalizePathSignal(pattern)
		if normalizedPattern == "" {
			continue
		}

		for _, controllerPath := range signals.ControllerPaths {
			if controllerMatchesPattern(controllerPath, normalizedPattern) {
				return fmt.Sprintf("controller_prefix %q matched controller %q", pattern, controllerPath), true
			}
		}
	}

	return "", false
}

func matchTopicEnumContains(patterns []string, signals semanticSignals) (string, bool) {
	source := strings.ToLower(signals.RawSource)
	for _, pattern := range patterns {
		normalizedPattern := strings.ToLower(strings.TrimSpace(pattern))
		if normalizedPattern == "" {
			continue
		}

		if strings.Contains(source, normalizedPattern) {
			return fmt.Sprintf("topic_enum_contains %q matched source", pattern), true
		}
	}

	return "", false
}

func controllerMatchesPattern(controllerPath, pattern string) bool {
	normalizedController := normalizePathSignal(controllerPath)
	return normalizedController == pattern ||
		strings.HasPrefix(normalizedController, pattern+"/") ||
		strings.Contains(normalizedController, pattern)
}

func normalizeAlphaNumeric(value string) string {
	return reNonAlphaNumeric.ReplaceAllString(strings.ToLower(value), "")
}

func normalizePathSignal(value string) string {
	return strings.Trim(strings.ToLower(value), "/ ")
}
