package agentmem

import "time"

// ScopeType identifies a memory isolation scope.
type ScopeType string

const (
	ScopeGlobal  ScopeType = "global"
	ScopeAgent   ScopeType = "agent"
	ScopeTeam    ScopeType = "team"
	ScopeUser    ScopeType = "user"
	ScopeSession ScopeType = "session"
)

// Scope is a typed memory bank coordinate.
type Scope struct {
	Type ScopeType `json:"type"`
	ID   string    `json:"id,omitempty"`
}

// Type categorises a memory record.
type Type string

const (
	TypeFact        Type = "fact"
	TypeSkill       Type = "skill"
	TypePattern     Type = "pattern"
	TypeContext     Type = "context"
	TypePreference  Type = "preference"
	TypeObservation Type = "observation"
)

// SourceType records how a memory was created.
type SourceType string

const (
	SourceUserInput    SourceType = "user_input"
	SourceInferred     SourceType = "inferred"
	SourceConsolidated SourceType = "consolidated"
)

// Revision is a single audit entry recording a change to a memory.
type Revision struct {
	At      time.Time `json:"at"`
	By      string    `json:"by,omitempty"`
	Summary string    `json:"summary,omitempty"`
}

// Memory is the canonical agentmem record.
type Memory struct {
	ID         string  `json:"id"`
	Scope      Scope   `json:"scope"`
	Type       Type    `json:"type"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`

	Tags     []string `json:"tags,omitempty"`
	Keywords []string `json:"keywords,omitempty"`

	SourceType  SourceType `json:"source_type,omitempty"`
	Confidence  float64    `json:"confidence,omitempty"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`

	ValidFrom    time.Time  `json:"valid_from,omitempty"`
	ValidTo      *time.Time `json:"valid_to,omitempty"`
	SupersededBy string     `json:"superseded_by,omitempty"`
	Conflicting  bool       `json:"conflicting,omitempty"`

	Archived      bool       `json:"archived,omitempty"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	ArchiveReason string     `json:"archive_reason,omitempty"`

	AccessCount  int       `json:"access_count"`
	LastAccessed time.Time `json:"last_accessed,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	RevisionHistory []Revision `json:"revision_history,omitempty"`
}

// ScoredMemory is a memory paired with a relevance score.
type ScoredMemory struct {
	Memory *Memory `json:"memory"`
	Score  float64 `json:"score"`
}

// BankConfig captures the disposition of a memory bank, mirroring agent-go's
// MemoryBankConfig.
type BankConfig struct {
	Mission    string   `json:"mission,omitempty"`
	Directives []string `json:"directives,omitempty"`
	Skepticism int      `json:"skepticism,omitempty"`
	Literalism int      `json:"literalism,omitempty"`
	Empathy    int      `json:"empathy,omitempty"`
}

// MentalModel is a curated rule or summary kept alongside the bank.
type MentalModel struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	Content     string    `json:"content"`
	Tags        []string  `json:"tags,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ContextSlot names a fixed system-context channel (OpenClaw style).
type ContextSlot string

const (
	SlotSoul      ContextSlot = "SOUL"
	SlotAgents    ContextSlot = "AGENTS"
	SlotTools     ContextSlot = "TOOLS"
	SlotMemory    ContextSlot = "MEMORY"
	SlotHeartbeat ContextSlot = "HEARTBEAT"
)

// DefaultContextOrder is the order BuildContextString uses by default.
var DefaultContextOrder = []ContextSlot{SlotSoul, SlotAgents, SlotTools, SlotMemory, SlotHeartbeat}
