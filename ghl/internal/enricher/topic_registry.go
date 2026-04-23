// Package enricher — topic_registry.go
//
// Maps pub/sub topic identifiers (canonical IDs and aliases including
// TypeScript enum references) to their downstream customer impact.
//
// Design:
//   - go:embed ships topic_registry.yaml in the binary — zero runtime I/O.
//   - Lookup is case-insensitive and handles both full enum paths
//     (A.B.CHECKOUT_INTEGRATIONS) and last-segment-only (CHECKOUT_INTEGRATIONS).
//   - ExtractPublisherStepTopics extracts enum references from
//     `new PublisherStep(...)` calls — the dominant GHL pattern for
//     Pub/Sub publishing that bypasses pubSub.publish() literal strings.
//   - ResolveEventChainImpact is pure: no I/O, deterministic.

package enricher

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// TopicProductArea describes one product surface affected by a topic.
type TopicProductArea struct {
	Product    string `yaml:"product" json:"product"`
	Owner      string `yaml:"owner" json:"owner"`
	UserImpact string `yaml:"user_impact" json:"user_impact"`
}

// TopicImpact describes the full downstream customer impact of a pub/sub topic.
type TopicImpact struct {
	TopicID           string             `json:"topic_id"`
	SubscriberService string             `json:"subscriber_service"`
	SubscriberRepo    string             `json:"subscriber_repo"`
	ProductAreas      []TopicProductArea `json:"product_areas"`
	MFAAppKeys        []string           `json:"mfa_app_keys"`
}

// topicEntry is one entry in topic_registry.yaml.
type topicEntry struct {
	ID                string             `yaml:"id"`
	Aliases           []string           `yaml:"aliases"`
	Description       string             `yaml:"description"`
	SubscriberService string             `yaml:"subscriber_service"`
	SubscriberRepo    string             `yaml:"subscriber_repo"`
	ProductAreas      []TopicProductArea `yaml:"product_areas"`
	MFAAppKeys        []string           `yaml:"mfa_app_keys"`
}

// topicRegistryFile is the top-level structure of topic_registry.yaml.
type topicRegistryFile struct {
	Topics []topicEntry `yaml:"topics"`
}

// TopicRegistry maps topic identifiers and aliases to downstream impact.
// Build once via LoadDefaultTopicRegistry or parseTopicRegistry.
type TopicRegistry struct {
	// byKey maps lowercase lookup key → TopicImpact.
	// Keys include: canonical ID, all aliases, and last-segment of each.
	byKey map[string]*TopicImpact
}

// buildIndex populates byKey from parsed entries.
func (r *TopicRegistry) buildIndex(entries []topicEntry) {
	r.byKey = make(map[string]*TopicImpact, len(entries)*4)
	for _, e := range entries {
		impact := &TopicImpact{
			TopicID:           e.ID,
			SubscriberService: e.SubscriberService,
			SubscriberRepo:    e.SubscriberRepo,
			ProductAreas:      e.ProductAreas,
			MFAAppKeys:        e.MFAAppKeys,
		}
		// Index canonical ID and its last segment.
		r.index(e.ID, impact)
		// Index each alias and its last segment.
		for _, alias := range e.Aliases {
			r.index(alias, impact)
		}
	}
}

// index stores impact under key and its last dot-separated segment, both lowercased.
func (r *TopicRegistry) index(key string, impact *TopicImpact) {
	lower := strings.ToLower(key)
	r.byKey[lower] = impact
	// Also index last segment for enum paths like "A.B.CHECKOUT_INTEGRATIONS".
	if idx := strings.LastIndex(lower, "."); idx >= 0 {
		seg := lower[idx+1:]
		if _, exists := r.byKey[seg]; !exists {
			r.byKey[seg] = impact
		}
	}
}

// LookupByTopicID finds impact for a topic identifier — canonical ID, alias, or
// last segment of an enum path. Case-insensitive. Returns nil if not found.
func (r *TopicRegistry) LookupByTopicID(id string) *TopicImpact {
	if r == nil {
		return nil
	}
	lower := strings.ToLower(id)
	if imp, ok := r.byKey[lower]; ok {
		return imp
	}
	// Try last segment of a dot-path.
	if idx := strings.LastIndex(lower, "."); idx >= 0 {
		seg := lower[idx+1:]
		return r.byKey[seg]
	}
	return nil
}

// rePublisherStep matches: new PublisherStep(<name>, <topicExpr>, <eventExpr>)
// Captures the second argument (topic expression), which may be a dotted enum path.
var rePublisherStep = regexp.MustCompile(`new\s+PublisherStep\s*\(\s*[^,]+,\s*([\w.]+)`)

// ExtractPublisherStepTopics finds topic identifiers inside `new PublisherStep(...)`
// calls. Returns the raw identifier strings (enum paths or literal names).
// The caller should look each one up in TopicRegistry.LookupByTopicID which
// handles both full paths and last-segment matching.
func ExtractPublisherStepTopics(source string) []string {
	matches := rePublisherStep.FindAllStringSubmatch(source, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, m := range matches {
		raw := strings.TrimSpace(m[1])
		if raw == "" {
			continue
		}
		// Extract last segment for de-dup key (CHECKOUT_INTEGRATIONS from A.B.CHECKOUT_INTEGRATIONS).
		seg := raw
		if idx := strings.LastIndex(raw, "."); idx >= 0 {
			seg = raw[idx+1:]
		}
		if _, ok := seen[seg]; ok {
			continue
		}
		seen[seg] = struct{}{}
		out = append(out, seg)
	}
	return out
}

// ResolveEventChainImpact takes extracted EventPatternCall records and returns
// the downstream customer impact for any "producer" events matched in the registry.
// Consumer events are skipped — they indicate the current file is a downstream
// subscriber, not an originator of blast radius.
// Returns nil (not empty slice) when no producer events match known topics.
func ResolveEventChainImpact(events []EventPatternCall, reg *TopicRegistry) []TopicImpact {
	if reg == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var impacts []TopicImpact
	for _, ev := range events {
		if ev.Role != "producer" {
			continue
		}
		impact := reg.LookupByTopicID(ev.Topic)
		if impact == nil {
			continue
		}
		if _, ok := seen[impact.TopicID]; ok {
			continue
		}
		seen[impact.TopicID] = struct{}{}
		impacts = append(impacts, *impact)
	}
	return impacts
}

// LoadDefaultTopicRegistry returns the topic registry embedded in the binary.
func LoadDefaultTopicRegistry() (*TopicRegistry, error) {
	return parseTopicRegistry(defaultTopicRegistryYAML)
}

// LoadTopicRegistry reads and parses a YAML file at path.
func LoadTopicRegistry(path string) (*TopicRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("topic_registry: read %q: %w", path, err)
	}
	return parseTopicRegistry(data)
}

// parseTopicRegistry unmarshals raw YAML bytes into an indexed TopicRegistry.
func parseTopicRegistry(data []byte) (*TopicRegistry, error) {
	var raw topicRegistryFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("topic_registry: parse YAML: %w", err)
	}
	r := &TopicRegistry{}
	r.buildIndex(raw.Topics)
	return r, nil
}
