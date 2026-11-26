package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// hookContextBudget bounds what one hook hands Claude Code as context.
//
// Claude Code does not truncate an oversized additionalContext: past about ten
// thousand characters it replaces the whole of it with a short preview, so the
// model receives none of what was recalled and nothing says so. A recall that
// grew one hit too long lost every hit. The block is therefore cut whole lines
// at a time, from the end, under a margin below that limit, and says how many
// it left out.
const hookContextBudget = 9000

// fitHookContext joins header, lines and footer, dropping lines from the end
// until the block fits the budget. When not one line fits, it returns "": a
// heading over nothing is not worth the context it takes.
func fitHookContext(header string, lines []string, footer string) string {
	return fitContext(header, lines, footer, hookContextBudget)
}

func fitContext(header string, lines []string, footer string, budget int) string {
	size := func(parts []string) int {
		n := 0
		for _, p := range parts {
			n += utf8.RuneCountInString(p) + 1
		}
		return n
	}
	kept := lines
	note := ""
	for len(kept) > 0 && size(append([]string{header, footer, note}, kept...)) > budget {
		kept = kept[:len(kept)-1]
		note = fmt.Sprintf("(%d more not shown here; ask CortexDB for them with knowledge_memory_recall.)", len(lines)-len(kept))
	}
	if len(kept) == 0 && len(lines) > 0 {
		return ""
	}
	parts := append([]string{header}, kept...)
	if note != "" {
		parts = append(parts, note)
	}
	if footer != "" {
		parts = append(parts, footer)
	}
	return strings.Join(parts, "\n")
}
