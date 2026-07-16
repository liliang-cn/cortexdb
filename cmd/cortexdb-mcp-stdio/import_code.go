package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// codeGraphFile is the language-agnostic code-graph payload produced by a code
// extractor (e.g. a Claude Code agent walking any repo) and consumed by
// --import-code-graph. It is deliberately simple: named symbols with a type and
// their source file, plus the relationships between them. Because the extractor
// is an LLM/agent rather than a per-language parser, this works for any
// language — Go, Python, TypeScript, Rust, Java, …
type codeGraphFile struct {
	// Language is an optional hint (e.g. "go", "python", "mixed").
	Language string `json:"language,omitempty"`
	Entities []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`           // package|module|file|class|type|interface|function|method|const
		File    string `json:"file,omitempty"` // source path the symbol is defined in
		Summary string `json:"summary,omitempty"`
	} `json:"entities"`
	Relations []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Type string `json:"type"` // imports|defines|has_method|implements|extends|calls|references
	} `json:"relations"`
}

// runImportCodeGraph reads a code-graph JSON file (or stdin when the path is "-"
// or omitted) and upserts it into the CortexDB knowledge graph as typed entity
// nodes and typed relation edges. One-shot mode behind `--import-code-graph`.
//
// The graph lands in whatever database CORTEXDB_PATH points at, so a code graph
// can be kept in its own file (isolated from the personal brain) simply by
// exporting CORTEXDB_PATH before running. Idempotent: re-importing updates in
// place.
func runImportCodeGraph(args []string) {
	path := "-"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		path = args[0]
	}

	var raw []byte
	var err error
	if path == "-" {
		raw, err = readAllStdin()
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: read code graph: %v\n", err)
		os.Exit(1)
	}

	var cg codeGraphFile
	if err := json.Unmarshal(stripBOM(raw), &cg); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: parse code graph json: %v\n", err)
		os.Exit(1)
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Collect entities; also index them so relation endpoints that were only
	// mentioned in relations still become nodes (never a dangling edge).
	seen := make(map[string]struct{})
	entities := make([]cortexdb.ToolEntityInput, 0, len(cg.Entities))
	addEntity := func(name, typ, file, summary string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		l := strings.ToLower(name)
		if _, ok := seen[l]; ok {
			return
		}
		seen[l] = struct{}{}
		e := cortexdb.ToolEntityInput{Name: name, Type: firstNonEmptyCode(typ, "symbol"), Description: summary}
		if file != "" {
			e.Metadata = map[string]string{"file": file}
		}
		entities = append(entities, e)
	}
	for _, e := range cg.Entities {
		addEntity(e.Name, e.Type, e.File, e.Summary)
	}

	relSeen := make(map[string]struct{})
	relations := make([]cortexdb.ToolRelationInput, 0, len(cg.Relations))
	for _, r := range cg.Relations {
		from := strings.TrimSpace(r.From)
		to := strings.TrimSpace(r.To)
		if from == "" || to == "" || strings.EqualFold(from, to) {
			continue
		}
		// Backfill endpoints that were not declared as entities.
		addEntity(from, "symbol", "", "")
		addEntity(to, "symbol", "", "")
		typ := firstNonEmptyCode(strings.TrimSpace(r.Type), "references")
		key := strings.ToLower(from) + "\x00" + strings.ToLower(to) + "\x00" + typ
		if _, ok := relSeen[key]; ok {
			continue
		}
		relSeen[key] = struct{}{}
		relations = append(relations, cortexdb.ToolRelationInput{From: from, To: to, Type: typ})
	}

	tools := db.GraphRAGTools()
	if len(entities) > 0 {
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: upsert entities: %v\n", err)
			os.Exit(1)
		}
	}
	if len(relations) > 0 {
		if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations}); err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: upsert relations: %v\n", err)
			os.Exit(1)
		}
	}

	lang := cg.Language
	if lang == "" {
		lang = "code"
	}
	fmt.Printf("imported %s knowledge graph: %d entities, %d relations into %s\n",
		lang, len(entities), len(relations), dbPath)
}

func firstNonEmptyCode(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// utf8BOM is the 3-byte UTF-8 byte order mark, matched by value so this source
// file never contains a literal BOM (which is a Go syntax error).
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// stripBOM removes a leading UTF-8 byte order mark if present.
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, utf8BOM)
}

func readAllStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}
