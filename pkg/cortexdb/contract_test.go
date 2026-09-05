package cortexdb

import (
	"strings"
	"testing"
)

// The three producers, each in the shape it writes today plus the contract's
// five keys. If any of these fails to validate, the vocabulary does not hold
// and the spec is wrong, not the producer.
func TestContractAcceptsEachProducersRealShape(t *testing.T) {
	cases := map[string]map[string]string{
		// alchemy: an LLM-extracted edge, reviewed and accepted. Every key it
		// writes today is present and passes through untouched.
		"alchemy reviewed edge": {
			KeySource: "s3://corpus/annual-report-2025.pdf", KeyChunk: "17",
			KeyProducer: ProducerLLMExtract, KeyGrade: GradeVerified,
			KeyState: "accept", KeyAt: "2026-09-03T10:12:00Z",
			KeyConfidence: "0.82",
			"_model":      "gemini-3.6-flash", "_ontology": "world@3", "_chunking": "whole",
			"_reviewed_by": "liang", "_run": "job-4f2c",
			"company": "Reuters", // not under `_`: the source's own attribute
		},
		// argus: a claim. Asserted by construction; the speaker is _by, the
		// outlet is _source, and it contradicts another claim that also stays.
		"argus claim": {
			KeySource: "reuters.com", KeyChunk: "-1",
			KeyProducer: ProducerLLMExtract, KeyGrade: GradeAsserted,
			KeyAt: "2026-09-04T06:30:00Z", KeyBy: "the Defense Ministry",
			KeyContradicts: `["claim:tass:2026-09-04:a91"]`,
		},
		// DataIntelligence: a precedent whose acceptance re-measured the metric.
		// Verified because it was re-measured; the outcome is the producer's
		// own word in _state. No number anywhere.
		"di precedent met_but_costly": {
			KeySource: "川鑫家电", KeyChunk: "-1",
			KeyProducer: ProducerMeasured, KeyGrade: GradeVerified,
			KeyState: "met_but_costly", KeyAt: "2026-08-20T02:00:00Z",
			"source": "di", "industry": "家电",
		},
		// DataIntelligence: a plan the client turned down. A refusal is a
		// record; it carries who and why.
		"di rejected plan": {
			KeySource: "川鑫家电", KeyProducer: ProducerHuman, KeyGrade: GradeRefused,
			KeyBy: "客户", KeyWhy: "换季前不动库存", KeyAt: "2026-08-12T09:00:00Z",
		},
		// DataIntelligence: an acceptance that could not be compared because
		// the measure was redefined under the plan. Held, with the reason.
		"di incomparable": {
			KeySource: "川鑫家电", KeyProducer: ProducerMeasured, KeyGrade: GradeHeld,
			KeyState: "incomparable", KeyWhy: "measure redefined after adoption",
			KeyAt: "2026-08-30T02:00:00Z",
		},
		// DataIntelligence: a compiled metric document. Deterministic from the
		// declared model, never checked against the world.
		"di metric doc": {
			KeySource: "shop", KeyChunk: "-1",
			KeyProducer: ProducerCompiled, KeyGrade: GradeSelfConsistent,
			KeyAt: "2026-08-16T04:00:00Z", "metric": "revenue",
		},
	}
	for name, meta := range cases {
		if err := ValidateContract(meta); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Every problem is named, and all of them at once: a producer should fix its
// writer in one pass, not discover the required keys one rejection at a time.
func TestContractNamesEveryMissingKeyAtOnce(t *testing.T) {
	err := ValidateContract(map[string]string{"company": "Reuters"})
	if err == nil {
		t.Fatal("an attribute-only record is not knowledge")
	}
	for _, key := range []string{KeySource, KeyProducer, KeyGrade, KeyAt} {
		if !strings.Contains(err.Error(), key+" is required") {
			t.Errorf("missing %s not named: %v", key, err)
		}
	}
}

// The contract exists to make an unexplained hold or refusal impossible to
// write. A reader cannot act on one, and will delete it.
func TestHeldAndRefusedRequireAReason(t *testing.T) {
	for _, grade := range []string{GradeHeld, GradeRefused} {
		meta := map[string]string{
			KeySource: "x", KeyProducer: ProducerLLMExtract, KeyGrade: grade,
			KeyAt: "2026-09-05T00:00:00Z",
		}
		err := ValidateContract(meta)
		if err == nil || !strings.Contains(err.Error(), KeyWhy+" is required") {
			t.Errorf("%s without %s accepted: %v", grade, KeyWhy, err)
		}
		meta[KeyWhy] = "ontology violation: Claim.reports has two sources"
		if err := ValidateContract(meta); err != nil {
			t.Errorf("%s with a reason refused: %v", grade, err)
		}
	}
}

func TestAHumanAssertionNamesTheHuman(t *testing.T) {
	err := ValidateContract(map[string]string{
		KeySource: "x", KeyProducer: ProducerHuman, KeyGrade: GradeAsserted,
		KeyAt: "2026-09-05T00:00:00Z",
	})
	if err == nil || !strings.Contains(err.Error(), KeyBy+" is required") {
		t.Fatalf("human producer without %s accepted: %v", KeyBy, err)
	}
}

// Closed sets stay closed. A producer inventing a sixth grade, or writing the
// proto enum name where the wire string belongs, gets told the whole allowed
// set, not just "invalid".
func TestUnknownGradeAndProducerAreNamedWithTheAllowedSet(t *testing.T) {
	err := ValidateContract(map[string]string{
		KeySource: "x", KeyProducer: "PRODUCER_LLM_EXTRACT", KeyGrade: "probably",
		KeyAt: "2026-09-05T00:00:00Z",
	})
	if err == nil {
		t.Fatal("unknown values accepted")
	}
	msg := err.Error()
	for _, want := range []string{`"probably"`, GradeVerified, GradeRefused, `"PRODUCER_LLM_EXTRACT"`, ProducerLLMExtract, ProducerMeasured} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %s: %s", want, msg)
		}
	}
}

