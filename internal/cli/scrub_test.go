package cli

import (
	"strings"
	"testing"
	"time"
)

func TestScrubDropsPrivateBlocks(t *testing.T) {
	in := "deploy to node-b <private>the vault pin is 4711</private> after lunch <PRIVATE>and this\nspans lines</PRIVATE>."
	got := scrubForCapture(in)
	if strings.Contains(got, "4711") || strings.Contains(got, "spans") || !strings.Contains(got, "deploy to node-b") || !strings.Contains(got, "after lunch") {
		t.Fatalf("%q", got)
	}
	// An unclosed block hides the rest of the message, not just the tag.
	if got := scrubForCapture("ok <private>everything after this"); strings.Contains(got, "everything") {
		t.Fatalf("unclosed: %q", got)
	}
}

func TestScrubRedactsCredentials(t *testing.T) {
	secrets := []string{
		"sk-ant-api03-" + strings.Repeat("a1B2", 10),
		"sk-proj-" + strings.Repeat("Z9y8", 8),
		"ghp_" + strings.Repeat("A1b2", 9),
		"github_pat_" + strings.Repeat("11AB", 8),
		"AKIAIOSFODNN7EXAMPLE",
		"xoxb-123456789012-abcdefABCDEF",
		"AIza" + strings.Repeat("Sy0_", 9)[:35],
		"npm_" + strings.Repeat("aB3d", 9),
		"Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----",
	}
	for _, s := range secrets {
		got := scrubForCapture("use " + s + " for it")
		if strings.Contains(got, s) || !strings.Contains(got, redacted) {
			t.Errorf("not redacted: %q -> %q", s, got)
		}
	}
	for in, want := range map[string]string{
		`DB_PASSWORD=hunter22`:           `DB_PASSWORD=[REDACTED]`,
		`api_key: "abcd1234efgh"`:        `api_key: [REDACTED]`,
		`CORTEXDB_GRPC_TOKEN='s3cr3t-v'`: `CORTEXDB_GRPC_TOKEN=[REDACTED]`,
	} {
		if got := scrubForCapture(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// Ids that look random but are not credentials stay: a memory that lost its
// commit hash or a session UUID lost what made it findable.
func TestScrubKeepsOrdinaryIdentifiers(t *testing.T) {
	for _, s := range []string{
		"commit 4247495 fixed it; full 71d34bad1045610252eda02ee79549436896e784",
		"session 3d09536c-89dd-4d52-863b-e218dc67efb9",
		"the token budget is 8000 and the password field is required",
		"set CORTEXDB_GRPC_TOKEN in the unit file",
	} {
		if got := scrubForCapture(s); got != s {
			t.Errorf("changed: %q -> %q", s, got)
		}
	}
}

func TestDigestScrubsBothSides(t *testing.T) {
	path := t.TempDir() + "/s.jsonl"
	write(t, path, jsonl(t,
		claudeTurn("user", "my key is sk-ant-api03-"+strings.Repeat("x1Y2", 10)),
		claudeTurn("assistant", "Saved <private>the pin 4711</private> for later."),
		claudeTurn("user", "<private>all of this is mine</private>"),
	), time.Time{})
	digest, turns, err := digestTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(digest, "x1Y2") || strings.Contains(digest, "4711") || strings.Contains(digest, "all of this") || turns != 1 {
		t.Fatalf("turns=%d digest=%q", turns, digest)
	}
}
