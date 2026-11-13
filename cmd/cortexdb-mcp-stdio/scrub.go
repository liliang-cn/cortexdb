package main

import (
	"regexp"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// What leaves a session for the model that distils it, and from there for a
// brain other agents read, is scrubbed first.
//
// Two things are taken out. Text between <private> and </private> is the
// person's own way to say "not this": it never reaches the model. And a
// credential pasted into a session — an API key, a token, a private key, a
// password=… — is replaced by [REDACTED], because a capture that kept it would
// file it under a stable id in a brain shared across machines.
//
// The patterns are deliberately the shapes credentials are issued in, not
// "anything that looks random": a commit hash or a UUID is not a secret, and a
// scrubber that ate them would ruin the memories it was protecting.

const redacted = cortexdb.RedactedCredential

var privateBlock = regexp.MustCompile(`(?is)<private>.*?(?:</private>|$)`)

// assignedSecret is a credential written as a setting: password=…, token: …,
// API_KEY="…". The name stays, so the memory still says which setting it was.
var assignedSecret = regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_]*)(\s*[:=]\s*)("[^"\s]{6,}"|'[^'\s]{6,}'|[^\s"',;]{6,})`)

// scrubForCapture removes private blocks and redacts credentials.
func scrubForCapture(text string) string {
	text = privateBlock.ReplaceAllString(text, "")
	text, _ = cortexdb.RedactCredentials(text)
	return assignedSecret.ReplaceAllString(text, "${1}${2}"+redacted)
}