// The shared brain is read by everyone who can read the record. A source
// string is the one place a producer might paste a connection string without
// thinking; it is caught here rather than in a log six months later.
func TestSourceMustNotCarryCredentials(t *testing.T) {
	for _, src := range []string{
		"postgres://di:s3cret@10.0.0.4/shop",
		"host=db user=di password=s3cret dbname=shop",
	} {
		err := ValidateContract(map[string]string{
			KeySource: src, KeyProducer: ProducerMeasured, KeyGrade: GradeSelfConsistent,
			KeyAt: "2026-09-05T00:00:00Z",
		})
		if err == nil || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("%q accepted as a source: %v", src, err)
		}
	}
	// A plain https URL with no userinfo is fine — that is what argus writes.
	if err := ValidateContract(map[string]string{
		KeySource: "https://www.reuters.com/world/x", KeyProducer: ProducerLLMExtract,
		KeyGrade: GradeAsserted, KeyAt: "2026-09-05T00:00:00Z",
	}); err != nil {
		t.Errorf("plain URL refused: %v", err)
	}
}

func TestTypedOptionalKeysAreChecked(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{
			KeySource: "x", KeyProducer: ProducerLLMExtract, KeyGrade: GradeAsserted,
			KeyAt: "2026-09-05T00:00:00Z",
		}
	}
	for _, tc := range []struct{ key, bad, want string }{
		{KeyChunk, "seventeen", "integer"},
		{KeyConfidence, "1.7", "[0,1]"},
		{KeyContradicts, `claim:a`, "JSON array"},
		{KeyContradicts, `["claim:a", ""]`, "empty id"},
		{KeyAt, "yesterday", "RFC 3339"},
	} {
		m := base()
		m[tc.key] = tc.bad
		err := ValidateContract(m)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s=%q: want %q in error, got %v", tc.key, tc.bad, tc.want, err)
		}
	}
}

// Keys the contract does not validate pass through: alchemy's _model,
// _ontology and friends, and every attribute the source itself carried.
func TestUnknownKeysPassThrough(t *testing.T) {
	if err := ValidateContract(map[string]string{
		KeySource: "x", KeyProducer: ProducerDDL, KeyGrade: GradeSelfConsistent,
		KeyAt:       "2026-09-05T00:00:00Z",
		"_ontology": "world@3", "_something_alchemy_adds_later": "y", "table": "orders",
	}); err != nil {
		t.Fatalf("pass-through keys rejected: %v", err)
	}
}
