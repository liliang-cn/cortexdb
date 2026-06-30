package graphflow

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// OrganizeOptions configures OrganizeFromBrain.
type OrganizeOptions struct {
	// IncludeMemories scans the agent-memory store (messages). Defaults to true
	// when both IncludeMemories and IncludeKnowledge are left false.
	IncludeMemories bool
	// IncludeKnowledge scans durable knowledge (documents).
	IncludeKnowledge bool
	// MaxDocuments caps the number of texts scanned (0 = no cap).
	MaxDocuments int
}

// OrganizeReport summarizes an organize pass.
type OrganizeReport struct {
	DocumentsScanned int `json:"documents_scanned"`
	EntityCount      int `json:"entity_count"`   // new entities written
	RelationCount    int `json:"relation_count"` // relations written
	CandidatesSeen   int `json:"candidates_seen"`
	CandidatesKept   int `json:"candidates_kept"`
}

// OrganizeFromBrain extracts entities and relations from stored memories (and,
// optionally, durable knowledge) and writes them into the knowledge graph, so a
// brain that only holds free-text memories gains a navigable entity graph.
//
// Extraction is deterministic (no LLM or embedder). Raw capitalized/backtick
// candidates are filtered for quality — common English words are dropped, and a
// single-occurrence candidate is kept only if it looks like a real entity
// (domain, path, code identifier, CamelCase, or contains a digit) or recurs
// across texts. Entities are written through the public GraphRAG upsert path so
// they get "entity:<name>" ids and lexical vectors matching SaveKnowledge, and
// EXISTING entities are never re-written (so their richer type is preserved).
// Co-occurrence ("co_occurs") relations link entities sharing a sentence.
// Idempotent. For typed relations, save them explicitly or use an LLM extractor.
func OrganizeFromBrain(ctx context.Context, db *cortexdb.DB, opts OrganizeOptions) (*OrganizeReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: organize: nil db")
	}
	includeMem, includeKnow := opts.IncludeMemories, opts.IncludeKnowledge
	if !includeMem && !includeKnow {
		includeMem = true
	}

	texts := make([]string, 0)
	sqlDB := db.SQL()
	collect := func(query string, cols int) error {
		rows, err := sqlDB.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("graphflow: organize scan: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if cols == 2 {
				var title, content string
				if err := rows.Scan(&title, &content); err != nil {
					return err
				}
				texts = append(texts, strings.TrimSpace(title+". "+content))
			} else {
				var content string
				if err := rows.Scan(&content); err != nil {
					return err
				}
				texts = append(texts, content)
			}
		}
		return rows.Err()
	}
	if includeMem {
		if err := collect(`SELECT content FROM messages WHERE TRIM(COALESCE(content,'')) != ''`, 1); err != nil {
			return nil, err
		}
	}
	if includeKnow {
		if err := collect(`SELECT COALESCE(title,''), COALESCE(content,'') FROM documents WHERE TRIM(COALESCE(content,'')) != ''`, 2); err != nil {
			return nil, err
		}
	}
	if opts.MaxDocuments > 0 && len(texts) > opts.MaxDocuments {
		texts = texts[:opts.MaxDocuments]
	}

	report := &OrganizeReport{DocumentsScanned: len(texts)}

	// Pass 1: candidate frequency (distinct texts) + display form + co-occurrence pairs.
	freq := make(map[string]int)
	display := make(map[string]string)
	type pair struct{ a, b string }
	pairs := make([]pair, 0)
	for _, text := range texts {
		seenInText := make(map[string]struct{})
		for _, name := range extractEntities(text) {
			l := strings.ToLower(name)
			if _, ok := display[l]; !ok {
				display[l] = name
			}
			if _, ok := seenInText[l]; !ok {
				freq[l]++
				seenInText[l] = struct{}{}
			}
		}
		for _, sentence := range splitSentences(text) {
			ents := extractEntities(sentence)
			for i := 0; i+1 < len(ents); i++ {
				a, b := strings.ToLower(ents[i]), strings.ToLower(ents[i+1])
				if a != b {
					pairs = append(pairs, pair{a, b})
				}
			}
		}
	}
	report.CandidatesSeen = len(freq)

	// Existing entity names (lowercased) — keep for relations, but never re-upsert.
	existing := loadExistingEntityNames(ctx, sqlDB)

	// Decide which candidates are meaningful entities.
	kept := make(map[string]struct{})
	for l := range freq {
		if _, ok := existing[l]; ok {
			kept[l] = struct{}{}
			continue
		}
		if keepEntityCandidate(display[l], freq[l]) {
			kept[l] = struct{}{}
		}
	}
	report.CandidatesKept = len(kept)

	// New entities = kept and not already in the graph.
	entities := make([]cortexdb.ToolEntityInput, 0)
	for l := range kept {
		if _, ok := existing[l]; ok {
			continue
		}
		entities = append(entities, cortexdb.ToolEntityInput{Name: display[l], Type: "entity"})
	}

	// Relations between kept entities only; dedupe.
	relSeen := make(map[string]struct{})
	relations := make([]cortexdb.ToolRelationInput, 0)
	for _, p := range pairs {
		if _, ok := kept[p.a]; !ok {
			continue
		}
		if _, ok := kept[p.b]; !ok {
			continue
		}
		key := p.a + "\x00" + p.b
		if _, ok := relSeen[key]; ok {
			continue
		}
		relSeen[key] = struct{}{}
		relations = append(relations, cortexdb.ToolRelationInput{From: display[p.a], To: display[p.b], Type: "co_occurs"})
	}

	tools := db.GraphRAGTools()
	if len(entities) > 0 {
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
			return nil, fmt.Errorf("graphflow: organize upsert entities: %w", err)
		}
		report.EntityCount = len(entities)
	}
	if len(relations) > 0 {
		if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations}); err != nil {
			return nil, fmt.Errorf("graphflow: organize upsert relations: %w", err)
		}
		report.RelationCount = len(relations)
	}
	return report, nil
}

