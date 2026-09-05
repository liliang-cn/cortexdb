package cortexdb

// Writing down a vocabulary that already exists.
//
// A store can hold four thousand nodes under a hundred and fifty node types,
// eight thousand edges under nearly three hundred edge types, and no ontology
// at all. The vocabulary is real — everything in there was typed by something
// — it was simply never written down. This derives a first draft of it.
//
// The temptation, and the reason this file is as long as it is, is to convert
// rather than to draft. A faithful conversion of such a store produces a
// schema that is true and useless:
//
//   - Half its objects are `entity`, which is not a type. It is this
//     codebase's own word for a write that named none — firstNonEmpty(
//     entity.Type, "entity") on the write path — so declaring it as an object
//     type writes down "we did not recognise this" as a thing in the world.
//   - `memory` and `document` are units of storage, not entities. Declaring
//     them describes the filing cabinet instead of what is in it.
//   - Its central relation is `mentions`, which is nine tenths of the edges
//     and is provenance: this record mentions that thing. `Memory —mentions→
//     Entity` is a true statement about the store and says nothing about the
//     world. Its runner-up is co-occurrence, which is a statistic.
//   - `tool` and `Tool`, `depends_on` and `dependsOn` come out as four things.
//     Those collisions are precisely what an ontology exists to resolve, and a
//     conversion reproduces them.
//
// So a draft is three things a caller must be able to tell apart: the schema,
// the reasoning that produced it, and the list of what a person has to decide.
// A deriver that hands back only a schema has thrown away the half that makes
// it reviewable, and a schema nobody can review is one somebody activates.
//
// Four rules run through all of it:
//
//   - The machine never merges. Two spellings may be two things.
//   - The machine never guesses cardinality. "Every A observed today has one
//     B" is a fact about today; alchemy raises CONFLICT_KIND_CARDINALITY
//     against at_most_one, so a guessed ONE turns ordinary later writes into
//     review items.
//   - Every verdict names the rule that produced it, and every rule is stated
//     in the report, so a person can overrule any of it.
//   - Nothing is saved. Saving is a decision, and this makes none.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The three buckets a node type can land in. They are never merged by the
// machine, because collapsing them is exactly the mistake: `entity` is not a
// small type, it is the absence of one, and `memory` is not a rare entity, it
// is a record. Both would look like ordinary object types after a merge, and
// the finding — that most of this store is unclassified — would be gone.
const (
	// OntologyBucketDomain is a candidate object type: something in the world.
	OntologyBucketDomain = "domain"
	// OntologyBucketBookkeeping is a record kind: a unit of storage the system
	// writes about its own contents.
	OntologyBucketBookkeeping = "bookkeeping"
	// OntologyBucketUnclassified is a type that says nothing — the extraction
	// fallback, or no type at all. On a real brain this is the headline
	// finding rather than a type.
	OntologyBucketUnclassified = "unclassified"
)

// The rules. Each is a name a finding can carry and a statement the report
// repeats, so that a verdict never arrives without the reasoning that produced
// it. Adding one means adding it to ontologyDraftRulebook below.
const (
	// OntologyDraftRuleOverride is the caller's own decision, recorded as
	// such so a report never presents a person's judgement as its own.
	OntologyDraftRuleOverride = "override:caller"
	// OntologyDraftRuleUntyped is a node with no type at all.
	OntologyDraftRuleUntyped = "unclassified:untyped"
	// OntologyDraftRuleFallbackType is a type name that means "nothing was
	// given" — the word a writer stamps when it recognised nothing.
	OntologyDraftRuleFallbackType = "unclassified:fallback-type"
	// OntologyDraftRuleWriterStamped is a record kind this library's own
	// storage writers stamp.
	OntologyDraftRuleWriterStamped = "bookkeeping:writer-stamped"
	// OntologyDraftRuleRecordShaped is a record kind nobody listed, recognised
	// by the shape a record has in a graph.
	OntologyDraftRuleRecordShaped = "bookkeeping:record-shaped"
	// OntologyDraftRuleProvenanceAttachment is an edge from a record to what
	// it holds, rather than between two things.
	OntologyDraftRuleProvenanceAttachment = "provenance:record-attachment"
	// OntologyDraftRuleCoOccurrence is an edge that reports two things having
	// been seen together — a statistic about the corpus.
	OntologyDraftRuleCoOccurrence = "provenance:co-occurrence"
	// OntologyDraftRuleFallbackEdge is the relation-side counterpart of
	// OntologyDraftRuleFallbackType.
	OntologyDraftRuleFallbackEdge = "provenance:fallback-type"
	// OntologyDraftRuleRemainder is what is left when no exclusion fired. It
	// is a rule and not a default: "nothing said otherwise" is the reasoning,
	// and a reader is entitled to see it stated.
	OntologyDraftRuleRemainder = "domain:remainder"

	// The withholding rules. A type these fire on keeps its bucket — the
	// verdict about what it is stands — and stays out of the schema, because
	// the schema cannot express it until somebody decides something.
	OntologyDraftRuleSpellingCollision = "withheld:spelling-collision"
	OntologyDraftRuleNotAnAPIName      = "withheld:not-an-api-name"
	OntologyDraftRuleBelowThreshold    = "withheld:below-threshold"
	OntologyDraftRuleEndsNotDeclared   = "withheld:ends-not-declared"
)

// The kinds of thing a person has to decide. Every one of them is a question
// the data cannot answer, which is why they are a list beside the draft rather
// than a value inside it.
const (
	OntologyDecisionMerge        = "merge_candidate"
	OntologyDecisionPrimaryKey   = "primary_key_guess"
	OntologyDecisionNoPrimaryKey = "no_primary_key_candidate"
	OntologyDecisionCardinality  = "cardinality_suspicion"
	OntologyDecisionRename       = "rename_required"
	OntologyDecisionLinkShape    = "link_shape_ambiguous"
	OntologyDecisionOrphanLink   = "relation_without_declared_ends"
)

