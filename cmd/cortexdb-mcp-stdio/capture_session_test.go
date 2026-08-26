package main

import (
	"encoding/json"
	"testing"
)

// One deployed proxy returns JSON missing its final closer. Retrying does not
// help — the adapter repairs instead.
func TestRepairJSONBalancesDroppedClosers(t *testing.T) {
	cases := []string{
		`{"memories":[{"slug":"a","content":"b"}]`,  // dropped final }
		`{"memories":[{"slug":"a","content":"b"}]}`, // already valid
		`{"memories":[{"slug":"a","content":"b"`,    // dropped }]}
	}
	for _, c := range cases {
		if !json.Valid(repairJSON([]byte(c))) {
			t.Errorf("repair failed for %q -> %q", c, repairJSON([]byte(c)))
		}
	}
	// A brace inside a string must not confuse the balance.
	tricky := `{"content":"a } inside","x":[1,2`
	if !json.Valid(repairJSON([]byte(tricky))) {
		t.Errorf("repair failed for string-embedded brace: %q", repairJSON([]byte(tricky)))
	}
}

// A user turn that was all injected machinery is not something the user said.
func TestStripTranscriptNoise(t *testing.T) {
	in := "<system-reminder>hook stuff</system-reminder>做!"
	if got := stripTranscriptNoise(in); got != "做!" {
		t.Errorf("got %q", got)
	}
	allNoise := "<command-name>/model</command-name><local-command-stdout>ok</local-command-stdout>"
	if got := stripTranscriptNoise(allNoise); got != "" {
		t.Errorf("machinery-only turn should vanish, got %q", got)
	}
}

// The same model answers bare JSON one run and fenced JSON the next.
func TestRepairJSONStripsMarkdownFences(t *testing.T) {
	fenced := "```json\n{\"memories\":[]}\n```"
	if !json.Valid(repairJSON([]byte(fenced))) {
		t.Errorf("fence not stripped: %q", repairJSON([]byte(fenced)))
	}
	fencedAndTruncated := "```json\n{\"memories\":[{\"slug\":\"a\"}]"
	if !json.Valid(repairJSON([]byte(fencedAndTruncated))) {
		t.Errorf("fence+truncation not repaired: %q", repairJSON([]byte(fencedAndTruncated)))
	}
}
