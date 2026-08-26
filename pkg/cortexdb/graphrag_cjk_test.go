package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// Entities are found with a Title Case Latin pattern, which cannot see Chinese, Japanese
// or Korean at all. On a CJK corpus every match is incidental Latin — in practice
// romanisation. A scanned textbook is the worst case: its text layer mangles the pinyin
// tone marks into stray capitals ("hSn dAi", "pT Wng", "Gu Dr"), which are perfectly good
// Title Case, so the graph filled with syllable debris and not one real concept. Tone
// marks cannot be tested for, because the mangling destroys them.

func entityNames(t *testing.T, text string) []string {
	t.Helper()
	extraction, err := defaultGraphRAGExtractor{}.Extract(context.Background(), text)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	names := make([]string, 0, len(extraction.Entities))
	for _, entity := range extraction.Entities {
		names = append(names, entity.Name)
	}
	return names
}

func TestChineseTextbookYieldsNoRomanisationEntities(t *testing.T) {
	// Real text taken from a scanned primary-school textbook's text layer.
	cases := []string{
		"山坡上，从坪坝 里，走来了许多小学生，有汉 族的，有傣 族 hSn dAi 太阳花 pT Wng",
		"bSo miQo fO jU Gu Dr # 暴 喵 孵 叽 偶 尔",
		"1 大青树下的小学 Dà Qīng Shù",
		"秋天的雨，是一把钥匙。qiū tiān de yǔ",
	}
	for _, text := range cases {
		if names := entityNames(t, text); len(names) != 0 {
			t.Errorf("text %.24q produced entities %v; romanisation is not a concept", text, names)
		}
	}
}

func TestBilingualTextKeepsRealLatinEntities(t *testing.T) {
	names := entityNames(t, "本文介绍 Transformer 架构与 Chain Rule 的关系，并对比 Attention 机制。")
	joined := strings.Join(names, ",")
	for _, want := range []string{"Transformer", "Chain Rule", "Attention"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bilingual entity %q lost; got %v", want, names)
		}
	}
}

func TestLatinOnlyTextIsUnaffected(t *testing.T) {
	names := entityNames(t, "The Chain Rule applies when Composition of Functions appears. See Dr Smith.")
	joined := strings.Join(names, ",")
	// Including short vowel-less names, which are only filtered when CJK is present.
	// "Chain Rule" rather than "The Chain Rule": a leading determiner is grammar,
	// not part of the name, and keeping it made a second node for the same thing.
	for _, want := range []string{"Chain Rule", "Composition", "Functions", "Dr Smith"} {
		if !strings.Contains(joined, want) {
			t.Errorf("entity %q lost from Latin-only text; got %v", want, names)
		}
	}
}

func TestPlausibleLatinEntityRules(t *testing.T) {
	for _, debris := range []string{"Gu Dr", "Wng", "Sh", "Qn", "pT", "Gu"} {
		if plausibleLatinEntity(debris) {
			t.Errorf("%q accepted as a plausible entity", debris)
		}
	}
	for _, real := range []string{"Transformer", "Chain Rule", "Attention", "Roman Empire"} {
		if !plausibleLatinEntity(real) {
			t.Errorf("%q rejected as an entity", real)
		}
	}
}
