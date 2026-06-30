package graphflow

import "testing"

func TestKeepEntityCandidate(t *testing.T) {
	// freq=1 unless noted: single-occurrence behavior is what matters most.
	keep := []string{
		"RAG", "CGO", "JSON", "OIDC", "MCP",       // acronyms
		"DefaultDBPath", "NewCortexMemoryStore",   // CamelCase
		"TikTokAIVideoGenerator", "UserPromptSubmit",
		"smartticket.superleo.app",                 // domain
		"internal/ossagent",                        // path
		"IPv4",                                     // has digit
		"OSS_EMB_KEY",                              // identifier
	}
	for _, name := range keep {
		if !keepEntityCandidate(name, 1) {
			t.Errorf("expected to KEEP %q", name)
		}
	}

	drop := []string{
		"git apply --3way",                         // command (whitespace)
		"cd ~/Things/dev && pnpm build",            // command
		"{ handle /api/* { reverse_proxy } }",      // config block
		"Home", "Quick", "Sample", "Verified",      // common words
		"Deep", "Double", "Zero", "Other", "Get",   // common words
		"Network", "Admin", "English", "Full",      // common words
		"A",                                        // too short
	}
	for _, name := range drop {
		if keepEntityCandidate(name, 1) {
			t.Errorf("expected to DROP %q", name)
		}
	}

	// A plain proper noun is dropped at freq 1 but kept when it recurs.
	if keepEntityCandidate("Meridian", 1) {
		t.Errorf("expected single-occurrence %q to be dropped", "Meridian")
	}
	if !keepEntityCandidate("Meridian", 2) {
		t.Errorf("expected recurring %q to be kept", "Meridian")
	}
}
