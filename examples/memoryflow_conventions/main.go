package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

func main() {
	dbPath := "memoryflow_conventions.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cfg := memoryflow.DefaultProjectConfig("apollo")
	cfg.Conventions.Rules = append(cfg.Conventions.Rules, memoryflow.ConventionRule{
		MatchSource: "manual",
		Wing:        "operations",
		Room:        "logbook",
		AddTags:     []string{"manual"},
	})

	flow, err := memoryflow.New(
		db,
		nil,
		nil,
		memoryflow.WithProjectConfig(cfg),
	)
	if err != nil {
		log.Fatal(err)
	}

	resolved := flow.ResolveTaxonomy(memoryflow.Taxonomy{}, memoryflow.SourceHint{
		Source: "manual",
		Path:   "docs/runbook.md",
		Title:  "Apollo runbook",
		Tags:   []string{"ops"},
	})

	fmt.Printf("resolved wing=%s room=%s tags=%v\n", resolved.Wing, resolved.Room, resolved.Tags)

	_, err = flow.AppendDiaryEntry(context.Background(), memoryflow.DiaryEntryRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "diary",
		Taxonomy:  resolved,
		PathHint:  "docs/runbook.md",
		Content:   "Daily runbook note for Apollo.",
	})
	if err != nil {
		log.Fatal(err)
	}

	list, err := flow.ListDiaryEntries(context.Background(), memoryflow.DiaryListRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "diary",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("diary entries=%d\n", len(list.Entries))
}
