package graphflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

type oneDocDetector struct{ doc SourceDocument }

func (d oneDocDetector) Detect(context.Context, string) ([]SourceDocument, error) {
	return []SourceDocument{d.doc}, nil
}

type recordingGenerator struct {
	prompts []string
	reply   string
}

func (g *recordingGenerator) GenerateJSON(_ context.Context, _, user string) ([]byte, error) {
	g.prompts = append(g.prompts, user)
	return []byte(g.reply), nil
}

const vocabularyTestReply = `{"nodes":[
	{"id":"dell","label":"dell","type":"server"},
	{"id":"brain","label":"brain","type":""},
	{"id":"cortexdb","label":"CortexDB","type":"Gizmo"}],
 "edges":[{"source":"brain","target":"dell","relation":"runs_on","confidence":"EXTRACTED","directed":true}]}`

func vocabularyTestDoc() SourceDocument {
	return SourceDocument{ID: "doc-1", Type: "markdown", Title: "Brain", Path: "brain.md", Content: "The brain runs on dell."}
}

func openVocabularyTestDB(t *testing.T, withSchema bool) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "graph.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if !withSchema {
		return db
	}
	str := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	schema := cortexdb.OntologySchema{
		SchemaID: "brain", Name: "Brain", Enforcement: cortexdb.OntologyEnforcementVocabulary,
		ObjectTypes: []cortexdb.OntologyObjectType{{
			APIName: "Host", Description: "A machine that runs services.", PrimaryKey: "hostName",
			Aliases:    []string{"server"},
			Properties: []cortexdb.OntologyProperty{{APIName: "hostName", DataType: str, Required: true}},
		}},
	}
	if _, err := db.SaveOntologySchema(context.Background(), cortexdb.OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate schema: %v", err)
	}
	return db
}

func runVocabularyPipeline(t *testing.T, db *cortexdb.DB) (*recordingGenerator, map[string]string) {
	t.Helper()
	gen := &recordingGenerator{reply: vocabularyTestReply}
	pipeline, err := NewPipeline(oneDocDetector{vocabularyTestDoc()}, LLMExtractor{Client: gen})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Run(context.Background(), db, RunRequest{Root: "."}); err != nil {
		t.Fatalf("run: %v", err)
	}
	nodes, err := db.Graph().GetAllNodes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, n := range nodes {
		types[n.Content] = n.NodeType
	}
	return gen, types
}

// With an active ontology, a pipeline run tells the LLM extractor the declared
// types, and node types come back as the schema spells them: an alias becomes
// the api name, an empty or undeclared type the generic graphflow_node.
func TestGraphflowExtractorIsToldTheActiveOntologysTypes(t *testing.T) {
	gen, types := runVocabularyPipeline(t, openVocabularyTestDB(t, true))
	if len(gen.prompts) != 1 {
		t.Fatalf("prompts: %d", len(gen.prompts))
	}
	prompt := gen.prompts[0]
	for _, want := range []string{
		"OBJECT TYPES (the active ontology):\n- Host: A machine that runs services. (also called server)\n",
		`leave type empty ("")`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Index(prompt, "OBJECT TYPES") > strings.Index(prompt, "Return JSON with:") {
		t.Errorf("the types come after the reply shape:\n%s", prompt)
	}
	for label, want := range map[string]string{"dell": "Host", "brain": "graphflow_node", "CortexDB": "graphflow_node"} {
		if types[label] != want {
			t.Errorf("%s stored as %q, want %q", label, types[label], want)
		}
	}
}

// Without an active ontology the prompt is the one graphflow always sent, and
// the model's types are stored as given.
func TestGraphflowPromptIsUnchangedWithoutAnOntology(t *testing.T) {
	gen, types := runVocabularyPipeline(t, openVocabularyTestDB(t, false))
	doc := vocabularyTestDoc()
	// The prompt as it was before #153, verbatim.
	want := fmt.Sprintf(
		"source_id: %s\nsource_type: %s\ntitle: %s\npath: %s\n\nDocument:\n%s\n\nReturn JSON with:\n{\n  \"nodes\": [{\"id\":\"...\",\"label\":\"...\",\"type\":\"...\",\"summary\":\"...\",\"source_file\":\"...\",\"source_location\":\"...\"}],\n  \"edges\": [{\"source\":\"...\",\"target\":\"...\",\"relation\":\"...\",\"confidence\":\"EXTRACTED|INFERRED|AMBIGUOUS\",\"directed\":true,\"source_file\":\"...\",\"source_location\":\"...\"}]\n}",
		doc.ID, doc.Type, doc.Title, doc.Path, doc.Content,
	)
	if len(gen.prompts) != 1 || gen.prompts[0] != want {
		t.Fatalf("prompt changed:\n%q\nwant\n%q", gen.prompts, want)
	}
	for label, want := range map[string]string{"dell": "server", "brain": "graphflow_node", "CortexDB": "Gizmo"} {
		if types[label] != want {
			t.Errorf("%s stored as %q, want %q", label, types[label], want)
		}
	}
}
