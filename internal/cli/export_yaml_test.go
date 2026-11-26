package cli

import (
	"encoding/json"
	"testing"
)

func TestYAMLInlineQuotedStringsPreserveBackslashes(t *testing.T) {
	// JSON quoted strings are valid YAML double-quoted scalars, so decoding
	// independently checks both escaping and preservation of each input byte.
	for _, input := range []string{
		`C:\temp\notes: keep this`,
		`C:\work\backup #1`,
		`a"b#c`,
		`α # π`,
		`a <b> & c`,
	} {
		t.Run(input, func(t *testing.T) {
			encoded := yamlInline(input)
			var decoded string
			if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
				t.Fatalf("quoted scalar %q cannot be decoded: %v", encoded, err)
			}
			if decoded != input {
				t.Fatalf("roundtrip = %q, want %q", decoded, input)
			}
		})
	}
}

func TestYAMLInlineLeavesHTMLCharactersReadable(t *testing.T) {
	if got, want := yamlInline(`see <docs> & notes`), `"see <docs> & notes"`; got != want {
		t.Fatalf("yamlInline = %s, want %s", got, want)
	}
}

func TestYAMLInlineKeepsPlainSourceValues(t *testing.T) {
	if got := yamlInline("transcript"); got != "transcript" {
		t.Fatalf("plain value changed: %q", got)
	}
}