// The vocabularies the naming rules match against, exported so a caller can
// read what the deriver believes before trusting what it says. Matching is on
// the folded name (lower case, separators removed), so `co_occurs`, `coOccurs`
// and `CO_OCCURS` are one entry rather than three.
var (
	// OntologyDraftFallbackNodeTypes are the words a writer stamps when it
	// recognised nothing. `entity` is not a guess: it is the literal default
	// this package's own write path applies.
	//
	// Deliberately short. Every entry here silently removes a type from the
	// draft, so a word that is generic in English and specific in somebody's
	// domain does not belong: `node` was in this list until a real brain
	// showed four cluster nodes and two Nodes being filed as "we recognised
	// nothing". A word earns a place here by meaning nothing anywhere.
	OntologyDraftFallbackNodeTypes = []string{"entity", "unknown", "untyped", "thing", "other", "misc", "unspecified"}
	// OntologyDraftFallbackEdgeTypes is the same on the relation side, where
	// `related_to` is this package's literal default.
	OntologyDraftFallbackEdgeTypes = []string{"related_to", "relatesto", "related", "linked_to", "links_to", "unknown", "unspecified"}
	// OntologyDraftRecordNodeTypes are the record kinds CortexDB's own writers
	// stamp — grep NodeType: through pkg/ and this is what comes back. A store
	// whose records are called something else is caught by the shape rule
	// instead, and a caller who disagrees overrules either.
	OntologyDraftRecordNodeTypes = []string{"chunk", "document", "memory", "message", "conversation", "session", "transcript", "summary", "episode", "record"}
	// OntologyDraftCoOccurrenceEdgeTypes name a statistic rather than an
	// assertion: that two things turned up in the same place.
	OntologyDraftCoOccurrenceEdgeTypes = []string{"co_occurs", "co_occurred", "co_occurrence", "cooccurs", "appears_with", "seen_with", "similar_to", "near"}
)

// The thresholds the two structural rules turn on, named so the report can
// state them and a reader can disagree with a number rather than with a
// verdict.
const (
	// ontologyDraftRecordInShare: a record is pointed at by nothing. Things in
	// the world are referred to; a note about them is not.
	ontologyDraftRecordInShare = 0.05
	// ontologyDraftRecordEdgeShare: a record has one way of pointing at what
	// it holds. A domain type participates in several relations.
	ontologyDraftRecordEdgeShare = 0.90
	// ontologyDraftRecordTargetTypes: and it points at many *kinds* of thing,
	// because what a record holds is not constrained — which is the signal
	// that separates a record kind from a leaf domain type that happens to sit
	// at the source of one relation.
	ontologyDraftRecordTargetTypes = 5
	// ontologyDraftRecordMinOut keeps the rule off types too small to have a
	// shape at all.
	ontologyDraftRecordMinOut = 2

	// ontologyDraftProvenanceFromShare: an edge type is record-attachment when
	// this share of it starts at a record kind. Not all of it, because one
	// hand-written edge of the same name does not make the other seven
	// thousand a relation.
	ontologyDraftProvenanceFromShare = 0.80

	// ontologyDraftKeyCoverage and ontologyDraftKeyDistinctness are what a
	// primary key candidate has to look like: on nearly every record of its
	// type, and nearly always different. Both are needed — coverage alone
	// finds classifiers like `type`, distinctness alone finds keys three
	// records happen to carry.
	ontologyDraftKeyCoverage     = 0.95
	ontologyDraftKeyDistinctness = 0.95
	// ontologyDraftKeyMinRecords is how many records distinctness needs to
	// mean anything. On a type with one node every key it carries is on all of
	// them and has one distinct value, so every key scores perfectly and the
	// winner is decided by whichever sorted first — a real brain's one-node
	// `Book` came back keyed on `description`. Below this, the honest answer
	// is that nothing was evidenced.
	ontologyDraftKeyMinRecords = 2
)

// ontologyDraftPlaceholderKey is the property a type gets when nothing in its
// records qualifies as an identity. The schema will not validate without a
// primary key, so the draft must emit one; naming it after the label every
// graph node already carries is the least invented option available, and it
// arrives with a no_primary_key_candidate decision saying so.
const ontologyDraftPlaceholderKey = "name"

// OntologyDraftRequest asks for a draft. Every field is a way for a person to
// overrule the deriver rather than to configure it.
type OntologyDraftRequest struct {
	// SchemaID names the draft. It is not saved under it — nothing here saves
	// — but a caller comparing two drafts, or running ontology_diff against a
	// stored schema, needs the id to be theirs to choose.
	SchemaID string `json:"schema_id,omitempty"`
	// MinNodes and MinEdges keep small types out of the *draft*. They never
	// keep anything out of the report: a threshold that silently dropped the
	// long tail would describe a fraction of the vocabulary while looking like
	// all of it.
	MinNodes int `json:"min_nodes,omitempty"`
	MinEdges int `json:"min_edges,omitempty"`
	// DomainTypes and BookkeepingTypes overrule the bucketing for named node
	// types. The report says the caller decided, not the deriver.
	DomainTypes      []string `json:"domain_types,omitempty"`
	BookkeepingTypes []string `json:"bookkeeping_types,omitempty"`
}

// OntologyDraftRule is one rule, stated. The report carries the whole rulebook
// beside the verdicts, because "bookkeeping" without a definition of
// bookkeeping is a verdict asking to be trusted.
type OntologyDraftRule struct {
	Name      string `json:"name"`
	AppliesTo string `json:"applies_to"`
	Statement string `json:"statement"`
}

// OntologyDraftTypeFinding is what the deriver concluded about one node type,
// with the counts it concluded it from.
type OntologyDraftTypeFinding struct {
	// Type is the spelling in the data. The empty string is the untyped.
	Type   string `json:"type"`
	Nodes  int    `json:"nodes"`
	Bucket string `json:"bucket"`
	// Rule is which rule assigned the bucket, and Why is what it saw.
	Rule string `json:"rule"`
	Why  string `json:"why"`
	// APIName is what the draft calls it, empty when the draft is silent
	// about it.
	APIName string `json:"api_name,omitempty"`
	// Withheld says why a domain type is nonetheless absent from the draft.
	// It is not a re-bucketing: the verdict about what the type *is* stands,
	// and the schema simply cannot express it until somebody decides
	// something.
	Withheld string `json:"withheld,omitempty"`
	// SkippedProperties are property keys on this type's records that cannot
	// be API names. Reported rather than rewritten: renaming somebody's field
	// is a decision.
	SkippedProperties []string `json:"skipped_properties,omitempty"`
}

// OntologyDraftLinkFinding is the same for one edge type, carrying the shapes
// the edges actually have — which is the evidence for a link type's two ends,
// and the only evidence there is.
type OntologyDraftLinkFinding struct {
	Type     string `json:"type"`
	Edges    int    `json:"edges"`
	Included bool   `json:"included"`
	Rule     string `json:"rule"`
	Why      string `json:"why"`
	// Shapes is every (from type, to type, count) this edge type was observed
	// in, capped so one promiscuous provenance edge cannot fill the report.
	Shapes []graph.EdgeShape `json:"shapes,omitempty"`
	// ShapesTotal is how many there were before the cap.
	ShapesTotal int `json:"shapes_total,omitempty"`
}

// ontologyDraftShapeLimit caps the shapes reported per edge type. On a real
// brain `mentions` runs between ninety-odd type pairs, and a report that
// listed them all would be mostly provenance — the thing the draft exists to
// push out of the way.
const ontologyDraftShapeLimit = 12

