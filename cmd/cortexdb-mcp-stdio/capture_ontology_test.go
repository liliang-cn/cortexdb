package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// captureTestSchema is a vocabulary-mode schema, the shape the shared brain
// runs: declared types steer the extractor without refusing keyless writes.
func captureTestSchema() cortexdb.OntologySchema {
	str := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	return cortexdb.OntologySchema{
		SchemaID:    "brain",
		Name:        "Brain",
		Enforcement: cortexdb.OntologyEnforcementVocabulary,
		ObjectTypes: []cortexdb.OntologyObjectType{
			{
				APIName: "Host", Description: "A machine\n  that runs   services.", PrimaryKey: "hostName",
				Aliases:    []string{"server", "machine"},
				Properties: []cortexdb.OntologyProperty{{APIName: "hostName", DataType: str, Required: true}},
			},
			{
				APIName: "Service", Description: "A long-running program.", PrimaryKey: "serviceName",
				Properties: []cortexdb.OntologyProperty{{APIName: "serviceName", DataType: str, Required: true}},
			},
			{
				APIName: "Mainframe", Description: "Retired.", PrimaryKey: "mainframeName", Status: cortexdb.OntologyStatusDeprecated,
				Properties: []cortexdb.OntologyProperty{{APIName: "mainframeName", DataType: str, Required: true}},
			},
		},
	}
}

func openCaptureTestBrain(t *testing.T, withSchema bool) *cortexdb.DB {
	t.Helper()
	withoutTheMachinesModels(t)
	db, err := openBrainDB(filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if withSchema {
		if _, err := db.SaveOntologySchema(context.Background(), cortexdb.OntologySaveRequest{Schema: captureTestSchema(), Activate: true}); err != nil {
			t.Fatalf("activate schema: %v", err)
		}
	}
	return db
}

type systemPromptRecorder struct {
	system string
	reply  string
}

func (p *systemPromptRecorder) GenerateJSON(_ context.Context, system, _ string) ([]byte, error) {
	p.system = system
	if p.reply == "" {
		return []byte(`{"memories":[]}`), nil
	}
	return []byte(p.reply), nil
}

// With an active ontology the extractor is told its declared types — api
// name, a one-line description, aliases as "also called" — and to use one or
// leave the type empty; a deprecated type is not offered.
func TestTheCaptureIsToldTheActiveOntologysTypes(t *testing.T) {
	ctx := context.Background()
	db := openCaptureTestBrain(t, true)
	vocab := captureVocabulary(ctx, localBrain{db})
	llm := &systemPromptRecorder{}
	if _, err := extractSessionFacts(ctx, llm, "USER: deploy it", "", time.Now(), vocab); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"OBJECT TYPES (the brain's active ontology):\n",
		"- Host: A machine that runs services. (also called server, machine)\n",
		"- Service: A long-running program.\n",
		`leave type empty ("")`,
		"Never invent a type.",
	} {
		if !strings.Contains(llm.system, want) {
			t.Errorf("prompt lacks %q:\n%s", want, llm.system)
		}
	}
	if strings.Contains(llm.system, "Mainframe") {
		t.Errorf("a deprecated type was offered:\n%s", llm.system)
	}
	if !strings.HasSuffix(llm.system, capturePromptJSON) {
		t.Errorf("the reply shape is no longer last:\n%s", llm.system)
	}
}

