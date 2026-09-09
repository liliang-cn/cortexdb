// Vectors, a knowledge graph, and an ontology — one brain, one estate, and
// what each layer can answer that the other two cannot.
//
// The other examples show the three separately: 02_rag and 14_semantic_rag for
// retrieval, 04_knowledge_graph for graph semantics, 16_ontology for typed
// objects and governed writes. This one puts all three over a single body of
// facts and spends most of its length on the SEAMS, because that is where the
// combination earns anything.
//
// The estate is a replicated block-storage cluster — DRBD resources on LINSTOR
// nodes — chosen because it has a real invariant (a resource is Primary on at
// most one node) and real prose (runbooks and incident notes) that talks about
// the same objects in different words.
//
// The three questions the example is organised around:
//
//	"how do I recover when the replicas disagree?"   only the text can answer
//	"which resources sit on a nearly full pool?"     only the structure can
//	"may hp become primary for sds-meta as well?"    only the ontology can
//
// An embedder is OPTIONAL. Without one, retrieval is lexical and two sections
// say so and show what is lost — which is itself the clearest argument for
// vectors in this file. With one, set:
//
//	OPENAI_API_KEY    required to enable the embedder
//	OPENAI_BASE_URL   default https://dashscope.aliyuncs.com/compatible-mode/v1
//	EMBED_MODEL       default text-embedding-v4
//	EMBED_DIM         default 1024
//
//	go run ./examples/18_vector_graph_ontology
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	_ = godotenv.Load(".env", "../../.env")
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "three-layers")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := cortexdb.DefaultConfig(filepath.Join(dir, "estate.db"))
	var opts []cortexdb.Option
	embedder, note := embedderFromEnv()
	if embedder != nil {
		cfg.Dimensions = embedder.Dim()
		opts = append(opts, cortexdb.WithEmbedder(embedder))
	}
	db, err := cortexdb.Open(cfg, opts...)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()

	fmt.Println("retrieval:", note)
	tools := db.GraphRAGTools()

	// ---------------------------------------------------------------------
	section("1. The ontology — what is allowed to exist")

	schema := storageSchema()
	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
		Schema: schema, Activate: true,
	}); err != nil {
		return fmt.Errorf("save schema: %w", err)
	}
	fmt.Printf("activated %q: %d object types, %d links, %d actions, %d interface\n",
		schema.SchemaID, len(schema.ObjectTypes), len(schema.LinkTypes),
		len(schema.ActionTypes), len(schema.InterfaceTypes))
	fmt.Println("from here on, every write to the graph is checked against it.")

	// ---------------------------------------------------------------------
	section("2. The estate — a graph the schema agrees with")

	// Nodes arrive through an action rather than a raw upsert. An action is a
	// named, parameterised, auditable write: the same four nodes could have
	// been upserted directly, but then "who may add a node, and what must be
	// true of one" would live in whatever code happened to call the upsert.
	for _, n := range nodes {
		if _, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
			Action: "registerNode",
			Parameters: map[string]string{
				"nodeName": n.name, "site": n.site, "nodeRole": n.role,
			},
			Actor: "example",
		}); err != nil {
			return fmt.Errorf("registerNode %s: %w", n.name, err)
		}
	}
	fmt.Printf("registered %d nodes through the governed action\n", len(nodes))

	// Pools, resources and volumes arrive through the generic upsert, which is
	// still validated: an undeclared type or an unknown property is refused.
	// Both write paths land in the same graph and obey the same schema.
	entities := make([]cortexdb.ToolEntityInput, 0, len(pools)+len(resources)+len(volumes))
	for _, p := range pools {
		entities = append(entities, cortexdb.ToolEntityInput{
			Name: p.key, Type: "StoragePool", Metadata: map[string]string{
				"poolKey": p.key, "displayName": p.display, "onNode": p.onNode,
				"backing": p.backing,
				"sizeGiB": strconv.Itoa(p.sizeGiB), "freeGiB": strconv.Itoa(p.freeGiB),
			}})
	}
	for _, r := range resources {
		props := map[string]string{
			"resourceName": r.name,
			"quorum":       strconv.FormatBool(r.quorum),
			"purpose":      r.purpose,
		}
		// Absent and empty are different things to the schema: an optional
		// property may be left out, and it may not be present-and-blank. So
		// backup-vault, which nobody has promoted, simply has no primaryOn.
		if r.primaryOn != "" {
			props["primaryOn"] = r.primaryOn
		}
		entities = append(entities, cortexdb.ToolEntityInput{
			Name: r.name, Type: "Resource", Metadata: props})
	}
	for _, v := range volumes {
		entities = append(entities, cortexdb.ToolEntityInput{
			Name: v.key, Type: "Volume", Metadata: map[string]string{
				"volumeKey": v.key, "displayName": v.display, "ofResource": v.ofResource,
				"sizeGiB": strconv.Itoa(v.sizeGiB), "minor": strconv.Itoa(v.minor),
			}})
	}
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		return fmt.Errorf("upsert entities: %w", err)
	}

	relations := make([]cortexdb.ToolRelationInput, 0, 16)
	for _, p := range pools {
		relations = append(relations, cortexdb.ToolRelationInput{
			From: p.onNode, To: p.key, Type: "poolLocation"})
	}
	for _, v := range volumes {
		relations = append(relations, cortexdb.ToolRelationInput{
			From: v.ofResource, To: v.key, Type: "volumeOf"})
	}
	for _, r := range resources {
		if r.primaryOn != "" {
			relations = append(relations, cortexdb.ToolRelationInput{
				From: r.primaryOn, To: r.name, Type: "resourcePrimary"})
		}
		for _, node := range r.replicas {
			relations = append(relations, cortexdb.ToolRelationInput{
				From: node, To: r.name, Type: "resourceReplica"})
		}
	}
	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations}); err != nil {
		return fmt.Errorf("upsert relations: %w", err)
	}
	fmt.Printf("wrote %d objects and %d links: %d pools, %d resources, %d volumes\n",
		len(entities), len(relations), len(pools), len(resources), len(volumes))

	// ---------------------------------------------------------------------
	section("3. What the ontology refuses")

	// (a) An object type nobody declared. The graph would happily hold it;
	// the schema is what has an opinion.
	_, err = tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
		Entities: []cortexdb.ToolEntityInput{
			{Name: "lto8-library", Type: "TapeLibrary", Metadata: map[string]string{"slots": "48"}},
		}})
	fmt.Printf("an undeclared object type      → %s\n", oneLine(err))

	// (b) A parameter that fails the action's own submission criteria. This is
	// checked before anything is read, so validate_only costs nothing.
	bad, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
		Action:       "registerNode",
		Parameters:   map[string]string{"nodeName": "Rack-B Node!", "site": "rack-b"},
		ValidateOnly: true,
	})
	if err != nil {
		return fmt.Errorf("validate registerNode: %w", err)
	}
	fmt.Printf("a node name that is not a label → rejected: %s\n", strings.Join(bad.Errors, "; "))

	// (c) THE ONE THAT MATTERS. sds-meta is already primary on dell. Making it
	// primary on hp as well is not a typo, it is the two-writer state that
	// destroys a filesystem — and it is refused because one side of the
	// resourcePrimary link is declared ONE, not because anything in this file
	// remembered to check.
	_, err = tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{
			{From: "hp", To: "sds-meta", Type: "resourcePrimary"},
		}})
	fmt.Printf("a second Primary for sds-meta   → %s\n", oneLine(err))

	// The same node may hold a second REPLICA of the same resource's peer,
	// because that link's sides are both MANY. One word apart in the schema.
	_, err = tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{
			{From: "openclaw", To: "vm-store", Type: "resourceReplica"},
		}})
	fmt.Printf("a third replica of vm-store     → %s\n", oneLine(err))

	// ---------------------------------------------------------------------
	section("4. The prose — the same objects, in words")

	// Two calls per document, and both are needed.
	//
	// The save declares the entities so the built-in extractor leaves those
	// names alone — otherwise it would build a second, untyped node for
	// "sds-meta" beside the typed one, and every later link would resolve to
	// the wrong end. The upsert that follows carries the chunk ids the save
	// just returned, and that is what writes the mention edges: an entity is
	// joined to a document BY the chunks that mention it, so the chunks have
	// to exist first.
	//
	// The types and primary keys are the ones the ontology already declares,
	// so this lands ON the objects built in section 2 rather than creating
	// look-alikes beside them. That is the whole join: one graph, described
	// twice, once in rows and once in prose.
	mentions := 0
	for _, doc := range corpus() {
		saved, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: doc.id,
			Title:       doc.title,
			Content:     doc.body,
			Collection:  "runbooks",
			Entities:    doc.entities,
		})
		if err != nil {
			return fmt.Errorf("save %s: %w", doc.id, err)
		}
		named := make([]cortexdb.ToolEntityInput, 0, len(doc.entities))
		for _, e := range doc.entities {
			e.ChunkIDs = saved.Knowledge.ChunkIDs
			named = append(named, e)
		}
		linked, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
			DocumentID: doc.id, Entities: named,
		})
		if err != nil {
			return fmt.Errorf("link %s: %w", doc.id, err)
		}
		mentions += linked.MentionEdgeCount
	}
	fmt.Printf("indexed %d documents and wrote %d mention edges to the objects they name\n",
		len(corpus()), mentions)

	// ---------------------------------------------------------------------
	section("5. Only the text layer can answer this")

	// Nothing in the corpus contains the words of this question. The document
	// that answers it talks about generation identifiers, StandAlone and
	// discarding a divergence. Lexical retrieval has almost nothing to match;
	// an embedding model has the meaning.
	const meaning = "the two machines each believe they are the one holding the real data"
	fmt.Printf("Q: %q\n", meaning)
	hits, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		Query: meaning, Collection: "runbooks", TopK: 3, MaxHops: 1,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	printHits(hits)
	// The document that actually answers it is rb-standalone. Naming it and
	// printing where it landed is worth more than asserting that retrieval
	// works: run this twice, once with an embedder and once without, and the
	// two rankings are the measurement. On this eight-document corpus a small
	// local embedding model does not reliably beat keyword overlap.
	//
	// Ignore the score column while comparing. It is a reciprocal-rank-fusion
	// score with k=60, so the first three results are 1/61, 1/62 and 1/63 — a
	// band 0.0005 wide that rounds to the same number and says nothing about
	// how confident either retriever was. The RANK is the signal here; the
	// score is an artefact of the fusion constant.
	fmt.Printf("   the document that answers it is %q — ranked %s here\n",
		"Recovering a resource that went StandAlone", rankOf(hits, "rb-standalone"))
	if embedder == nil {
		fmt.Println("   lexical mode: that ranking is keyword overlap, and the question")
		fmt.Println("   shares almost no keywords with the answer. Set OPENAI_API_KEY")
		fmt.Println("   to rank the same corpus by meaning and compare.")
	}
	fmt.Println("The structure layer cannot answer this at all: no property holds a procedure.")

	// ---------------------------------------------------------------------
	section("6. Only the structure layer can answer this")

	// "Which resources have a replica on a node whose pool is nearly full?"
	//
	// Three hops, each typed, composed as ONE expression rather than three
	// queries with application code between them: pools under 500 GiB free →
	// the nodes they sit on → the resources replicated there.
	lowPools := cortexdb.ObjectSet{
		Kind:   cortexdb.ObjectSetFilter,
		Source: &cortexdb.ObjectSet{Kind: cortexdb.ObjectSetBase, ObjectType: "StoragePool"},
		Where: &cortexdb.ObjectSetPredicate{
			Op: cortexdb.PredicateLt, Property: "freeGiB", Value: "500",
		},
	}
	crowdedNodes := cortexdb.ObjectSet{
		Kind: cortexdb.ObjectSetSearchAround, Link: "node", Source: &lowPools,
	}
	atRisk := cortexdb.ObjectSet{
		Kind: cortexdb.ObjectSetSearchAround, Link: "replicates", Source: &crowdedNodes,
	}

	for _, step := range []struct {
		label string
		set   cortexdb.ObjectSet
	}{
		{"pools with under 500 GiB free", lowPools},
		{"  → the nodes they sit on", crowdedNodes},
		{"  → resources replicated there", atRisk},
	} {
		resolved, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{ObjectSet: step.set})
		if err != nil {
			return fmt.Errorf("resolve %s: %w", step.label, err)
		}
		fmt.Printf("%-32s %v\n", step.label, titles(resolved))
	}

	// An interface answers across object types that share nothing else. A pool
	// and a volume have different keys, different lives and different owners;
	// "everything with a size" is still a reasonable question.
	sized, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{
			Kind: cortexdb.ObjectSetInterfaceBase, InterfaceType: "Sized",
		},
	})
	if err != nil {
		return fmt.Errorf("resolve interface: %w", err)
	}
	fmt.Printf("%-32s %d objects across %s\n", "everything Sized", sized.Total, typesIn(sized))
	fmt.Println("The text layer cannot answer this: no runbook lists what is replicated where.")

	// ---------------------------------------------------------------------
	section("7. The seams — where the three stop being separate")

	// 7a. TEXT → STRUCTURE. The passage found by meaning names an object; the
	// graph turns that name into the instances actually at risk. Retrieval
	// found the idea, traversal found the blast radius.
	fmt.Println("(a) a passage names an object; the graph says what that object touches")
	if len(hits.Results) > 0 {
		top := hits.Results[0]
		fmt.Printf("    top passage: %q\n", top.Title)
		// The chunks are graph nodes, and the mention edges written in
		// section 4 run from them to the estate objects. So walking out of a
		// retrieved chunk IS the join — no lookup table, no second store.
		expanded, err := tools.ExpandGraph(ctx, cortexdb.ToolExpandGraphRequest{
			NodeIDs: top.ChunkIDs, MaxHops: 1, Limit: 60,
		})
		if err != nil {
			return fmt.Errorf("expand: %w", err)
		}
		involved := objectsIn(expanded)
		fmt.Printf("    one hop out of its chunks: %s\n", describeNeighbourhood(expanded))
		fmt.Printf("    the estate objects it is about: %v\n", involved)
		if len(involved) > 0 {
			reach, err := tools.ExpandGraph(ctx, cortexdb.ToolExpandGraphRequest{
				NodeIDs: idsOf(expanded, involved), MaxHops: 1, Limit: 80,
			})
			if err != nil {
				return fmt.Errorf("expand estate: %w", err)
			}
			fmt.Printf("    and what those touch: %v\n", objectsIn(reach))
		}
		fmt.Println("    retrieval found the procedure; the graph found what it applies to.")
	}

	// 7b. STRUCTURE → TEXT. The reverse direction. The object set produced an
	// exact, typed answer in section 6; feeding those names back as entity
	// seeds pulls only the prose about them — the ontology telling retrieval
	// what to look for, instead of hoping the words line up.
	fmt.Println("(b) an exact set pulls the prose about it, instead of hoping words match")
	risk, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{ObjectSet: atRisk})
	if err != nil {
		return fmt.Errorf("resolve at-risk: %w", err)
	}
	seeds := titles(risk)
	fmt.Printf("    seeds from section 6: %v\n", seeds)
	// The same mention edges, walked the other way: from the objects the
	// ontology named, out to the passages that mention them.
	back, err := tools.ExpandGraph(ctx, cortexdb.ToolExpandGraphRequest{
		NodeIDs: objectIDs(risk), MaxHops: 1, Limit: 40,
	})
	if err != nil {
		return fmt.Errorf("expand back to prose: %w", err)
	}
	found := 0
	for _, n := range back.Nodes {
		if n.NodeType != "chunk" || n.Content == "" {
			continue
		}
		found++
		fmt.Printf("    · %s\n", firstLine(n.Content))
	}
	if found == 0 {
		fmt.Println("    (no prose mentions it)")
	}
	fmt.Println("    the question was structural; the answer came back as prose about")
	fmt.Println("    exactly the objects the structure named, and nothing else.")

	// 7c. VECTOR INSIDE THE ONTOLOGY. Not retrieval that returns text chunks,
	// and not a filter over properties — a nearest-neighbour predicate INSIDE
	// a typed object set. The answer is Resources, with their keys and
	// properties, ranked by what they are for.
	fmt.Println("(c) retrieval as one operand of a typed set expression")
	// Not "search, then filter in Go". The passages found by meaning become a
	// STATIC object set, and that set is then intersected and traversed like
	// any other — so the answer is objects with their keys and properties,
	// which you can walk on from, rather than a paragraph you have to read.
	recall, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		Query: "what do we do when a replica set loses its majority", Collection: "runbooks", TopK: 3,
	})
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}
	mentioned, err := tools.ExpandGraph(ctx, cortexdb.ToolExpandGraphRequest{
		NodeIDs: chunkIDsOf(recall), MaxHops: 1, Limit: 60,
	})
	if err != nil {
		return fmt.Errorf("expand recall: %w", err)
	}
	fromProse := cortexdb.ObjectSet{Kind: cortexdb.ObjectSetStatic, ObjectIDs: nodeIDsOfType(mentioned, "Resource")}
	// The second operand is a graph traversal: everything replicated on hp.
	hp := cortexdb.ObjectSet{
		Kind:   cortexdb.ObjectSetFilter,
		Source: &cortexdb.ObjectSet{Kind: cortexdb.ObjectSetBase, ObjectType: "Node"},
		Where:  &cortexdb.ObjectSetPredicate{Op: cortexdb.PredicateEq, Property: "nodeName", Value: "hp"},
	}
	onHP := cortexdb.ObjectSet{Kind: cortexdb.ObjectSetSearchAround, Link: "replicates", Source: &hp}
	fused, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{
			Kind: cortexdb.ObjectSetIntersect, Operands: []cortexdb.ObjectSet{fromProse, onHP},
		},
	})
	if err != nil {
		return fmt.Errorf("fuse: %w", err)
	}
	fmt.Printf("    resources the retrieved passages are about: %v\n",
		titlesOfIDs(mentioned, fromProse.ObjectIDs))
	fmt.Printf("    ∩ the ones actually replicated on hp:       %v\n", titles(fused))
	fmt.Println("    the question was asked in words and answered in objects.")

	// ---------------------------------------------------------------------
	section("8. What each layer is for")

	fmt.Println(scoreboard)
	return nil
}