// OntologyDraftDecision is one question the data cannot answer.
type OntologyDraftDecision struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	// Detail is the question in a sentence; Evidence is what was observed, so
	// a person can decide without going back to the graph.
	Detail   string `json:"detail"`
	Evidence string `json:"evidence"`
}

// OntologyDraftSource is what was read, so a reader can tell a small brain
// from a partial read.
type OntologyDraftSource struct {
	Nodes         int            `json:"nodes"`
	Edges         int            `json:"edges"`
	NodeTypes     int            `json:"node_types"`
	EdgeTypes     int            `json:"edge_types"`
	Buckets       map[string]int `json:"buckets"`
	PropertyScope string         `json:"property_scope"`
	DerivedAt     time.Time      `json:"derived_at"`
}

// OntologyDraftReport is the reasoning: what was read, which rule fired on
// each type, and the rulebook itself.
type OntologyDraftReport struct {
	Source    OntologyDraftSource        `json:"source"`
	Rules     []OntologyDraftRule        `json:"rules"`
	NodeTypes []OntologyDraftTypeFinding `json:"node_types"`
	EdgeTypes []OntologyDraftLinkFinding `json:"edge_types"`
	Notes     []string                   `json:"notes"`
}

// OntologyDraftResponse is the three things a caller must tell apart.
type OntologyDraftResponse struct {
	Schema    OntologySchema          `json:"schema"`
	Report    OntologyDraftReport     `json:"report"`
	Decisions []OntologyDraftDecision `json:"to_decide"`
}

// ontologyDraftRulebook is the report's copy of the rules, with the thresholds
// spelled out where there are any.
func ontologyDraftRulebook() []OntologyDraftRule {
	return []OntologyDraftRule{
		{OntologyDraftRuleOverride, "node types",
			"the caller named this type in domain_types or bookkeeping_types; the deriver's own reading was not consulted"},
		{OntologyDraftRuleUntyped, "node types",
			"the node carries no type at all, so there is nothing to declare — a finding, not a type"},
		{OntologyDraftRuleFallbackType, "node types",
			fmt.Sprintf("the type name folds onto one of the words a writer stamps when it recognised nothing (%s); `entity` is this package's literal default on the write path",
				strings.Join(OntologyDraftFallbackNodeTypes, ", "))},
		{OntologyDraftRuleWriterStamped, "node types",
			fmt.Sprintf("the type names a unit of storage rather than a thing in the world (%s) — the record kinds CortexDB's own writers stamp",
				strings.Join(OntologyDraftRecordNodeTypes, ", "))},
		{OntologyDraftRuleRecordShaped, "node types",
			fmt.Sprintf("the type has the shape of a record and no name anybody listed: at most %.0f%% of the edges touching it arrive, at least %.0f%% of the edges it emits are one edge type, and that edge type reaches at least %d different kinds of thing — a relation is about two types, a record is about whatever it happened to hold",
				ontologyDraftRecordInShare*100, ontologyDraftRecordEdgeShare*100, ontologyDraftRecordTargetTypes)},
		{OntologyDraftRuleProvenanceAttachment, "edge types",
			fmt.Sprintf("at least %.0f%% of these edges start at a node type bucketed as bookkeeping: they attach a record to what it holds rather than relating two things, so they are how the store came to hold something and not an assertion about the world",
				ontologyDraftProvenanceFromShare*100)},
		{OntologyDraftRuleCoOccurrence, "edge types",
			fmt.Sprintf("the type name folds onto one of the words for having been seen together (%s), which is a statistic about the corpus rather than a claim about either end",
				strings.Join(OntologyDraftCoOccurrenceEdgeTypes, ", "))},
		{OntologyDraftRuleFallbackEdge, "edge types",
			fmt.Sprintf("the type name folds onto one of the words a writer stamps when no relation was given (%s); `related_to` is this package's literal default",
				strings.Join(OntologyDraftFallbackEdgeTypes, ", "))},
		{OntologyDraftRuleRemainder, "node types, edge types",
			"no exclusion applied. This is a rule and not a default: 'nothing said otherwise' is the whole of the reasoning, and it is worth seeing stated"},
		{OntologyDraftRuleSpellingCollision, "node types, edge types",
			"another spelling of this name carries more records. Two spellings may be two things, so nothing is merged: the draft describes the majority spelling and this one becomes a question"},
		{OntologyDraftRuleNotAnAPIName, "node types, edge types",
			"the name cannot be an API name ([A-Za-z][A-Za-z0-9_]*). Renaming somebody's type is a decision, so the draft asks rather than inventing a spelling"},
		{OntologyDraftRuleBelowThreshold, "node types, edge types",
			"fewer records than min_nodes/min_edges. Kept in this report, because a threshold that dropped the long tail silently would describe a fraction of the vocabulary while looking like all of it"},
		{OntologyDraftRuleEndsNotDeclared, "edge types",
			"no observed shape of this edge type has both ends in a declared object type. The relation is real; what it runs between is not yet named, and a link type needs two ends"},
	}
}

// DraftOntology reads the graph and proposes a first schema for it, with the
// reasoning and the open questions beside it.
//
// It writes nothing. The result goes to a person, who saves it — or a
// corrected version of it — through ontology_save.
func (db *DB) DraftOntology(ctx context.Context, req OntologyDraftRequest) (*OntologyDraftResponse, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	nodeCounts, err := db.graph.NodeTypeCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("draft ontology: %w", err)
	}
	edgeCounts, err := db.graph.EdgeTypeCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("draft ontology: %w", err)
	}
	shapes, err := db.graph.EdgeShapes(ctx)
	if err != nil {
		return nil, fmt.Errorf("draft ontology: %w", err)
	}
	keys, err := db.graph.NodePropertyKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("draft ontology: %w", err)
	}

	d := &ontologyDrafter{
		req:        req,
		nodeCounts: nodeCounts,
		edgeCounts: edgeCounts,
		shapes:     shapes,
		keys:       keys,
		findings:   map[string]*OntologyDraftTypeFinding{},
		links:      map[string]*OntologyDraftLinkFinding{},
	}
	d.bucketNodeTypes()
	d.chooseObjectTypes()
	d.classifyEdgeTypes()
	d.buildSchema()
	if err := d.raiseCardinalitySuspicions(ctx, db); err != nil {
		return nil, err
	}
	resp := d.finish()

	// The gate this whole file is written to pass. A draft that cannot be
	// saved is not a draft, and the failure has to be loud here rather than
	// half an hour later inside ontology_save, where the caller has no way to
	// tell a bad deriver from a bad edit of its output.
	if err := validateOntologySchema(resp.Schema); err != nil {
		return nil, fmt.Errorf("draft ontology produced a schema that does not validate — this is a bug in the deriver, not in the data: %w", err)
	}
	return resp, nil
}

