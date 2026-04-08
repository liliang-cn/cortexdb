package memoryflow

import (
	"context"
	"regexp"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// DefaultPromotionPolicy keeps high-signal categories and drops generic notes by default.
type DefaultPromotionPolicy struct{}

// Select filters promotion candidates using a deterministic allowlist.
func (DefaultPromotionPolicy) Select(_ context.Context, _ Transcript, _ SessionState, candidates []PromotionCandidate) ([]PromotionCandidate, error) {
	out := make([]PromotionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		kind := normalizePromotionKind(candidate)
		switch kind {
		case PromotionKindDecision, PromotionKindPreference, PromotionKindMilestone, PromotionKindProblem:
			candidate.Kind = kind
			out = append(out, candidate)
		}
	}
	return out, nil
}

// HeuristicExtractor extracts a small set of durable facts without an LLM.
type HeuristicExtractor struct{}

// Extract scans transcript text for simple preference/decision/milestone/problem signals.
func (HeuristicExtractor) Extract(_ context.Context, transcript Transcript, state SessionState) ([]PromotionCandidate, error) {
	out := make([]PromotionCandidate, 0)
	seen := make(map[string]struct{})

	for _, turn := range transcript.Turns {
		sentences := splitSentences(turn.Content)
		for _, sentence := range sentences {
			kind, ok := classifySentence(sentence)
			if !ok {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(sentence)) + "|" + string(kind)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			entities := extractEntities(sentence)
			metadata := map[string]string{"kind": string(kind)}
			if state.Taxonomy.Wing != "" {
				metadata["wing"] = state.Taxonomy.Wing
			}
			if state.Taxonomy.Room != "" {
				metadata["room"] = state.Taxonomy.Room
			}
			if transcript.Source != "" {
				metadata["source"] = transcript.Source
			}
			if turn.Role != "" {
				metadata["role"] = turn.Role
			}

			out = append(out, PromotionCandidate{
				Kind:     kind,
				Title:    compactTitle(sentence),
				Content:  sentence,
				Metadata: metadata,
				Entities: buildPromotionEntities(entities),
			})
		}
	}
	return out, nil
}

func normalizePromotionKind(candidate PromotionCandidate) PromotionKind {
	if candidate.Kind != "" {
		return candidate.Kind
	}
	if candidate.Metadata == nil {
		return PromotionKindNote
	}
	switch PromotionKind(strings.ToLower(strings.TrimSpace(candidate.Metadata["kind"]))) {
	case PromotionKindDecision:
		return PromotionKindDecision
	case PromotionKindPreference:
		return PromotionKindPreference
	case PromotionKindMilestone:
		return PromotionKindMilestone
	case PromotionKindProblem:
		return PromotionKindProblem
	default:
		return PromotionKindNote
	}
}

func joinTranscriptContent(transcript Transcript) string {
	parts := make([]string, 0, len(transcript.Turns))
	for _, turn := range transcript.Turns {
		if content := strings.TrimSpace(turn.Content); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func splitSentences(text string) []string {
	text = strings.ReplaceAll(text, "\n", " ")
	raw := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?'
	})
	out := make([]string, 0, len(raw))
	for _, sentence := range raw {
		sentence = strings.TrimSpace(sentence)
		if sentence != "" {
			out = append(out, sentence)
		}
	}
	return out
}

func compactTitle(text string) string {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) == 0 {
		return ""
	}
	if len(words) > 8 {
		words = words[:8]
	}
	return strings.Join(words, " ")
}

func classifySentence(sentence string) (PromotionKind, bool) {
	lower := strings.ToLower(strings.TrimSpace(sentence))
	switch {
	case strings.Contains(lower, "prefer"),
		strings.Contains(lower, "likes "),
		strings.Contains(lower, "dislikes "),
		strings.Contains(lower, "wants "),
		strings.Contains(lower, "prefers "):
		return PromotionKindPreference, true
	case strings.Contains(lower, "decided"),
		strings.Contains(lower, "decision"),
		strings.Contains(lower, "deadline"),
		strings.Contains(lower, "ship "),
		strings.Contains(lower, "launch "),
		strings.Contains(lower, "will "):
		return PromotionKindDecision, true
	case strings.Contains(lower, "shipped"),
		strings.Contains(lower, "launched"),
		strings.Contains(lower, "released"),
		strings.Contains(lower, "milestone"),
		strings.Contains(lower, "completed"):
		return PromotionKindMilestone, true
	case strings.Contains(lower, "bug"),
		strings.Contains(lower, "issue"),
		strings.Contains(lower, "problem"),
		strings.Contains(lower, "blocked"),
		strings.Contains(lower, "failed"),
		strings.Contains(lower, "error"):
		return PromotionKindProblem, true
	default:
		return PromotionKindNote, false
	}
}

var entityPattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_-]{1,}\b`)

func extractEntities(text string) []string {
	matches := entityPattern.FindAllString(text, -1)
	return uniqueStrings(matches)
}

func buildPromotionEntities(names []string) []cortexdb.ToolEntityInput {
	out := make([]cortexdb.ToolEntityInput, 0, len(names))
	for _, name := range names {
		out = append(out, cortexdb.ToolEntityInput{Name: name})
	}
	return out
}
