package memory

// reflect.go implements the Reflect operation – the third pillar of the retain → recall → reflect
// lifecycle, analogous to Hindsight's reflect endpoint.
//
// Reflect wraps a Recall result with the MemoryBank's BankConfig (Mission / Directives /
// Disposition) so the caller can construct a fully-contextualised LLM system prompt without
// any further knowledge of the memory internals.
//
// sqvect does not call an LLM itself; ReflectContext contains all the structured data needed
// for the caller to build their own prompt – keeping sqvect LLM-provider-agnostic.

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// ReflectContext – structured output ready for LLM prompt injection
// ---------------------------------------------------------------------------

// ReflectContext is the output of Reflect, fully self-contained for LLM prompt assembly.
type ReflectContext struct {
	// SystemPrompt is built from BankConfig.Mission + Directives and should be inserted
	// as the "system" message in the LLM conversation.
	SystemPrompt string `json:"system_prompt"`

	// DispositionHints is a human-readable description of active disposition traits,
	// suitable for appending to the system prompt or a separate instruction block.
	DispositionHints string `json:"disposition_hints,omitempty"`

	// MemoryBlock is a pre-formatted plain-text summary of recalled memories,
	// ready to inject into the user or system message as a "<MEMORY>" block.
	MemoryBlock string `json:"memory_block"`

	// Memory holds the raw structured recall result for callers that want deeper access.
	Memory *MemoryContext `json:"memory"`

	// Query is the original query text, passed through for convenience.
	Query string `json:"query"`
}

// ---------------------------------------------------------------------------
// Reflect – orchestrate recall and wrap with BankConfig
// ---------------------------------------------------------------------------

// Reflect performs a full four-channel Recall and wraps the result with the configured
// BankConfig (Mission, Directives, Disposition) to produce a ReflectContext ready for
// LLM prompt injection.
//
// The returned SystemPrompt and MemoryBlock can be inserted directly into your LLM request
// without further manipulation.
func (m *MemoryManager) Reflect(ctx context.Context, userID, sessionID string, queryVec []float32, queryText string) (*ReflectContext, error) {
	memCtx, err := m.Recall(ctx, userID, sessionID, queryVec, queryText)
	if err != nil {
		return nil, fmt.Errorf("reflect: recall failed: %w", err)
	}

	cfg := m.GetBankConfig()

	return &ReflectContext{
		SystemPrompt:     buildSystemPrompt(cfg),
		DispositionHints: buildDispositionHints(cfg),
		MemoryBlock:      buildMemoryBlock(memCtx),
		Memory:           memCtx,
		Query:            queryText,
	}, nil
}

// ---------------------------------------------------------------------------
// Prompt-building helpers
// ---------------------------------------------------------------------------

// buildSystemPrompt assembles a system prompt from BankConfig.Mission and Directives.
func buildSystemPrompt(cfg BankConfig) string {
	var sb strings.Builder

	if strings.TrimSpace(cfg.Mission) != "" {
		sb.WriteString("## Identity\n")
		sb.WriteString(strings.TrimSpace(cfg.Mission))
		sb.WriteString("\n\n")
	}

	if len(cfg.Directives) > 0 {
		sb.WriteString("## Rules (must never be violated)\n")
		for i, d := range cfg.Directives {
			if strings.TrimSpace(d) == "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(d)))
		}
	}

	return strings.TrimSpace(sb.String())
}

// buildDispositionHints produces a short prose description of active disposition traits.
func buildDispositionHints(cfg BankConfig) string {
	if len(cfg.Disposition) == 0 {
		return ""
	}

	traits := make([]string, 0, len(cfg.Disposition))
	for t := range cfg.Disposition {
		traits = append(traits, t)
	}
	sort.Strings(traits) // deterministic ordering

	var parts []string
	for _, trait := range traits {
		level := cfg.Disposition[trait]
		if level < 1 {
			continue
		}
		if desc := dispositionDesc(trait, level); desc != "" {
			parts = append(parts, desc)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "Disposition: " + strings.Join(parts, "; ") + "."
}

// dispositionDesc maps a trait + level to a human-readable instruction fragment.
func dispositionDesc(trait string, level float32) string {
	intensity := "mild"
	switch {
	case level >= 4.5:
		intensity = "very strong"
	case level >= 3.5:
		intensity = "strong"
	case level >= 2.5:
		intensity = "moderate"
	}
	return fmt.Sprintf("%s %s (%.0f/5)", intensity, trait, level)
}

// buildMemoryBlock formats the recalled MemoryContext as a plain-text <MEMORY> block
// ready for injection into an LLM prompt.
func buildMemoryBlock(mc *MemoryContext) string {
	if mc == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<MEMORY>\n")

	// Short-term history
	if len(mc.RecentHistory) > 0 {
		sb.WriteString("### Recent Conversation\n")
		for _, msg := range mc.RecentHistory {
			sb.WriteString(fmt.Sprintf("[%s]: %s\n", strings.ToUpper(msg.Role), msg.Content))
		}
		sb.WriteString("\n")
	}

	// Long-term ranked memories – grouped by layer priority
	if len(mc.RankedMemories) > 0 {
		layerOrder := []MemoryLayer{LayerMentalModel, LayerObservation, LayerWorldFact, LayerExperience}
		layerNames := map[MemoryLayer]string{
			LayerMentalModel: "Mental Models",
			LayerObservation: "Observations",
			LayerWorldFact:   "Facts",
			LayerExperience:  "Experiences",
		}

		groups := make(map[MemoryLayer][]*RecallResult, 4)
		for _, r := range mc.RankedMemories {
			groups[r.Layer] = append(groups[r.Layer], r)
		}

		for _, layer := range layerOrder {
			items := groups[layer]
			if len(items) == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("### %s\n", layerNames[layer]))
			for _, item := range items {
				sb.WriteString(fmt.Sprintf("- %s\n", item.Content))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("</MEMORY>")
	return sb.String()
}
