// Package memoryflow provides a higher-level workflow facade on top of CortexDB's
// memory, knowledge, and brain primitives.
//
// The package is intended for applications that want "memory system" behavior
// rather than direct database primitives. It focuses on:
//   - transcript ingest as episodic memory exchanges
//   - deterministic taxonomy and project conventions
//   - layered wake-up context assembly
//   - session-close promotion into durable knowledge
//   - diary and reply-protocol helpers
//
// LLM-dependent behavior is exposed through interfaces:
//   - QueryPlanner
//   - SessionExtractor
//   - PromotionPolicy
//
// This keeps the package usable in fully deterministic mode while allowing
// applications to plug in planner and extraction logic when needed.
package memoryflow
