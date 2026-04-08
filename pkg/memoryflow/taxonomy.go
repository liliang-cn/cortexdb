package memoryflow

import "strings"

// SourceHint is the non-LLM input used to resolve a taxonomy from deterministic conventions.
type SourceHint struct {
	Project string   `json:"project,omitempty"`
	Path    string   `json:"path,omitempty"`
	Source  string   `json:"source,omitempty"`
	Title   string   `json:"title,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// ConventionRule applies deterministic taxonomy defaults when its conditions match.
type ConventionRule struct {
	MatchSource      string   `json:"match_source,omitempty"`
	PathContainsAny  []string `json:"path_contains_any,omitempty"`
	TitleContainsAny []string `json:"title_contains_any,omitempty"`
	Wing             string   `json:"wing,omitempty"`
	Room             string   `json:"room,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	AddTags          []string `json:"add_tags,omitempty"`
}

// ConventionSet is the project-level deterministic taxonomy policy.
type ConventionSet struct {
	Project     string           `json:"project,omitempty"`
	DefaultWing string           `json:"default_wing,omitempty"`
	DefaultRoom string           `json:"default_room,omitempty"`
	DefaultKind string           `json:"default_kind,omitempty"`
	Rules       []ConventionRule `json:"rules,omitempty"`
}

// DefaultConventionSet returns pragmatic defaults for a project-scoped memory workflow.
func DefaultConventionSet(project string) ConventionSet {
	project = strings.TrimSpace(project)
	return ConventionSet{
		Project:     project,
		DefaultWing: normalizeName(firstNonEmpty(project, "general")),
		DefaultRoom: "inbox",
		DefaultKind: "episode",
		Rules: []ConventionRule{
			{
				MatchSource: "slack",
				Wing:        "collaboration",
				Room:        "chat",
				AddTags:     []string{"slack"},
			},
			{
				MatchSource: "chatgpt",
				Wing:        "assistant",
				Room:        "chat",
				AddTags:     []string{"chat"},
			},
			{
				MatchSource: "claude",
				Wing:        "assistant",
				Room:        "chat",
				AddTags:     []string{"chat"},
			},
			{
				PathContainsAny: []string{"src/", "pkg/", "cmd/", "internal/"},
				Wing:            "code",
				Room:            "implementation",
				AddTags:         []string{"code"},
			},
			{
				PathContainsAny: []string{"docs/", "notes/", "spec/", "design/"},
				Wing:            "knowledge",
				Room:            "docs",
				AddTags:         []string{"docs"},
			},
		},
	}
}

// ResolveTaxonomy applies deterministic project conventions to a base taxonomy.
func ResolveTaxonomy(base Taxonomy, hint SourceHint, conventions ConventionSet) Taxonomy {
	resolved := base
	if resolved.Wing == "" {
		resolved.Wing = conventions.DefaultWing
	}
	if resolved.Room == "" {
		resolved.Room = conventions.DefaultRoom
	}
	if resolved.Kind == "" {
		resolved.Kind = conventions.DefaultKind
	}
	if resolved.Source == "" {
		resolved.Source = strings.TrimSpace(hint.Source)
	}

	for _, rule := range conventions.Rules {
		if !matchesConvention(rule, hint) {
			continue
		}
		if resolved.Wing == "" && rule.Wing != "" {
			resolved.Wing = rule.Wing
		}
		if resolved.Room == "" && rule.Room != "" {
			resolved.Room = rule.Room
		}
		if resolved.Kind == "" && rule.Kind != "" {
			resolved.Kind = rule.Kind
		}
		resolved.Tags = append(resolved.Tags, rule.AddTags...)
	}

	resolved.Wing = normalizeName(resolved.Wing)
	resolved.Room = normalizeName(resolved.Room)
	resolved.Source = strings.TrimSpace(resolved.Source)
	resolved.Tags = uniqueStrings(append(resolved.Tags, hint.Tags...))
	resolved.Entities = uniqueStrings(resolved.Entities)
	return resolved
}

func matchesConvention(rule ConventionRule, hint SourceHint) bool {
	if rule.MatchSource != "" && !strings.EqualFold(strings.TrimSpace(rule.MatchSource), strings.TrimSpace(hint.Source)) {
		return false
	}
	if len(rule.PathContainsAny) > 0 && !containsAnyFold(hint.Path, rule.PathContainsAny) {
		return false
	}
	if len(rule.TitleContainsAny) > 0 && !containsAnyFold(hint.Title, rule.TitleContainsAny) {
		return false
	}
	return rule.MatchSource != "" || len(rule.PathContainsAny) > 0 || len(rule.TitleContainsAny) > 0
}

func containsAnyFold(value string, terms []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, term := range terms {
		if term = strings.ToLower(strings.TrimSpace(term)); term != "" && strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, ":", "_")
	return value
}
