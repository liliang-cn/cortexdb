package main

import (
	"strings"
	"testing"
)

// A memory whose title is entirely CJK used to slug to "memory", and on macOS
// and Windows memory.md is MEMORY.md — so writing the index destroyed it. The
// directory then looked like the memory had been deleted, which is exactly what
// --sync-memory --prune would have gone on to do.
func TestSlugifyDoesNotProduceTheIndexName(t *testing.T) {
	for _, title := range []string{"黄金历史价格关键数据点：", "内存", "…", "", "   "} {
		if got := slugify(title); got != "" {
			t.Errorf("slugify(%q) = %q, want empty so the caller falls back to the id", title, got)
		}
	}
}

func TestUniqueSlugNeverReturnsTheIndexName(t *testing.T) {
	used := map[string]int{strings.ToLower(memoryIndexSlug): 1}
	if got := uniqueSlug(memoryIndexSlug, used); strings.EqualFold(got, memoryIndexSlug) {
		t.Fatalf("uniqueSlug handed out the index name %q", got)
	}
}

// "situation" seen nine times produced "situation-9", which is also what a
// memory titled "situation 9" slugs to; the second write replaced the first.
func TestUniqueSlugSurvivesASuffixThatIsAlsoARealSlug(t *testing.T) {
	used := map[string]int{}
	seen := map[string]bool{}
	// Eight memories that all slug to "situation", then one really called
	// "situation 9", then more of the first kind.
	names := []string{
		"situation", "situation", "situation", "situation",
		"situation", "situation", "situation", "situation",
		"situation-9",
		"situation", "situation",
	}
	for i, n := range names {
		got := uniqueSlug(n, used)
		if seen[strings.ToLower(got)] {
			t.Fatalf("slug %q handed out twice (input %d = %q) — one memory would overwrite another", got, i, n)
		}
		seen[strings.ToLower(got)] = true
	}
	if len(seen) != len(names) {
		t.Errorf("got %d distinct slugs for %d memories", len(seen), len(names))
	}
}

// Every memory must get a file whose name is its own.
func TestExportSlugAssignmentIsCollisionFree(t *testing.T) {
	titles := []string{
		"黄金历史价格关键数据点：", // slugs to nothing
		"记忆",                // slugs to nothing
		"situation",
		"situation",
		"situation-2",
		"MEMORY",
		"memory",
		"Report 1-1",
		"1-1-2",
		"1-1",
		"1-1",
	}
	used := map[string]int{strings.ToLower(memoryIndexSlug): 1}
	seen := map[string]bool{}
	for i, title := range titles {
		id := "id-" + strings.Repeat("x", i%3) + string(rune('a'+i))
		slug := uniqueSlug(firstNonEmptyStr(slugify(title), firstNonEmptyStr(slugify(id), "untitled")), used)
		key := strings.ToLower(slug)
		if seen[key] {
			t.Fatalf("title %q reused slug %q", title, slug)
		}
		if key == strings.ToLower(memoryIndexSlug) {
			t.Fatalf("title %q took the index name", title)
		}
		seen[key] = true
	}
	if len(seen) != len(titles) {
		t.Errorf("got %d files for %d memories", len(seen), len(titles))
	}
}