type ontologyDrafter struct {
	req        OntologyDraftRequest
	nodeCounts map[string]int
	edgeCounts map[string]int
	shapes     []graph.EdgeShape
	keys       []graph.PropertyKeyUsage

	findings map[string]*OntologyDraftTypeFinding
	links    map[string]*OntologyDraftLinkFinding
	// objectTypes maps a node type as spelled in the data to the object type
	// the draft declares for it. A type absent here is one the draft is silent
	// about, whatever its bucket.
	objectTypes map[string]OntologyObjectType
	schema      OntologySchema
	decisions   []OntologyDraftDecision
}

// ontologyDraftFold is the name-collision key: case folded and separators
// removed, so `tool`/`Tool` and `depends_on`/`dependsOn`/`DEPENDS-ON` each
// fold to one group.
//
// Deliberately coarser than ontologyAPIKey, which only lowercases. That is the
// right rule for the schema — it decides what the store can actually hold two
// of — and the wrong one for this, whose job is to notice that a person should
// look. `depends_on` and `dependsOn` are two distinct API keys and one
// question.
func ontologyDraftFold(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch r {
		case '_', '-', ' ', '.':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ontologyDraftInVocabulary(name string, vocabulary []string) bool {
	folded := ontologyDraftFold(name)
	for _, entry := range vocabulary {
		if ontologyDraftFold(entry) == folded {
			return true
		}
	}
	return false
}

// nodeDegrees is what the structural record rule reads: how much arrives at a
// type, how much leaves it, by which edge type, and how many different kinds
// of thing each of those reaches.
type nodeDegrees struct {
	in            int
	out           int
	outByEdge     map[string]int
	targetsByEdge map[string]map[string]struct{}
}

func (d *ontologyDrafter) degrees() map[string]*nodeDegrees {
	out := map[string]*nodeDegrees{}
	at := func(t string) *nodeDegrees {
		if out[t] == nil {
			out[t] = &nodeDegrees{outByEdge: map[string]int{}, targetsByEdge: map[string]map[string]struct{}{}}
		}
		return out[t]
	}
	for _, s := range d.shapes {
		from, to := at(s.FromType), at(s.ToType)
		from.out += s.Count
		to.in += s.Count
		from.outByEdge[s.EdgeType] += s.Count
		if from.targetsByEdge[s.EdgeType] == nil {
			from.targetsByEdge[s.EdgeType] = map[string]struct{}{}
		}
		from.targetsByEdge[s.EdgeType][s.ToType] = struct{}{}
	}
	return out
}

// bucketNodeTypes runs the node-type rules, first match wins. The order is the
// design: a caller's decision outranks everything, an absent type outranks a
// name, and a name a writer stamps outranks a shape — because the shape rule
// is the one most likely to be wrong on a small type, and a listed record kind
// should not depend on having accumulated enough edges to look like one.
func (d *ontologyDrafter) bucketNodeTypes() {
	degrees := d.degrees()
	override := map[string]string{}
	for _, name := range d.req.BookkeepingTypes {
		override[ontologyAPIKey(name)] = OntologyBucketBookkeeping
	}
	for _, name := range d.req.DomainTypes {
		override[ontologyAPIKey(name)] = OntologyBucketDomain
	}

	for nodeType, count := range d.nodeCounts {
		f := &OntologyDraftTypeFinding{Type: nodeType, Nodes: count}
		switch {
		case override[ontologyAPIKey(nodeType)] != "":
			f.Bucket = override[ontologyAPIKey(nodeType)]
			f.Rule = OntologyDraftRuleOverride
			f.Why = "the caller placed it here"
		case strings.TrimSpace(nodeType) == "":
			f.Bucket = OntologyBucketUnclassified
			f.Rule = OntologyDraftRuleUntyped
			f.Why = fmt.Sprintf("%d nodes carry no type at all", count)
		case ontologyDraftInVocabulary(nodeType, OntologyDraftFallbackNodeTypes):
			f.Bucket = OntologyBucketUnclassified
			f.Rule = OntologyDraftRuleFallbackType
			f.Why = fmt.Sprintf("%q is what a writer stamps when it recognised nothing, so these %d nodes are untyped by another name", nodeType, count)
		case ontologyDraftInVocabulary(nodeType, OntologyDraftRecordNodeTypes):
			f.Bucket = OntologyBucketBookkeeping
			f.Rule = OntologyDraftRuleWriterStamped
			f.Why = fmt.Sprintf("%q is a unit of storage this library writes, not a thing in the world", nodeType)
		default:
			if why, ok := ontologyDraftRecordShape(nodeType, degrees[nodeType]); ok {
				f.Bucket = OntologyBucketBookkeeping
				f.Rule = OntologyDraftRuleRecordShaped
				f.Why = why
				break
			}
			f.Bucket = OntologyBucketDomain
			f.Rule = OntologyDraftRuleRemainder
			f.Why = fmt.Sprintf("%d nodes, and no rule said it was anything else", count)
		}
		d.findings[nodeType] = f
	}
}

// ontologyDraftRecordShape is the structural half of the bookkeeping rule: a
// record is written, points at whatever it happened to hold, and is pointed at
// by nothing.
//
// The third condition is what keeps it off a leaf domain type. A `service`
// that only ever appears at the source of `runs_on` satisfies the first two
// and is not a record; what separates them is that a relation is about two
// kinds of thing and a record is about whatever was in it.
func ontologyDraftRecordShape(nodeType string, deg *nodeDegrees) (string, bool) {
	if deg == nil || deg.out < ontologyDraftRecordMinOut {
		return "", false
	}
	total := deg.in + deg.out
	if total == 0 || float64(deg.in)/float64(total) > ontologyDraftRecordInShare {
		return "", false
	}
	topEdge, topCount := "", 0
	for edgeType, n := range deg.outByEdge {
		// Ties broken by name so two runs over one graph agree.
		if n > topCount || (n == topCount && edgeType < topEdge) {
			topEdge, topCount = edgeType, n
		}
	}
	if float64(topCount)/float64(deg.out) < ontologyDraftRecordEdgeShare {
		return "", false
	}
	targets := len(deg.targetsByEdge[topEdge])
	if targets < ontologyDraftRecordTargetTypes {
		return "", false
	}
	return fmt.Sprintf("%d edges leave %q and %d arrive; %d of those leaving are %q, which reaches %d different node types — the shape of a record, not of a relation",
		deg.out, nodeType, deg.in, topCount, topEdge, targets), true
}

// chooseObjectTypes decides which domain types the draft actually declares.
// Everything withheld here keeps its bucket and gains a question.
func (d *ontologyDrafter) chooseObjectTypes() {
	d.objectTypes = map[string]OntologyObjectType{}

	// Group by folded name so the collisions are visible before anything is
	// declared. Sorted throughout: these maps decide which spelling wins.
	groups := map[string][]string{}
	for nodeType, f := range d.findings {
		if f.Bucket != OntologyBucketDomain {
			continue
		}
		groups[ontologyDraftFold(nodeType)] = append(groups[ontologyDraftFold(nodeType)], nodeType)
	}

	keysByType := ontologyDraftKeysByType(d.keys)
	for _, fold := range sortedMapKeys(groups) {
		spellings := groups[fold]
		sort.Slice(spellings, func(i, j int) bool {
			if d.nodeCounts[spellings[i]] != d.nodeCounts[spellings[j]] {
				return d.nodeCounts[spellings[i]] > d.nodeCounts[spellings[j]]
			}
			return spellings[i] < spellings[j]
		})

		// The winner is decided before the API-name check, so that a group
		// whose majority spelling cannot be an API name asks about the
		// majority rather than quietly promoting a minority into the schema.
		winner := spellings[0]
		if len(spellings) > 1 {
			d.raiseMergeCandidate("node type", winner, spellings[1:])
			for _, loser := range spellings[1:] {
				d.findings[loser].Withheld = OntologyDraftRuleSpellingCollision
			}
		}

		f := d.findings[winner]
		if err := validateOntologyAPIName("object type", winner); err != nil {
			f.Withheld = OntologyDraftRuleNotAnAPIName
			d.decide(OntologyDecisionRename, winner,
				"this node type cannot be an API name, so the draft cannot declare it. Rename the type in the store, or decide what it should be called and add it by hand.",
				fmt.Sprintf("%d nodes; %v", f.Nodes, err))
			continue
		}
		if f.Nodes < d.req.MinNodes {
			f.Withheld = OntologyDraftRuleBelowThreshold
			continue
		}

		objectType, skipped := d.objectTypeFor(winner, f.Nodes, keysByType[winner])
		f.APIName = objectType.APIName
		f.SkippedProperties = skipped
		d.objectTypes[winner] = objectType
	}
}

func ontologyDraftKeysByType(keys []graph.PropertyKeyUsage) map[string][]graph.PropertyKeyUsage {
	out := map[string][]graph.PropertyKeyUsage{}
	for _, k := range keys {
		out[k.NodeType] = append(out[k.NodeType], k)
	}
	return out
}

// objectTypeFor turns one node type and the keys its records carry into a
// declared object type, and raises the primary key question that always
// accompanies one.
//
// Data types are not inferred. Every property is a string, because the values
// arrive as JSON text and reading `42` as an integer is the same class of
// mistake as reading two observations as a cardinality: it is a guess that the
// schema then enforces. A person retyping a property is doing one edit; a
// person discovering that a draft retyped forty of them is doing an
// investigation.
func (d *ontologyDrafter) objectTypeFor(nodeType string, nodes int, usage []graph.PropertyKeyUsage) (OntologyObjectType, []string) {
	properties := make([]OntologyProperty, 0, len(usage))
	skipped := make([]string, 0)
	seen := map[string]struct{}{}
	for _, u := range usage {
		// The knowledge contract's own keys say how well established a record
		// is, not what it is. They belong to contract.go and describing them
		// as characteristics of a host would be describing the wrong subject.
		if strings.HasPrefix(u.Key, "_") {
			continue
		}
		if err := validateOntologyAPIName("property", u.Key); err != nil {
			skipped = append(skipped, u.Key)
			continue
		}
		if _, dup := seen[ontologyAPIKey(u.Key)]; dup {
			// Two keys differing only in case cannot both be declared. Kept as
			// a skip rather than a silent drop for the same reason spellings
			// are: somebody has to decide they are the same field.
			skipped = append(skipped, u.Key)
			continue
		}
		seen[ontologyAPIKey(u.Key)] = struct{}{}
		properties = append(properties, OntologyProperty{
			APIName:     u.Key,
			Description: fmt.Sprintf("observed on %d of %d %q nodes with %d distinct values; type not inferred", u.Records, nodes, nodeType, u.Distinct),
			DataType:    OntologyDataType{Kind: OntologyDataString},
			Searchable:  true,
		})
	}

	primaryKey, evidence, confident := ontologyDraftPrimaryKey(nodeType, usage, nodes)
	if !confident {
		if _, exists := seen[ontologyAPIKey(ontologyDraftPlaceholderKey)]; !exists {
			properties = append(properties, OntologyProperty{
				APIName:     ontologyDraftPlaceholderKey,
				Description: "placeholder: no property key on these records is both near-universal and near-distinct, and the schema will not validate without a primary key",
				DataType:    OntologyDataType{Kind: OntologyDataString},
				Searchable:  true,
			})
			seen[ontologyAPIKey(ontologyDraftPlaceholderKey)] = struct{}{}
		}
		primaryKey = ontologyDraftPlaceholderKey
		d.decide(OntologyDecisionNoPrimaryKey, nodeType,
			fmt.Sprintf("nothing in these records identifies them, so the draft declares %q as a placeholder to make the schema valid. Decide what identifies one of these before saving.", ontologyDraftPlaceholderKey),
			evidence)
	} else {
		d.decide(OntologyDecisionPrimaryKey, nodeType,
			fmt.Sprintf("the draft guesses %q as the primary key. Nothing in the data can confirm it: a key is what a person promises will stay unique, and changing it later re-identifies every object.", primaryKey),
			evidence)
	}

	// Required on the primary key: the validator insists, and it is the one
	// place a required flag is not a guess about the data — it is what being
	// a key means.
	for i := range properties {
		if ontologyAPIKey(properties[i].APIName) == ontologyAPIKey(primaryKey) {
			properties[i].Required = true
		}
	}

	sort.Slice(properties, func(i, j int) bool { return properties[i].APIName < properties[j].APIName })
	sort.Strings(skipped)
	return OntologyObjectType{
		APIName: nodeType,
		Description: fmt.Sprintf("Drafted from %d nodes typed %q in this store. Nobody has signed this. The primary key %q is a guess.",
			nodes, nodeType, primaryKey),
		Status:     OntologyStatusExperimental,
		PrimaryKey: primaryKey,
		Properties: properties,
	}, skipped
}

// ontologyDraftPrimaryKey looks for a property key that could be an identity:
// on nearly every record of the type, and nearly always different.
//
// It returns its reasoning either way, because "nothing qualified" is as much
// a finding as a candidate is — and is the honest answer far more often than
// a deriver that had to produce something would like.
func ontologyDraftPrimaryKey(nodeType string, usage []graph.PropertyKeyUsage, nodes int) (key string, evidence string, ok bool) {
	if nodes == 0 {
		return "", "the type has no nodes to read", false
	}
	type candidate struct {
		key                    string
		coverage, distinctness float64
		records, distinct      int
	}
	var best *candidate
	var considered []string
	for _, u := range usage {
		if strings.HasPrefix(u.Key, "_") || validateOntologyAPIName("property", u.Key) != nil {
			continue
		}
		c := candidate{
			key:          u.Key,
			coverage:     float64(u.Records) / float64(nodes),
			distinctness: float64(u.Distinct) / float64(max(u.Records, 1)),
			records:      u.Records,
			distinct:     u.Distinct,
		}
		considered = append(considered,
			fmt.Sprintf("%s on %d/%d with %d distinct", u.Key, u.Records, nodes, u.Distinct))
		if c.records < ontologyDraftKeyMinRecords ||
			c.coverage < ontologyDraftKeyCoverage || c.distinctness < ontologyDraftKeyDistinctness {
			continue
		}
		// Ties broken toward `name` and then alphabetically. Two equally
		// evidenced keys need something to separate them, and alphabetical
		// order is arbitrary where "the key this store's own writers use for a
		// node's label" is at least a reason.
		if best == nil || c.coverage > best.coverage ||
			(c.coverage == best.coverage && c.distinctness > best.distinctness) ||
			(c.coverage == best.coverage && c.distinctness == best.distinctness &&
				ontologyDraftKeyRank(c.key) < ontologyDraftKeyRank(best.key)) {
			chosen := c
			best = &chosen
		}
	}
	sort.Strings(considered)
	seen := "no property keys at all"
	if len(considered) > 0 {
		seen = strings.Join(considered, "; ")
	}
	if best == nil {
		if nodes < ontologyDraftKeyMinRecords {
			return "", fmt.Sprintf("%q has %d node, so nothing about it is evidence: every key it carries is on all of them and has one distinct value. Keys seen: %s",
				nodeType, nodes, seen), false
		}
		return "", fmt.Sprintf("no key is on at least %.0f%% of the %d %q records with at least %.0f%% distinct values. Keys seen: %s",
			ontologyDraftKeyCoverage*100, nodes, nodeType, ontologyDraftKeyDistinctness*100, seen), false
	}
	return best.key, fmt.Sprintf("%s is on %d of %d %q nodes with %d distinct values (coverage %.0f%%, distinctness %.0f%%). Keys seen: %s",
		best.key, best.records, nodes, nodeType, best.distinct, best.coverage*100, best.distinctness*100, seen), true
}

// ontologyDraftKeyRank orders equally evidenced candidates. Everything ties at
// 1 except the conventional label key, so this is a tie-break and never a
// preference: a key that scores worse cannot win on its name.
func ontologyDraftKeyRank(key string) string {
	if ontologyAPIKey(key) == ontologyDraftPlaceholderKey {
		return "0" + key
	}
	return "1" + key
}

// classifyEdgeTypes decides which edge types become link types. The order is
// the design again: what a name says about itself first, then what the ends
// say about it, then what the schema can express.
func (d *ontologyDrafter) classifyEdgeTypes() {
	shapesByType := map[string][]graph.EdgeShape{}
	for _, s := range d.shapes {
		shapesByType[s.EdgeType] = append(shapesByType[s.EdgeType], s)
	}

	// Spelling groups, decided before anything is classified so that the
	// majority spelling is the one the rest of the rules are asked about.
	groups := map[string][]string{}
	for edgeType := range d.edgeCounts {
		groups[ontologyDraftFold(edgeType)] = append(groups[ontologyDraftFold(edgeType)], edgeType)
	}
	minority := map[string]struct{}{}
	for _, fold := range sortedMapKeys(groups) {
		spellings := groups[fold]
		if len(spellings) < 2 {
			continue
		}
		sort.Slice(spellings, func(i, j int) bool {
			if d.edgeCounts[spellings[i]] != d.edgeCounts[spellings[j]] {
				return d.edgeCounts[spellings[i]] > d.edgeCounts[spellings[j]]
			}
			return spellings[i] < spellings[j]
		})
		d.raiseMergeCandidate("edge type", spellings[0], spellings[1:])
		for _, loser := range spellings[1:] {
			minority[loser] = struct{}{}
		}
	}

	for edgeType, count := range d.edgeCounts {
		shapes := shapesByType[edgeType]
		f := &OntologyDraftLinkFinding{Type: edgeType, Edges: count, ShapesTotal: len(shapes)}
		f.Shapes = ontologyDraftTopShapes(shapes)
		d.links[edgeType] = f

		switch {
		case strings.TrimSpace(edgeType) == "":
			f.Rule = OntologyDraftRuleFallbackEdge
			f.Why = fmt.Sprintf("%d edges carry no type at all", count)
		case ontologyDraftInVocabulary(edgeType, OntologyDraftFallbackEdgeTypes):
			f.Rule = OntologyDraftRuleFallbackEdge
			f.Why = fmt.Sprintf("%q is what a writer stamps when no relation was given, so these %d edges assert only that something was linked", edgeType, count)
		case ontologyDraftInVocabulary(edgeType, OntologyDraftCoOccurrenceEdgeTypes):
			f.Rule = OntologyDraftRuleCoOccurrence
			f.Why = fmt.Sprintf("%q reports that two things were seen together — a statistic about the corpus, not a claim about either end (%d edges)", edgeType, count)
		default:
			if why, ok := d.provenanceAttachment(shapes, count); ok {
				f.Rule = OntologyDraftRuleProvenanceAttachment
				f.Why = why
				break
			}
			if _, isMinority := minority[edgeType]; isMinority {
				f.Rule = OntologyDraftRuleSpellingCollision
				f.Why = fmt.Sprintf("another spelling of %q carries more edges; the draft describes that one and asks about this", edgeType)
				break
			}
			if err := validateOntologyAPIName("link type", edgeType); err != nil {
				f.Rule = OntologyDraftRuleNotAnAPIName
				f.Why = err.Error()
				d.decide(OntologyDecisionRename, edgeType,
					"this edge type cannot be an API name, so the draft cannot declare it. Rename it in the store, or decide what it should be called.",
					fmt.Sprintf("%d edges; %v", count, err))
				break
			}
			if count < d.req.MinEdges {
				f.Rule = OntologyDraftRuleBelowThreshold
				f.Why = fmt.Sprintf("%d edges, under min_edges=%d", count, d.req.MinEdges)
				break
			}
			declared := d.declarableShapes(shapes)
			if len(declared) == 0 {
				f.Rule = OntologyDraftRuleEndsNotDeclared
				f.Why = fmt.Sprintf("%d edges, and no shape of them has both ends in a declared object type", count)
				d.decide(OntologyDecisionOrphanLink, edgeType,
					"this looks like a real relation and cannot be declared: a link type needs two declared ends. Classify the node types at its ends first, then re-draft.",
					ontologyDraftShapeEvidence(shapes))
				break
			}
			f.Included = true
			f.Rule = OntologyDraftRuleRemainder
			f.Why = fmt.Sprintf("%d edges, %d of which run between declared object types", count, ontologyDraftShapeCount(declared))
		}
	}
}

// provenanceAttachment is the structural exclusion: an edge that starts at a
// record is how the store came to hold something, not something the store
// holds. It is derived from the buckets rather than from names, so overruling
// a node type's bucket moves every edge that leaves it.
func (d *ontologyDrafter) provenanceAttachment(shapes []graph.EdgeShape, count int) (string, bool) {
	if count == 0 {
		return "", false
	}
	fromBookkeeping := 0
	sources := map[string]int{}
	for _, s := range shapes {
		if f, ok := d.findings[s.FromType]; ok && f.Bucket == OntologyBucketBookkeeping {
			fromBookkeeping += s.Count
			sources[s.FromType] += s.Count
		}
	}
	if float64(fromBookkeeping)/float64(count) < ontologyDraftProvenanceFromShare {
		return "", false
	}
	named := make([]string, 0, len(sources))
	for _, t := range sortedMapKeys(sources) {
		named = append(named, fmt.Sprintf("%s (%d)", t, sources[t]))
	}
	return fmt.Sprintf("%d of %d edges start at a record kind — %s — so they attach a record to what it holds rather than relating two things",
		fromBookkeeping, count, strings.Join(named, ", ")), true
}

// declarableShapes keeps the shapes whose ends the draft actually declares,
// best first.
func (d *ontologyDrafter) declarableShapes(shapes []graph.EdgeShape) []graph.EdgeShape {
	out := make([]graph.EdgeShape, 0, len(shapes))
	for _, s := range shapes {
		if _, ok := d.objectTypes[s.FromType]; !ok {
			continue
		}
		if _, ok := d.objectTypes[s.ToType]; !ok {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].FromType != out[j].FromType {
			return out[i].FromType < out[j].FromType
		}
		return out[i].ToType < out[j].ToType
	})
	return out
}

func ontologyDraftShapeCount(shapes []graph.EdgeShape) int {
	n := 0
	for _, s := range shapes {
		n += s.Count
	}
	return n
}

func ontologyDraftTopShapes(shapes []graph.EdgeShape) []graph.EdgeShape {
	sorted := append([]graph.EdgeShape(nil), shapes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Count != sorted[j].Count {
			return sorted[i].Count > sorted[j].Count
		}
		if sorted[i].FromType != sorted[j].FromType {
			return sorted[i].FromType < sorted[j].FromType
		}
		return sorted[i].ToType < sorted[j].ToType
	})
	if len(sorted) > ontologyDraftShapeLimit {
		sorted = sorted[:ontologyDraftShapeLimit]
	}
	return sorted
}

