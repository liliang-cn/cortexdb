package memoryflow

import (
	"context"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// PromotionKind classifies durable knowledge candidates.
type PromotionKind string

const (
	PromotionKindDecision   PromotionKind = "decision"
	PromotionKindPreference PromotionKind = "preference"
	PromotionKindMilestone  PromotionKind = "milestone"
	PromotionKindProblem    PromotionKind = "problem"
	PromotionKindNote       PromotionKind = "note"
)

// WakeUpLevel identifies one context density tier.
type WakeUpLevel string

const (
	WakeUpLevelL0 WakeUpLevel = "L0"
	WakeUpLevelL1 WakeUpLevel = "L1"
	WakeUpLevelL2 WakeUpLevel = "L2"
	WakeUpLevelL3 WakeUpLevel = "L3"
)

// Taxonomy describes the workflow-level memory placement and labeling metadata.
type Taxonomy struct {
	Kind     string            `json:"kind,omitempty"`
	Wing     string            `json:"wing,omitempty"`
	Room     string            `json:"room,omitempty"`
	Source   string            `json:"source,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	Entities []string          `json:"entities,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// TranscriptTurn is one normalized turn from a conversation transcript.
type TranscriptTurn struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	SourceID  string    `json:"source_id,omitempty"`
}

// Transcript is the normalized input to the memory workflow ingest path.
type Transcript struct {
	SessionID string           `json:"session_id,omitempty"`
	UserID    string           `json:"user_id,omitempty"`
	Source    string           `json:"source,omitempty"`
	Title     string           `json:"title,omitempty"`
	Turns     []TranscriptTurn `json:"turns"`
}

// SessionState provides planner context for recall and wake-up flows.
type SessionState struct {
	UserID     string         `json:"user_id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Wing       string         `json:"wing,omitempty"`
	Room       string         `json:"room,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Taxonomy   Taxonomy       `json:"taxonomy,omitempty"`
	Transcript *Transcript    `json:"transcript,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// PromotionCandidate is one knowledge item proposed by the workflow extractor.
type PromotionCandidate struct {
	KnowledgeID string                       `json:"knowledge_id,omitempty"`
	Kind        PromotionKind                `json:"kind,omitempty"`
	Title       string                       `json:"title,omitempty"`
	Content     string                       `json:"content"`
	SourceURL   string                       `json:"source_url,omitempty"`
	Author      string                       `json:"author,omitempty"`
	Collection  string                       `json:"collection,omitempty"`
	Metadata    map[string]string            `json:"metadata,omitempty"`
	Entities    []cortexdb.ToolEntityInput   `json:"entities,omitempty"`
	Relations   []cortexdb.ToolRelationInput `json:"relations,omitempty"`
}

// QueryPlanner produces retrieval plans for lexical or hybrid recall.
type QueryPlanner interface {
	Plan(ctx context.Context, query string, state SessionState) (*cortexdb.RetrievalPlan, error)
}

// SessionExtractor proposes durable knowledge promotions from a transcript.
type SessionExtractor interface {
	Extract(ctx context.Context, transcript Transcript, state SessionState) ([]PromotionCandidate, error)
}

// PromotionPolicy filters or reorders extracted promotion candidates.
type PromotionPolicy interface {
	Select(ctx context.Context, transcript Transcript, state SessionState, candidates []PromotionCandidate) ([]PromotionCandidate, error)
}

// IngestTranscriptRequest stores a transcript as episodic memory exchanges.
type IngestTranscriptRequest struct {
	Transcript Transcript     `json:"transcript"`
	Scope      string         `json:"scope,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Wing       string         `json:"wing,omitempty"`
	Room       string         `json:"room,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Taxonomy   Taxonomy       `json:"taxonomy,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// IngestTranscriptResponse summarizes the stored episodic memories.
type IngestTranscriptResponse struct {
	MemoryIDs []string `json:"memory_ids"`
	Count     int      `json:"count"`
}

// Episode is one stored exchange-shaped memory record plus reconstructed turns.
type Episode struct {
	Memory        cortexdb.MemoryRecord `json:"memory"`
	ExchangeIndex int                   `json:"exchange_index,omitempty"`
	Taxonomy      Taxonomy              `json:"taxonomy,omitempty"`
	Turns         []TranscriptTurn      `json:"turns,omitempty"`
}

// RecallRequest resolves a plan and returns a fused memory/knowledge pack.
type RecallRequest struct {
	Query            string                  `json:"query"`
	UserID           string                  `json:"user_id,omitempty"`
	SessionID        string                  `json:"session_id,omitempty"`
	Scope            string                  `json:"scope,omitempty"`
	Namespace        string                  `json:"namespace,omitempty"`
	Collection       string                  `json:"collection,omitempty"`
	TopKMemories     int                     `json:"top_k_memories,omitempty"`
	TopKKnowledge    int                     `json:"top_k_knowledge,omitempty"`
	RetrievalMode    string                  `json:"retrieval_mode,omitempty"`
	DisableMemory    bool                    `json:"disable_memory,omitempty"`
	DisableKnowledge bool                    `json:"disable_knowledge,omitempty"`
	DisableGraph     bool                    `json:"disable_graph,omitempty"`
	GraphLight       bool                    `json:"graph_light,omitempty"`
	MaxContextChars  int                     `json:"max_context_chars,omitempty"`
	MaxContextChunks int                     `json:"max_context_chunks,omitempty"`
	Plan             *cortexdb.RetrievalPlan `json:"plan,omitempty"`
	State            SessionState            `json:"state,omitempty"`
}

// RecallResponse contains the fused recall pack plus the resolved plan.
type RecallResponse struct {
	Plan     cortexdb.RetrievalPlan       `json:"plan"`
	Decision cortexdb.RetrievalDecision   `json:"decision"`
	Response cortexdb.BrainRecallResponse `json:"response"`
}

// WakeUpRequest assembles a compact startup context for an agent.
type WakeUpRequest struct {
	Identity string        `json:"identity,omitempty"`
	Recall   RecallRequest `json:"recall"`
}

// WakeUpResponse contains the final startup text and the underlying recall response.
type WakeUpResponse struct {
	Text   string         `json:"text"`
	Recall RecallResponse `json:"recall"`
}

// WakeUpLayer is one wake-up context tier.
type WakeUpLayer struct {
	Level WakeUpLevel `json:"level"`
	Title string      `json:"title,omitempty"`
	Text  string      `json:"text"`
}

// WakeUpLayersRequest requests all wake-up context tiers.
type WakeUpLayersRequest struct {
	Identity string        `json:"identity,omitempty"`
	Recall   RecallRequest `json:"recall"`
}

// WakeUpLayersResponse returns all wake-up context tiers plus the recall payload.
type WakeUpLayersResponse struct {
	Layers []WakeUpLayer  `json:"layers"`
	Recall RecallResponse `json:"recall"`
}

// CloseSessionRequest optionally promotes extracted knowledge at session end.
type CloseSessionRequest struct {
	Transcript Transcript        `json:"transcript"`
	Scope      string            `json:"scope,omitempty"`
	Namespace  string            `json:"namespace,omitempty"`
	State      SessionState      `json:"state,omitempty"`
	Promote    bool              `json:"promote,omitempty"`
	Collection string            `json:"collection,omitempty"`
	Author     string            `json:"author,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// CloseSessionResponse reports promotions created at session close.
type CloseSessionResponse struct {
	Promotions []cortexdb.KnowledgeRecord `json:"promotions,omitempty"`
	Count      int                        `json:"count"`
}

// ListEpisodesRequest lists stored episodic memory exchanges for a resolved bucket.
type ListEpisodesRequest struct {
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// ListEpisodesResponse returns stored episodic exchange records.
type ListEpisodesResponse struct {
	Episodes []Episode `json:"episodes"`
}

// GetTranscriptRequest reconstructs the original transcript for a resolved bucket.
type GetTranscriptRequest struct {
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// GetTranscriptResponse returns the reconstructed transcript and its backing episodes.
type GetTranscriptResponse struct {
	Transcript Transcript `json:"transcript"`
	Episodes   []Episode  `json:"episodes"`
}

// DiaryEntryRequest appends one workflow diary entry.
type DiaryEntryRequest struct {
	EntryID    string         `json:"entry_id,omitempty"`
	UserID     string         `json:"user_id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Taxonomy   Taxonomy       `json:"taxonomy,omitempty"`
	PathHint   string         `json:"path_hint,omitempty"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Importance float64        `json:"importance,omitempty"`
}

// DiaryEntryResponse returns the stored diary memory record.
type DiaryEntryResponse struct {
	Entry cortexdb.MemoryRecord `json:"entry"`
}

// DiaryListRequest lists diary entries for a resolved bucket.
type DiaryListRequest struct {
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// DiaryListResponse returns chronological diary entries.
type DiaryListResponse struct {
	Entries []cortexdb.MemoryRecord `json:"entries"`
}

// ReplyProtocolRequest prepares a grounded reply workflow.
type ReplyProtocolRequest struct {
	Identity string        `json:"identity,omitempty"`
	Recall   RecallRequest `json:"recall"`
}

// ReplyProtocolResponse returns wake-up layers plus a deterministic grounding recommendation.
type ReplyProtocolResponse struct {
	WakeUp          WakeUpLayersResponse `json:"wake_up"`
	ShouldGround    bool                 `json:"should_ground"`
	RecommendedMode string               `json:"recommended_mode"`
	Steps           []ProtocolStep       `json:"steps,omitempty"`
	Notes           []string             `json:"notes,omitempty"`
}

// ProtocolStep is one deterministic step in the reply workflow.
type ProtocolStep struct {
	Name      string `json:"name"`
	Action    string `json:"action"`
	Required  bool   `json:"required"`
	Completed bool   `json:"completed"`
	Note      string `json:"note,omitempty"`
}