const scoreboard = `                    vectors            graph              ontology
answers             "what is this      "what connects     "what may exist,
                     text about?"       to what?"          and be said?"
identity            a chunk id         a node id          a primary key
wrong answer        plausible and      a real path to     refused at the
looks like          unrelated          the wrong thing    write
degrades to         keyword overlap    an unusable        an unchecked
  without it         (still useful)     property bag       property bag

The combination is not three stores. It is one graph that can be reached by
meaning, walked by structure, and constrained by a schema — and the three
sections above are the three directions across those seams.`

// ------------------------------------------------------------------ helpers

func section(title string) { fmt.Printf("\n=== %s ===\n", title) }

// oneLine keeps an expected rejection readable next to the line that caused it.
func oneLine(err error) string {
	if err == nil {
		return "ACCEPTED — the schema did not object"
	}
	return "rejected: " + strings.Join(strings.Fields(err.Error()), " ")
}

func titles(resp *cortexdb.ObjectSetResolveResponse) []string {
	out := make([]string, 0, len(resp.Objects))
	for _, o := range resp.Objects {
		out = append(out, o.Title)
	}
	sort.Strings(out)
	return out
}

func typesIn(resp *cortexdb.ObjectSetResolveResponse) string {
	seen := map[string]int{}
	for _, o := range resp.Objects {
		seen[o.ObjectType]++
	}
	parts := make([]string, 0, len(seen))
	for name, n := range seen {
		parts = append(parts, fmt.Sprintf("%s×%d", name, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// rankOf says where a known document landed, or that it did not.
func rankOf(resp *cortexdb.KnowledgeSearchResponse, knowledgeID string) string {
	for i, hit := range resp.Results {
		if hit.KnowledgeID == knowledgeID {
			return fmt.Sprintf("#%d", i+1)
		}
	}
	return "outside the top " + strconv.Itoa(len(resp.Results))
}

func printHits(resp *cortexdb.KnowledgeSearchResponse) {
	if len(resp.Results) == 0 {
		fmt.Println("   (nothing)")
		return
	}
	for i, hit := range resp.Results {
		fmt.Printf("   %d. %-46s score %.3f\n", i+1, hit.Title, hit.Score)
	}
}

// objectsIn names the estate objects inside an expanded neighbourhood, which is
// how a passage becomes a list of things somebody has to go and look at.
func objectsIn(resp *cortexdb.ToolExpandGraphResponse) []string {
	estate := map[string]bool{"Node": true, "StoragePool": true, "Resource": true, "Volume": true}
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, n := range resp.Nodes {
		if !estate[n.NodeType] {
			continue
		}
		name := n.Content
		if name == "" {
			name = n.ID
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// idsOf turns the object names found in a neighbourhood back into node ids, so
// the walk can continue from them.
func idsOf(resp *cortexdb.ToolExpandGraphResponse, names []string) []string {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := make([]string, 0, len(names))
	for _, n := range resp.Nodes {
		if want[n.Content] {
			out = append(out, n.ID)
		}
	}
	return out
}

// objectIDs is the node id of every member of a resolved set — the handle the
// graph knows them by, as opposed to the title a person reads.
// chunkIDsOf is every chunk behind a set of retrieval hits — the graph nodes
// the mention edges leave from.
func chunkIDsOf(resp *cortexdb.KnowledgeSearchResponse) []string {
	out := make([]string, 0, 8)
	for _, hit := range resp.Results {
		out = append(out, hit.ChunkIDs...)
	}
	return out
}

func nodeIDsOfType(resp *cortexdb.ToolExpandGraphResponse, nodeType string) []string {
	out := make([]string, 0, 4)
	for _, n := range resp.Nodes {
		if n.NodeType == nodeType {
			out = append(out, n.ID)
		}
	}
	sort.Strings(out)
	return out
}

func titlesOfIDs(resp *cortexdb.ToolExpandGraphResponse, ids []string) []string {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make([]string, 0, len(ids))
	for _, n := range resp.Nodes {
		if want[n.ID] {
			out = append(out, n.Content)
		}
	}
	sort.Strings(out)
	return out
}

func objectIDs(resp *cortexdb.ObjectSetResolveResponse) []string {
	out := make([]string, 0, len(resp.Objects))
	for _, o := range resp.Objects {
		out = append(out, o.ObjectID)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 88 {
		s = s[:88] + "…"
	}
	return strings.TrimSpace(s)
}

func describeNeighbourhood(resp *cortexdb.ToolExpandGraphResponse) string {
	byType := map[string]int{}
	for _, n := range resp.Nodes {
		byType[n.NodeType]++
	}
	parts := make([]string, 0, len(byType))
	for name, n := range byType {
		parts = append(parts, fmt.Sprintf("%s×%d", name, n))
	}
	sort.Strings(parts)
	return fmt.Sprintf("%d nodes (%s) over %d edges",
		len(resp.Nodes), strings.Join(parts, " "), len(resp.Edges))
}

// embedderFromEnv wires a real embedding model when one is configured, and
// says plainly when it is not. Both paths run; only section 5 and seam (c)
// notice the difference, and noticing it is the point.
func embedderFromEnv() (cortexdb.Embedder, string) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, "LEXICAL (no OPENAI_API_KEY) — sections 5 and 7c show what that costs"
	}
	base := envOr("OPENAI_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")
	model := envOr("EMBED_MODEL", "text-embedding-v4")
	dim, err := strconv.Atoi(envOr("EMBED_DIM", "1024"))
	if err != nil || dim <= 0 {
		dim = 1024
	}
	return newOpenAIEmbedder(base, key, model, dim),
		fmt.Sprintf("VECTOR (%s, %d dimensions, via %s)", model, dim, base)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
