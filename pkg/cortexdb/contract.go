package cortexdb

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The knowledge contract: what a record must carry, in its metadata, to be
// trusted and explained. Normative text is docs/superpowers/specs/
// 2026-09-05-knowledge-contract-design.md; this file is its executable form.
//
// Every producer that writes into CortexDB — alchemy, argus, DataIntelligence,
// the memory tools — answers the same two questions about each record: how do
// I know this, and how sure am I. Before this file each answered in its own
// words. These constants are the one vocabulary; ValidateContract is what a
// producer calls before it writes.
//
// The keys ride in the metadata maps the store already has. Nothing here
// changes an RPC. The `_` prefix is alchemy's convention, kept: everything
// under it is what a producer knows about a record; everything else is what
// the source said, verbatim.

const (
	// ContractPrefix separates contract keys from the source's own attributes.
	ContractPrefix = "_"

	// KeySource is where the record came from: a URL, a file, a job, an
	// engagement or database name. Never a DSN, never a path with credentials
	// in it — this store is shared, and a source string is read by everyone
	// who can read the record.
	KeySource = ContractPrefix + "source"
	// KeyChunk is the chunk index within the source, or -1 when the producer
	// did not work in chunks (DDL, graph import, a measurement).
	KeyChunk = ContractPrefix + "chunk"
	// KeyProducer is how the record was made. Values are the Producer*
	// constants below.
	KeyProducer = ContractPrefix + "producer"
	// KeyGrade is by what kind of thing the record's truth is established.
	// Values are the Grade* constants below.
	KeyGrade = ContractPrefix + "grade"
	// KeyState is the producer's own word for where the record stands,
	// verbatim. It is displayed as detail and never interpreted across
	// producers — that is what keeps KeyGrade from flattening anything.
	KeyState = ContractPrefix + "state"
	// KeyAt is when the record was produced, RFC 3339: when a measurement was
	// taken, when a claim was published, when a person asserted. A writer that
	// has no producer-side time (alchemy stamps none on an extraction, because
	// its results are content-addressed and a clock would change every
	// address) writes the moment it put the record on the shelf. That is a
	// true statement about the record, and _run/_source point back to the job.
	KeyAt = ContractPrefix + "at"
	// KeyBy is the named person who asserted this record into the graph. Not
	// the speaker a report quotes — that is what a claim is about, and it
	// belongs in the graph as an edge (argus: attributed_to) where it can be
	// queried; folding it in here would make "who put this here" and "who the
	// report says said it" one field.
	KeyBy = ContractPrefix + "by"
	// KeyWhy is the reason a record is held or refused, in words a person can
	// act on. A refusal without a reason is noise the reader will delete.
	KeyWhy = ContractPrefix + "why"
	// KeyContradicts is a JSON array of record ids this record cannot
	// both-be-true with. Written on both records by whoever detects it. The
	// disagreement is information, not an error, and both records stay.
	KeyContradicts = ContractPrefix + "contradicts"
	// KeyConfidence is an extraction confidence in [0,1]. It is never a
	// substitute for KeyGrade: a model's confidence in its own output is not
	// evidence about the world.
	KeyConfidence = ContractPrefix + "confidence"
)

// Producer values. The first five are the strings alchemy's Producer type
// already puts on the wire (pkg/alchemy/types.go: "ddl", "graph-import", …)
// and its CortexDB connector already writes under _producer — not the proto
// enum names, which never leave the RPC layer. The contract ratifies what is
// written, so a record alchemy stored last month validates today. Two more
// for a producer whose output is a governed number rather than an extraction.
const (
	ProducerDDL         = "ddl"
	ProducerGraphImport = "graph-import"
	ProducerTabular     = "tabular"
	ProducerLLMExtract  = "llm-extract"
	ProducerHuman       = "human"
	// ProducerMeasured is a value obtained by running a governed query against
	// a system of record. The value itself does not travel — see the hard rule
	// in the spec — only the judgement about it does.
	ProducerMeasured = "measured"
	// ProducerCompiled is derived deterministically from a declared model: a
	// metric definition, a schema.
	ProducerCompiled = "compiled"
)

// Grade values: by what kind of thing a record's truth is established.
//
// Five, and deliberately not a ladder that the producers' own verdicts map
// onto one-to-one. di-consult's Met/Short/Missed are outcomes of an
// acceptance; di-anchor's Anchored/Ambiguous are how a figure was pinned;
// alchemy's confidence-plus-review is about an extraction. One axis for all
// three would flatten exactly the distinctions each of them documents at
// length. So Grade answers one narrow question and the finer word goes in
// KeyState untouched.
const (
	// GradeVerified: established by something outside the producer — a
	// re-measurement, an external published figure, a named person's review.
	GradeVerified = "verified"
	// GradeSelfConsistent: internal coherence only. Derived deterministically
	// from something already stated; not checked against the world.
	GradeSelfConsistent = "self_consistent"
	// GradeAsserted: a source or a model said so and nothing has checked it.
	// Every claim is asserted by construction, and stays so after review —
	// reviewing a claim confirms the outlet said it, not that it is true.
	GradeAsserted = "asserted"
	// GradeHeld: nothing yet; a person has to look. Requires KeyWhy.
	GradeHeld = "held"
	// GradeRefused: the producer declined to produce it and can say why.
	// Requires KeyWhy. A refusal is a record, not an absence — the reader must
	// be able to tell "we have no precedent" from "we refused to form one".
	GradeRefused = "refused"
)

