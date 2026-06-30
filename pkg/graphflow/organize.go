package graphflow

import (
	"context"
	"fmt"
	"strings"

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
	EntityCount      int `json:"entity_count"`
	RelationCount    int `json:"relation_count"`
}

// OrganizeFromBrain extracts entities and relations from stored memories (and,
// optionally, durable knowledge) and writes them into the knowledge graph, so a
// brain that only holds free-text memories gains a navigable entity graph —
// "organizing" the memory rather than only displaying whatever was tagged.
//
// Extraction is deterministic (no LLM or embedder, no network): proper-noun /
// backtick entities, plus co-occurrence ("co_occurs") relations between entities
// that share a sentence. Entities are written through the public GraphRAG upsert
// path, so they get the same "entity:<name>" ids and lexical vectors that
// SaveKnowledge uses — extracted entities merge with explicitly-saved ones and
// are visible to recall / expand_graph. Re-running is idempotent.
func OrganizeFromBrain(ctx context.Context, db *cortexdb.DB, opts OrganizeOptions) (*OrganizeReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: organize: nil db")
	}
	includeMem, includeKnow := opts.IncludeMemories, opts.IncludeKnowledge
	if !includeMem && !includeKnow {
		includeMem = true // sensible default: organize memories
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

	// Gather unique entity names and co-occurrence relations across all texts.
	entitySeen := make(map[string]struct{})
	entities := make([]cortexdb.ToolEntityInput, 0)
	relSeen := make(map[string]struct{})
	relations := make([]cortexdb.ToolRelationInput, 0)
	addEntity := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if _, ok := entitySeen[key]; ok {
			return
		}
		entitySeen[key] = struct{}{}
		entities = append(entities, cortexdb.ToolEntityInput{Name: name, Type: "entity"})
	}
	for _, text := range texts {
		for _, name := range extractEntities(text) {
			addEntity(name)
		}
		for _, sentence := range splitSentences(text) {
			ents := extractEntities(sentence)
			for i := 0; i+1 < len(ents); i++ {
				from, to := ents[i], ents[i+1]
				if strings.EqualFold(from, to) {
					continue
				}
				key := strings.ToLower(from) + "\x00" + strings.ToLower(to)
				if _, ok := relSeen[key]; ok {
					continue
				}
				relSeen[key] = struct{}{}
				relations = append(relations, cortexdb.ToolRelationInput{From: from, To: to, Type: "co_occurs"})
			}
		}
	}

	if len(entities) == 0 {
		return report, nil
	}

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		return nil, fmt.Errorf("graphflow: organize upsert entities: %w", err)
	}
	report.EntityCount = len(entities)
	if len(relations) > 0 {
		if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations}); err != nil {
			return nil, fmt.Errorf("graphflow: organize upsert relations: %w", err)
		}
		report.RelationCount = len(relations)
	}
	return report, nil
}