// The shared brain is remote: the types come from its ontology_list tool over
// gRPC, not from this machine's local file.
func TestTheCaptureReadsTheSharedBrainsOntologyOverGRPC(t *testing.T) {
	ctx := context.Background()
	withoutTheMachinesModels(t)
	const token = "shared-secret"
	addr, shared := startTestBrain(t, token)
	if _, err := shared.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: captureTestSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORTEXDB_PATH", filepath.Join(t.TempDir(), "local.db"))
	t.Setenv("CORTEXDB_REMOTE", addr)
	t.Setenv("CORTEXDB_GRPC_TOKEN", token)
	w, _, local, closeFn, err := openBrainWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if local != nil {
		t.Fatal("CORTEXDB_REMOTE set, yet a local brain was opened")
	}
	vocab := captureVocabulary(ctx, w)
	if vocab.SchemaID != "brain" || len(vocab.Types) != 2 || vocab.Types[0].APIName != "Host" {
		t.Fatalf("vocabulary over gRPC = %+v", vocab)
	}

	// A brain that cannot answer leaves the prompt as it always was.
	t.Setenv("CORTEXDB_GRPC_TOKEN", "wrong")
	w2, _, _, closeFn2, err := openBrainWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn2()
	if v := captureVocabulary(ctx, w2); !v.Empty() {
		t.Fatalf("a refused ontology_list still gave types: %+v", v)
	}
}

// captureSystemPromptSHA256 is the sha256 of the capture prompt before #153.
// With no active ontology the prompt must still be exactly that.
const captureSystemPromptSHA256 = "7b34d77188a3bde12e2a1f28bb0370089b2bb0f7a7f199313a1d4837fb898d40"

type failingOntologyBrain struct{ brainClient }

func (failingOntologyBrain) ActiveOntologies(context.Context) ([]cortexdb.OntologySchema, error) {
	return nil, errors.New("unavailable")
}

// No active ontology, a brain that fails to say, and a writer that cannot be
// asked all give the prompt capture always used, byte for byte.
func TestTheCapturePromptIsUnchangedWithoutAnOntology(t *testing.T) {
	ctx := context.Background()
	db := openCaptureTestBrain(t, false)
	for name, w := range map[string]brainClient{
		"no schema":     localBrain{db},
		"failed fetch":  failingOntologyBrain{localBrain{db}},
		"cannot answer": struct{ brainClient }{localBrain{db}},
	} {
		llm := &systemPromptRecorder{}
		if _, err := extractSessionFacts(ctx, llm, "USER: deploy it", "", time.Now(), captureVocabulary(ctx, w)); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(llm.system))
		if got := hex.EncodeToString(sum[:]); got != captureSystemPromptSHA256 {
			t.Errorf("%s: prompt changed (sha256 %s):\n%s", name, got, llm.system)
		}
	}
}

// An entity the model leaves untyped is saved as the store's generic entity,
// not as "" or a made-up word; an alias is saved as the declared api name, an
// undeclared type is dropped, and "file" — which the FILES rule asks for —
// is kept.
func TestAnUntypedCapturedEntityIsSavedAsAGenericEntity(t *testing.T) {
	ctx := context.Background()
	db := openCaptureTestBrain(t, true)
	brain := localBrain{db}
	llm := &systemPromptRecorder{reply: `{"memories":[{"slug":"dell-runs-brain","content":"CortexDB's shared brain runs on dell as the brain service; see main.go.","importance":0.6,"type":"fact","entities":[
		{"name":"dell","type":"SERVER"},
		{"name":"brain","type":""},
		{"name":"CortexDB","type":"Gizmo"},
		{"name":"main.go","type":"file"},
		{"name":"cortexdb-grpc","type":"  service "}]}]}`}
	facts, err := extractSessionFacts(ctx, llm, "USER: deploy it", "", time.Now(), captureVocabulary(ctx, brain))
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts %+v, err %v", facts, err)
	}
	req := capturedRequest("33333333-session", "2026-11-10", facts[0])
	if err := brain.SaveMemory(ctx, req); err != nil {
		t.Fatalf("an untyped entity failed the save: %v", err)
	}
	nodes, err := db.Graph().GetAllNodes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, n := range nodes {
		got[n.Content] = n.NodeType
	}
	for name, want := range map[string]string{
		"dell":          "Host",
		"brain":         "entity",
		"CortexDB":      "entity",
		"main.go":       "file",
		"cortexdb-grpc": "Service",
	} {
		if got[name] != want {
			t.Errorf("%s saved as type %q, want %q", name, got[name], want)
		}
	}
}