var (
	contractProducers = map[string]bool{
		ProducerDDL: true, ProducerGraphImport: true, ProducerTabular: true,
		ProducerLLMExtract: true, ProducerHuman: true,
		ProducerMeasured: true, ProducerCompiled: true,
	}
	contractGrades = map[string]bool{
		GradeVerified: true, GradeSelfConsistent: true, GradeAsserted: true,
		GradeHeld: true, GradeRefused: true,
	}
)

// ContractError lists every way a metadata map fails the contract, so a
// producer fixes them in one pass rather than one per write attempt.
type ContractError struct {
	Problems []string
}

func (e *ContractError) Error() string {
	return "knowledge contract: " + strings.Join(e.Problems, "; ")
}

// ValidateContract checks a record's metadata against the knowledge contract.
//
// It returns nil when the record carries what it must, or a *ContractError
// naming each problem. It does not reject keys it does not know: everything
// outside the `_` prefix is the source's own attribute, and contract keys it
// does not validate (alchemy's _model, _ontology, …) are passed through.
//
// The server does not call this. A producer does, before it writes; the store
// keeps what it is given. Enforcement at the door is a later decision, once
// the producers write conformant records and the rejections are known.
func ValidateContract(meta map[string]string) error {
	var problems []string
	need := func(key string) (string, bool) {
		v, ok := meta[key]
		if !ok || strings.TrimSpace(v) == "" {
			problems = append(problems, key+" is required")
			return "", false
		}
		return v, true
	}

	if src, ok := need(KeySource); ok && looksLikeCredential(src) {
		problems = append(problems, KeySource+" must not carry credentials (a DSN or a URL with a password)")
	}

	producer, hasProducer := need(KeyProducer)
	if hasProducer && !contractProducers[producer] {
		problems = append(problems, fmt.Sprintf("%s %q is not one of %s", KeyProducer, producer, contractValues(contractProducers)))
	}

	grade, hasGrade := need(KeyGrade)
	if hasGrade && !contractGrades[grade] {
		problems = append(problems, fmt.Sprintf("%s %q is not one of %s", KeyGrade, grade, contractValues(contractGrades)))
	}

	if at, ok := need(KeyAt); ok {
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			problems = append(problems, KeyAt+" must be RFC 3339: "+err.Error())
		}
	}

	// A held or refused record without a reason is the thing this contract
	// exists to make impossible: the reader cannot act on it and will delete it.
	if hasGrade && (grade == GradeHeld || grade == GradeRefused) {
		if strings.TrimSpace(meta[KeyWhy]) == "" {
			problems = append(problems, fmt.Sprintf("%s is required when %s is %q", KeyWhy, KeyGrade, grade))
		}
	}
	// A human assertion with no human is not a human assertion.
	if hasProducer && producer == ProducerHuman && strings.TrimSpace(meta[KeyBy]) == "" {
		problems = append(problems, fmt.Sprintf("%s is required when %s is %s", KeyBy, KeyProducer, ProducerHuman))
	}

	if v, ok := meta[KeyChunk]; ok {
		if _, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			problems = append(problems, KeyChunk+" must be an integer (-1 when not chunked)")
		}
	}
	if v, ok := meta[KeyConfidence]; ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f < 0 || f > 1 {
			problems = append(problems, KeyConfidence+" must be a number in [0,1]")
		}
	}
	if v, ok := meta[KeyContradicts]; ok {
		var ids []string
		if err := json.Unmarshal([]byte(v), &ids); err != nil {
			problems = append(problems, KeyContradicts+" must be a JSON array of record ids")
		} else {
			for _, id := range ids {
				if strings.TrimSpace(id) == "" {
					problems = append(problems, KeyContradicts+" must not contain an empty id")
					break
				}
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return &ContractError{Problems: problems}
}

// looksLikeCredential catches the two shapes a secret takes in a source
// string: a URL with user:password@ and a key=value DSN with a password.
// It is a guard against the obvious, not a scanner.
func looksLikeCredential(s string) bool {
	low := strings.ToLower(s)
	if strings.Contains(low, "password=") || strings.Contains(low, "pwd=") {
		return true
	}
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if at := strings.IndexByte(rest, '@'); at >= 0 && strings.IndexByte(rest[:at], ':') >= 0 {
			return true
		}
	}
	return false
}

func contractValues(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
