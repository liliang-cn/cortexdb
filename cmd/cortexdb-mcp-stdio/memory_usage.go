package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// runMemoryUsage reports which memories recall actually surfaces and which
// never have — the observability half of the feedback loop.
//
// Deliberately a report, not a policy: feeding recall counts back into ranking
// would compound its own bias (recall begets recall), so the numbers go to a
// person, who can prune, rewrite or supersede with --sync-memory.
//
// `--memory-usage [--top N] [--stale-days D]`
func runMemoryUsage(args []string) {
	top := 15
	staleDays := 60
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--top":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &top)
			}
		case "--stale-days":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &staleDays)
			}
		}
	}

	memories, source := loadAllMemories(context.Background())
	if len(memories) == 0 {
		fmt.Printf("no memories in %s\n", source)
		return
	}

	type usage struct {
		m     cortexdb.MemoryRecord
		count int
		last  string
	}
	used := make([]usage, 0, len(memories))
	var never []usage
	for _, m := range memories {
		u := usage{m: m}
		if m.Metadata != nil {
			if v, ok := m.Metadata["recall_count"].(float64); ok {
				u.count = int(v)
			}
			u.last, _ = m.Metadata["last_recalled_at"].(string)
		}
		if u.count > 0 {
			used = append(used, u)
		} else {
			never = append(never, u)
		}
	}
	sort.Slice(used, func(i, j int) bool { return used[i].count > used[j].count })

	fmt.Printf("memory usage in %s — %d memories, %d recalled at least once, %d never\n\n",
		source, len(memories), len(used), len(never))

	fmt.Printf("most recalled (top %d):\n", top)
	for i, u := range used {
		if i >= top {
			break
		}
		fmt.Printf("  %4d×  %-44s %s\n", u.count, clip(u.m.ID, 44), clip(collapseWS(u.m.Content), 60))
	}

	// Old and never recalled: the prune candidates. Age matters — a memory
	// written last week has not had its chance yet.
	cutoff := time.Now().AddDate(0, 0, -staleDays)
	var stale []usage
	for _, u := range never {
		if !u.m.CreatedAt.IsZero() && u.m.CreatedAt.Before(cutoff) {
			stale = append(stale, u)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].m.CreatedAt.Before(stale[j].m.CreatedAt) })
	fmt.Printf("\nnever recalled and older than %d days (%d — prune candidates for --export-memory / --sync-memory):\n", staleDays, len(stale))
	for i, u := range stale {
		if i >= top {
			break
		}
		fmt.Printf("  %s  %-44s %s\n", u.m.CreatedAt.Format("2006-01-02"), clip(u.m.ID, 44), clip(collapseWS(u.m.Content), 52))
	}
	if len(stale) > top {
		fmt.Printf("  ... and %d more\n", len(stale)-top)
	}
	_ = strings.TrimSpace
}