func loadExistingEntityNames(ctx context.Context, sqlDB *sql.DB) map[string]struct{} {
	out := make(map[string]struct{})
	rows, err := sqlDB.QueryContext(ctx, `SELECT content FROM graph_nodes WHERE id LIKE 'entity:%' AND TRIM(COALESCE(content,'')) != ''`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return out
		}
		out[strings.ToLower(strings.TrimSpace(content))] = struct{}{}
	}
	return out
}

// keepEntityCandidate decides whether a raw extracted candidate is a meaningful
// entity. Favors precision: commands/config (anything with whitespace or shell
// metacharacters), over-long strings, and common English words are dropped. A
// single-occurrence candidate survives only if it looks structurally like an
// entity (domain/path/identifier/CamelCase/has-digit) or is a short acronym;
// otherwise it must recur across texts.
func keepEntityCandidate(name string, freq int) bool {
	trimmed := strings.TrimSpace(name)
	n := len([]rune(trimmed))
	if n < 2 || n > 40 {
		return false
	}
	if !entityCharsOK(trimmed) {
		return false // rejects shell commands, config blocks, prose snippets
	}
	if _, stop := entityStopwords[strings.ToLower(trimmed)]; stop {
		return false
	}
	if strongEntityPattern(trimmed) {
		return true
	}
	// Short all-caps acronyms (RAG, CGO, JSON, OIDC, MCP) are usually real.
	if isAcronym(trimmed) {
		return true
	}
	return freq >= 2
}

// entityCharsOK requires an entity to start alphanumeric and contain only
// identifier-ish characters (letters, digits, and . _ - /). No whitespace, no
// shell/punctuation metacharacters — that excludes commands and config blobs
// the backtick extractor otherwise captures wholesale.
func entityCharsOK(name string) bool {
	for i, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// ok
		case r == '.' || r == '_' || r == '-' || r == '/':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// strongEntityPattern reports whether a name looks like a real entity on its
// own: a domain/path/identifier, CamelCase, or containing a digit.
func strongEntityPattern(name string) bool {
	upperAfterLower := false
	prevLower := false
	hasDigit := false
	for _, r := range name {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsUpper(r):
			if prevLower {
				upperAfterLower = true
			}
			prevLower = false
		case unicode.IsLower(r):
			prevLower = true
		default:
			prevLower = false
		}
	}
	if strings.ContainsAny(name, "._-/") {
		return true
	}
	if hasDigit {
		return true
	}
	return upperAfterLower // CamelCase, e.g. "DataIntelligence"
}

// isAcronym reports whether a name is a short all-uppercase token (2–5 chars).
func isAcronym(name string) bool {
	r := []rune(name)
	if len(r) < 2 || len(r) > 5 {
		return false
	}
	for _, c := range r {
		if !unicode.IsUpper(c) && !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}