func ontologyDraftShapeEvidence(shapes []graph.EdgeShape) string {
	parts := make([]string, 0, ontologyDraftShapeLimit)
	for _, s := range ontologyDraftTopShapes(shapes) {
		parts = append(parts, fmt.Sprintf("%s -> %s (%d)", ontologyDraftTypeLabel(s.FromType), ontologyDraftTypeLabel(s.ToType), s.Count))
	}
	if len(parts) == 0 {
		return "no shapes observed"
	}
	return "observed shapes: " + strings.Join(parts, ", ")
}

// ontologyDraftTypeLabel names the untyped in a sentence. The empty string
// reads as a missing word rather than as a finding.
func ontologyDraftTypeLabel(nodeType string) string {
	if strings.TrimSpace(nodeType) == "" {
		return "(untyped)"
	}
	return nodeType
}

// buildSchema assembles the draft from what the two classification passes
// decided.
func (d *ontologyDrafter) buildSchema() {
	schemaID := strings.TrimSpace(d.req.SchemaID)
	if schemaID == "" {
		schemaID = "draft"
	}
	now := time.Now().UTC()
	d.schema = OntologySchema{
		SchemaID:    schemaID,
		Name:        "Draft derived from the graph",
		Description: "A first schema written from what this store already contains. Nobody has signed it: every type is experimental, every primary key is a guess, and every cardinality is MANY because cardinality is not in the data. Read the report and the to-decide list before saving.",
		Version:     1,
		// Never active, and never strict. A strict schema derived from the
		// data rejects nothing by construction — it describes the data — and
		// then refuses the first genuinely new fact for being new.
		Active:      false,
		Enforcement: OntologyEnforcementVocabulary,
		Metadata: map[string]string{
			"derived_by": "ontology_draft",
			"derived_at": now.Format(time.RFC3339),
			"signed_off": "no",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	for _, nodeType := range sortedMapKeys(d.objectTypes) {
		d.schema.ObjectTypes = append(d.schema.ObjectTypes, d.objectTypes[nodeType])
	}

	shapesByType := map[string][]graph.EdgeShape{}
	for _, s := range d.shapes {
		shapesByType[s.EdgeType] = append(shapesByType[s.EdgeType], s)
	}
	for _, edgeType := range sortedMapKeys(d.links) {
		f := d.links[edgeType]
		if !f.Included {
			continue
		}
		declared := d.declarableShapes(shapesByType[edgeType])
		dominant := declared[0]
		if len(declared) > 1 {
			d.decide(OntologyDecisionLinkShape, edgeType,
				fmt.Sprintf("a link type has two ends and this relation was observed between %d different pairs of declared types. The draft takes the commonest, %s -> %s. Declare an interface over the others, or split the relation.",
					len(declared), dominant.FromType, dominant.ToType),
				ontologyDraftShapeEvidence(declared))
		}
		d.schema.LinkTypes = append(d.schema.LinkTypes, OntologyLinkType{
			APIName: edgeType,
			Description: fmt.Sprintf("Drafted from %d edges typed %q, %d of them between %s and %s. Both sides are MANY because nothing in the data says otherwise.",
				f.Edges, edgeType, dominant.Count, dominant.FromType, dominant.ToType),
			Status: OntologyStatusExperimental,
			// Side names are derived from the link type so they cannot collide
			// on an object type that several link types touch — which the
			// validator refuses, and which a draft of a hundred relations
			// would otherwise hit constantly.
			A: OntologyLinkSide{
				APIName:           edgeType + "_from",
				ObjectTypeAPIName: dominant.FromType,
				Cardinality:       OntologyCardinalityMany,
			},
			B: OntologyLinkSide{
				APIName:           edgeType + "_to",
				ObjectTypeAPIName: dominant.ToType,
				Cardinality:       OntologyCardinalityMany,
			},
		})
	}
}

// raiseCardinalitySuspicions reports the one-ness the data happens to show,
// without acting on it.
//
// This is the rule that costs the most to get wrong. Every relation in the
// draft is MANY/MANY; if a side really is ONE, a person tightening it later
// makes one edit. If a deriver guesses ONE and is wrong, alchemy raises
// CONFLICT_KIND_CARDINALITY against at_most_one on every ordinary write that
// contradicts it, and a working ingest turns into a review queue.
func (d *ontologyDrafter) raiseCardinalitySuspicions(ctx context.Context, db *DB) error {
	included := make([]string, 0, len(d.schema.LinkTypes))
	sides := map[string]OntologyLinkType{}
	for _, l := range d.schema.LinkTypes {
		included = append(included, l.APIName)
		sides[l.APIName] = l
	}
	if len(included) == 0 {
		return nil
	}
	// Only the included types: this scans one row per distinct (type, from,
	// to) triple, and asking it about provenance would scan the part of the
	// graph the draft exists to leave out.
	pairs, err := db.graph.EdgeEndpointPairs(ctx, included...)
	if err != nil {
		return fmt.Errorf("draft ontology: %w", err)
	}

	type fanout struct {
		fromDegree map[string]int
		toDegree   map[string]int
	}
	byType := map[string]*fanout{}
	for _, p := range pairs {
		link, ok := sides[p.EdgeType]
		if !ok {
			continue
		}
		// Restricted to the shape the draft actually declared. Counting the
		// other shapes would answer a question about a relation the schema
		// does not describe.
		if p.From.NodeType != link.A.ObjectTypeAPIName || p.To.NodeType != link.B.ObjectTypeAPIName {
			continue
		}
		if byType[p.EdgeType] == nil {
			byType[p.EdgeType] = &fanout{fromDegree: map[string]int{}, toDegree: map[string]int{}}
		}
		byType[p.EdgeType].fromDegree[p.From.ID] += p.Count
		byType[p.EdgeType].toDegree[p.To.ID] += p.Count
	}

	for _, edgeType := range sortedMapKeys(byType) {
		f := byType[edgeType]
		link := sides[edgeType]
		for _, side := range []struct {
			name    string
			owner   string
			degrees map[string]int
		}{
			{link.B.APIName, link.B.ObjectTypeAPIName, f.toDegree},
			{link.A.APIName, link.A.ObjectTypeAPIName, f.fromDegree},
		} {
			// Two nodes is the floor for the observation to be about anything:
			// one node with one edge is not evidence of a pattern.
			if len(side.degrees) < 2 {
				continue
			}
			maxDegree := 0
			for _, n := range side.degrees {
				if n > maxDegree {
					maxDegree = n
				}
			}
			if maxDegree != 1 {
				continue
			}
			d.decide(OntologyDecisionCardinality, edgeType+"."+side.name,
				fmt.Sprintf("no %s carries more than one %q edge today, which looks like ONE and is not evidence of it. The draft says MANY. Tighten it only if a person promises it: alchemy raises CONFLICT_KIND_CARDINALITY against at_most_one, so a wrong ONE turns ordinary later writes into review items.",
					side.owner, edgeType),
				fmt.Sprintf("%d %s nodes carry this edge, the busiest of them one", len(side.degrees), side.owner))
		}
	}
	return nil
}

func (d *ontologyDrafter) raiseMergeCandidate(kind string, winner string, losers []string) {
	counts := d.nodeCounts
	unit := "nodes"
	if kind == "edge type" {
		counts = d.edgeCounts
		unit = "edges"
	}
	parts := make([]string, 0, len(losers)+1)
	parts = append(parts, fmt.Sprintf("%s (%d %s)", winner, counts[winner], unit))
	for _, loser := range losers {
		parts = append(parts, fmt.Sprintf("%s (%d %s)", loser, counts[loser], unit))
	}
	d.decide(OntologyDecisionMerge, winner,
		fmt.Sprintf("%s %s appears under %d spellings. Nothing was merged: two spellings may be two things. The draft describes %q and is silent about the rest — if they are the same thing, add the others as aliases; if they are not, declare them separately.",
			kind, winner, len(losers)+1, winner),
		strings.Join(parts, ", "))
}

func (d *ontologyDrafter) decide(kind, target, detail, evidence string) {
	d.decisions = append(d.decisions, OntologyDraftDecision{
		Kind: kind, Target: target, Detail: detail, Evidence: evidence,
	})
}

func (d *ontologyDrafter) finish() *OntologyDraftResponse {
	buckets := map[string]int{
		OntologyBucketDomain:       0,
		OntologyBucketBookkeeping:  0,
		OntologyBucketUnclassified: 0,
	}
	nodes, edges := 0, 0
	findings := make([]OntologyDraftTypeFinding, 0, len(d.findings))
	for _, nodeType := range sortedMapKeys(d.findings) {
		f := d.findings[nodeType]
		buckets[f.Bucket] += f.Nodes
		nodes += f.Nodes
		findings = append(findings, *f)
	}
	links := make([]OntologyDraftLinkFinding, 0, len(d.links))
	for _, edgeType := range sortedMapKeys(d.links) {
		links = append(links, *d.links[edgeType])
		edges += d.links[edgeType].Edges
	}

	// Ordered so that two drafts of one brain can be diffed. Everything above
	// this is built out of Go maps.
	sort.SliceStable(d.decisions, func(i, j int) bool {
		if d.decisions[i].Kind != d.decisions[j].Kind {
			return d.decisions[i].Kind < d.decisions[j].Kind
		}
		return d.decisions[i].Target < d.decisions[j].Target
	})

	notes := []string{
		"Nothing here was saved. Read it, correct it, then save it with ontology_save.",
		"Property data types are not inferred: every property is a string. Reading `42` as an integer is a guess the schema would then enforce.",
		"Property keys beginning with _ are the knowledge contract's own keys — how well established a record is, not what it is — and are not declared as properties.",
		fmt.Sprintf("%d of the %d nodes are unclassified and %d are bookkeeping; the draft describes the %d that are neither.",
			buckets[OntologyBucketUnclassified], nodes, buckets[OntologyBucketBookkeeping], buckets[OntologyBucketDomain]),
	}

	return &OntologyDraftResponse{
		Schema: d.schema,
		Report: OntologyDraftReport{
			Source: OntologyDraftSource{
				Nodes:         nodes,
				Edges:         edges,
				NodeTypes:     len(findings),
				EdgeTypes:     len(links),
				Buckets:       buckets,
				PropertyScope: "every property key on every node, counted whole through graph.NodePropertyKeys — not a sample",
				DerivedAt:     d.schema.UpdatedAt,
			},
			Rules:     ontologyDraftRulebook(),
			NodeTypes: findings,
			EdgeTypes: links,
			Notes:     notes,
		},
		Decisions: d.decisions,
	}
}
