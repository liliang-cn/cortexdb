package memoryflow

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

type stubPlanner struct {
	plan *cortexdb.RetrievalPlan
}

func (s stubPlanner) Plan(_ context.Context, _ string, _ SessionState) (*cortexdb.RetrievalPlan, error) {
	if s.plan == nil {
		return nil, nil
	}
	planCopy := *s.plan
	return &planCopy, nil
}

type stubExtractor struct {
	promotions []PromotionCandidate
}

func (s stubExtractor) Extract(_ context.Context, _ Transcript, _ SessionState) ([]PromotionCandidate, error) {
	return append([]PromotionCandidate(nil), s.promotions...), nil
}

func TestServiceIngestRecallWakeUpAndClose(t *testing.T) {
	dbPath := fmt.Sprintf("memoryflow_%s.db", t.Name())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	svc, err := New(db, stubPlanner{
		plan: &cortexdb.RetrievalPlan{
			Query:         "project deadline",
			Keywords:      []string{"deadline", "schedule"},
			EntityNames:   []string{"Apollo"},
			RetrievalMode: cortexdb.RetrievalModeLexical,
		},
	}, stubExtractor{
		promotions: []PromotionCandidate{
			{
				Title:   "Deadline decision",
				Content: "Apollo deadline is Friday.",
				Metadata: map[string]string{
					"kind": "decision",
				},
				Entities: []cortexdb.ToolEntityInput{
					{Name: "Apollo"},
					{Name: "Friday"},
				},
				Relations: []cortexdb.ToolRelationInput{
					{From: "Apollo", To: "Friday", Type: "deadline"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	transcript := Transcript{
		SessionID: "session-1",
		UserID:    "user-1",
		Source:    "chatgpt",
		Turns: []TranscriptTurn{
			{Role: "user", Content: "What is the Apollo project deadline?"},
			{Role: "assistant", Content: "The current deadline is Friday."},
			{Role: "user", Content: "Remember that decision."},
			{Role: "assistant", Content: "Stored."},
		},
	}

	ingestResp, err := svc.IngestTranscript(context.Background(), IngestTranscriptRequest{
		Transcript: transcript,
		Scope:      cortexdb.MemoryScopeSession,
		Namespace:  "agent",
		Wing:       "projects",
		Room:       "apollo",
	})
	if err != nil {
		t.Fatalf("ingest transcript: %v", err)
	}
	if ingestResp.Count != 2 {
		t.Fatalf("expected 2 exchanges, got %+v", ingestResp)
	}

	episodesResp, err := svc.ListEpisodes(context.Background(), ListEpisodesRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "agent",
	})
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(episodesResp.Episodes) != 2 {
		t.Fatalf("expected 2 stored episodes, got %+v", episodesResp)
	}

	transcriptResp, err := svc.GetTranscript(context.Background(), GetTranscriptRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "agent",
	})
	if err != nil {
		t.Fatalf("get transcript: %v", err)
	}
	if len(transcriptResp.Transcript.Turns) != 4 {
		t.Fatalf("expected 4 reconstructed turns, got %+v", transcriptResp.Transcript)
	}
	if transcriptResp.Transcript.Source != "chatgpt" {
		t.Fatalf("expected transcript source, got %+v", transcriptResp.Transcript)
	}

	recallResp, err := svc.Recall(context.Background(), RecallRequest{
		Query:            "When is the Apollo deadline?",
		UserID:           "user-1",
		SessionID:        "session-1",
		Scope:            cortexdb.MemoryScopeSession,
		Namespace:        "agent",
		DisableKnowledge: true,
		State: SessionState{
			UserID:     "user-1",
			SessionID:  "session-1",
			Namespace:  "agent",
			Wing:       "projects",
			Room:       "apollo",
			Transcript: &transcript,
		},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(recallResp.Plan.Keywords) == 0 || recallResp.Plan.Keywords[0] != "deadline" {
		t.Fatalf("expected planner keywords to be applied, got %+v", recallResp.Plan)
	}
	if len(recallResp.Response.Memories) == 0 {
		t.Fatalf("expected recall memories, got %+v", recallResp)
	}

	wakeResp, err := svc.WakeUp(context.Background(), WakeUpRequest{
		Identity: "You are the Apollo PM assistant.",
		Recall: RecallRequest{
			Query:            "Apollo startup context",
			UserID:           "user-1",
			SessionID:        "session-1",
			Scope:            cortexdb.MemoryScopeSession,
			Namespace:        "agent",
			DisableKnowledge: true,
			State: SessionState{
				UserID:    "user-1",
				SessionID: "session-1",
				Namespace: "agent",
			},
		},
	})
	if err != nil {
		t.Fatalf("wake-up: %v", err)
	}
	if wakeResp.Text == "" {
		t.Fatal("expected wake-up text")
	}

	layersResp, err := svc.WakeUpLayers(context.Background(), WakeUpLayersRequest{
		Identity: "You are the Apollo PM assistant.",
		Recall: RecallRequest{
			Query:            "Apollo startup context",
			UserID:           "user-1",
			SessionID:        "session-1",
			Scope:            cortexdb.MemoryScopeSession,
			Namespace:        "agent",
			DisableKnowledge: true,
		},
	})
	if err != nil {
		t.Fatalf("wake-up layers: %v", err)
	}
	if len(layersResp.Layers) != 4 {
		t.Fatalf("expected 4 wake-up layers, got %+v", layersResp)
	}
	if layersResp.Layers[0].Level != WakeUpLevelL0 || layersResp.Layers[3].Level != WakeUpLevelL3 {
		t.Fatalf("unexpected wake-up levels: %+v", layersResp.Layers)
	}

	closeResp, err := svc.CloseSession(context.Background(), CloseSessionRequest{
		Transcript: transcript,
		Scope:      cortexdb.MemoryScopeSession,
		Namespace:  "agent",
		State: SessionState{
			UserID:    "user-1",
			SessionID: "session-1",
			Namespace: "agent",
		},
		Promote: true,
	})
	if err != nil {
		t.Fatalf("close session: %v", err)
	}
	if closeResp.Count != 1 {
		t.Fatalf("expected one promotion, got %+v", closeResp)
	}

	inference, err := db.RefreshKnowledgeGraphInference(context.Background(), cortexdb.KnowledgeGraphInferenceRefreshRequest{})
	if err != nil {
		t.Fatalf("refresh inference: %v", err)
	}
	_ = inference

	knowledge, err := db.QueryKnowledgeGraph(context.Background(), cortexdb.KnowledgeGraphQueryRequest{
		Query: `
SELECT ?title WHERE {
	?doc <https://schema.org/name> ?title .
}
`,
	})
	if err == nil && knowledge.Result.Count > 0 {
		_ = knowledge
	}

	protocolResp, err := svc.PrepareReply(context.Background(), ReplyProtocolRequest{
		Identity: "You are the Apollo PM assistant.",
		Recall: RecallRequest{
			Query:            "What is the deadline?",
			UserID:           "user-1",
			SessionID:        "session-1",
			Scope:            cortexdb.MemoryScopeSession,
			Namespace:        "agent",
			DisableKnowledge: true,
		},
	})
	if err != nil {
		t.Fatalf("prepare reply: %v", err)
	}
	if !protocolResp.ShouldGround || protocolResp.RecommendedMode != "grounded" {
		t.Fatalf("expected grounded reply protocol, got %+v", protocolResp)
	}
}

func TestCloseSessionUsesHeuristicExtractorWhenNoneProvided(t *testing.T) {
	dbPath := fmt.Sprintf("memoryflow_heuristic_%s.db", t.Name())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	svc, err := New(db, nil, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	resp, err := svc.CloseSession(context.Background(), CloseSessionRequest{
		Transcript: Transcript{
			SessionID: "session-1",
			Turns: []TranscriptTurn{
				{Role: "user", Content: "We decided the launch deadline is Friday."},
			},
		},
		Promote: true,
	})
	if err != nil {
		t.Fatalf("expected heuristic extractor to work, got %v", err)
	}
	if resp.Count == 0 {
		t.Fatalf("expected heuristic promotion, got %+v", resp)
	}
}

func TestKnowledgePromotionPersistsRecord(t *testing.T) {
	dbPath := fmt.Sprintf("memoryflow_query_%s.db", t.Name())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	svc, err := New(db, nil, stubExtractor{
		promotions: []PromotionCandidate{
			{
				KnowledgeID: "knowledge:apollo",
				Kind:        PromotionKindMilestone,
				Title:       "Apollo",
				Content:     "Apollo launches on Friday.",
				Entities: []cortexdb.ToolEntityInput{
					{Name: "Apollo"},
					{Name: "Friday"},
				},
				Relations: []cortexdb.ToolRelationInput{
					{From: "Apollo", To: "Friday", Type: "launch_day"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	_, err = svc.CloseSession(context.Background(), CloseSessionRequest{
		Transcript: Transcript{
			SessionID: "session-apollo",
			Turns:     []TranscriptTurn{{Role: "user", Content: "Apollo launches on Friday."}},
		},
		Promote: true,
	})
	if err != nil {
		t.Fatalf("close session promote: %v", err)
	}

	record, err := db.GetKnowledge(context.Background(), cortexdb.KnowledgeGetRequest{KnowledgeID: "knowledge:apollo"})
	if err != nil {
		t.Fatalf("get promoted knowledge: %v", err)
	}
	if record.Knowledge.Title != "Apollo" {
		t.Fatalf("unexpected promoted knowledge: %+v", record)
	}
}

func TestDiaryAppendAndList(t *testing.T) {
	dbPath := fmt.Sprintf("memoryflow_diary_%s.db", t.Name())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	svc, err := New(db, nil, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	_, err = svc.AppendDiaryEntry(context.Background(), DiaryEntryRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "diary",
		Taxonomy: Taxonomy{
			Wing:   "projects",
			Room:   "apollo",
			Source: "manual",
			Tags:   []string{"daily"},
		},
		Content: "Daily note: Apollo deadline was confirmed.",
	})
	if err != nil {
		t.Fatalf("append diary entry: %v", err)
	}

	listResp, err := svc.ListDiaryEntries(context.Background(), DiaryListRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "diary",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list diary entries: %v", err)
	}
	if len(listResp.Entries) != 1 {
		t.Fatalf("expected one diary entry, got %+v", listResp)
	}
	if listResp.Entries[0].Role != "diary" {
		t.Fatalf("unexpected diary role: %+v", listResp.Entries[0])
	}
}
