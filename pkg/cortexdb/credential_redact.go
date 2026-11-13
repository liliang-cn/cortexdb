package cortexdb

import "regexp"

// RedactedCredential is what a credential found in saved text is replaced by.
const RedactedCredential = "[REDACTED]"

// credentialPatterns are the shapes credentials are issued in: a provider's
// key prefix, a private key block, a bearer header. They are deliberately not
// "anything that looks random" — a commit hash or a UUID is not a secret, and
// a filter that ate them would ruin the text it was protecting — and not
// "anything assigned to a name with token in it", which would redact
// max_tokens: 100000 out of a document.
var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`),
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`\bcfut_[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{20,}`),
}

// RedactCredentials replaces every credential in text with
// RedactedCredential and reports how many it replaced.
//
// SaveMemory, UpdateMemory, SaveKnowledge and UpdateKnowledge run it on what
// they store: a memory is filed under a stable id, recalled into prompts and,
// on a shared brain, read by every agent connected to it, so a key pasted into
// one is a key handed to all of them. WithoutCredentialRedaction turns that
// off for a store that is meant to hold them.
func RedactCredentials(text string) (string, int) {
	n := 0
	for _, p := range credentialPatterns {
		text = p.ReplaceAllStringFunc(text, func(string) string {
			n++
			return RedactedCredential
		})
	}
	return text, n
}

// WithoutCredentialRedaction stores saved text as given, credentials included.
func WithoutCredentialRedaction() Option {
	return func(db *DB) { db.keepCredentials = true }
}

// redactSaved is RedactCredentials unless this store keeps credentials.
func (db *DB) redactSaved(text string) (string, int) {
	if db.keepCredentials {
		return text, 0
	}
	return RedactCredentials(text)
}
