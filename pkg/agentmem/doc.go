// Package agentmem is a SQL-backed agent memory store.
//
// It mirrors the semantics of agent-go's pkg/store FileMemoryStore — typed
// memories (fact / skill / pattern / context / preference / observation),
// Hindsight fields (evidence / confidence / valid_from-to / superseded_by /
// conflicting / revisions / archived), bank configuration, mental models, and
// OpenClaw-style context slots — but persists everything in CortexDB's SQLite
// file rather than a tree of Markdown files.
//
// The package does NOT require an embedder. Search is based on FTS5 (the
// trigram tokenizer when available, unicode61 as fallback) re-ranked with
// importance and exponential time decay.
//
// # Quick Start
//
//	cdb, _ := cortexdb.Open(cortexdb.DefaultConfig("memory.db"))
//	defer cdb.Close()
//
//	store, _ := agentmem.New(cdb)
//
//	_ = store.Save(ctx, &agentmem.Memory{
//		Scope:      agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"},
//		Type:       agentmem.TypeFact,
//		Content:    "Apollo ships on Friday.",
//		Importance: 0.8,
//		Tags:       []string{"apollo", "deadline"},
//	})
//
//	hits, _ := store.SearchByText(ctx, "apollo deadline", agentmem.SearchOptions{TopK: 5})
//	for _, h := range hits { _ = h.Memory }
//
// # Reflection
//
// Reflect plugs in any Reflector implementation (LLM-backed or deterministic):
//
//	type myReflector struct{}
//	func (myReflector) Consolidate(ctx context.Context, facts, existing []*agentmem.Memory) ([]agentmem.Observation, error) {
//		// produce one or more observations citing fact ids as evidence
//	}
//
//	res, _ := store.Reflect(ctx, scope, myReflector{})
//
// # Context Slots
//
// Context slots replace agent-go's MEMORY.md/AGENTS.md/SOUL.md/TOOLS.md/HEARTBEAT.md
// fixed files. They live in agentmem_context_slots and can be assembled into a
// single string with BuildContextString.
package agentmem
