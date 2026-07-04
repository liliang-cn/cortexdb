package cortexdb

import "testing"

// TestAutoModePrefersSemanticWithEmbedder guards the fix for the bug where auto
// mode silently fell back to lexical even with an embedder configured, so a
// configured embedder was never used for plain knowledge queries.
func TestAutoModePrefersSemanticWithEmbedder(t *testing.T) {
	// Plain query, no entity-like terms, auto mode.
	with := resolveRetrievalDecision(RetrievalModeAuto, false, "pet that woofs", nil, true, false, RetrievalModeLexical, "", true)
	if with.EffectiveMode == RetrievalModeLexical {
		t.Errorf("auto+embedder should not be lexical, got %q (%s)", with.EffectiveMode, with.Reason)
	}

	without := resolveRetrievalDecision(RetrievalModeAuto, false, "pet that woofs", nil, true, false, RetrievalModeLexical, "", false)
	if without.EffectiveMode != RetrievalModeLexical {
		t.Errorf("auto without embedder should stay lexical, got %q", without.EffectiveMode)
	}

	// Explicit lexical must stay lexical regardless of embedder.
	lex := resolveRetrievalDecision(RetrievalModeLexical, false, "anything", nil, true, false, RetrievalModeLexical, "", true)
	if lex.EffectiveMode != RetrievalModeLexical {
		t.Errorf("explicit lexical must stay lexical, got %q", lex.EffectiveMode)
	}
}
