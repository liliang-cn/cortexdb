package agentmem

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Reflector is the LLM-dependent extension point for consolidating raw facts
// into observations. It is the agentmem analogue of agent-go's Reflect prompt.
//
// Implementations receive only the candidate facts (already filtered to active,
// not-yet-evidenced records) and any existing observations in the same scope.
// They must return one or more Observation rows; agentmem will persist them and
// optionally mark superseded observations stale.
type Reflector interface {
	Consolidate(ctx context.Context, facts []*Memory, existing []*Memory) ([]Observation, error)
}

// Observation is the result of one consolidation step.
type Observation struct {
	Content     string
	Confidence  float64
	EvidenceIDs []string
	Conflicting bool
	UpdateObsID string // when set, the existing observation with this id is marked stale
}

// ReflectResult summarises a Reflect run.
type ReflectResult struct {
	Created  int      `json:"created"`
	Updated  int      `json:"updated"`
	Reviewed int      `json:"reviewed"`
	NewIDs   []string `json:"new_ids,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// Reflect collects active facts in scope, asks the Reflector to consolidate
// them into observations, persists each observation as a new memory, and marks
// any superseded observations stale.
func (s *Store) Reflect(ctx context.Context, scope Scope, r Reflector) (*ReflectResult, error) {
	if r == nil {
		return nil, fmt.Errorf("agentmem: nil Reflector")
	}
	scope = normalizeScope(scope)
	bank := BankID(scope)

	memos, err := s.activeBankMemories(ctx, bank)
	if err != nil {
		return nil, err
	}

	var facts []*Memory
	var existing []*Memory
	usedEvidence := map[string]struct{}{}
	for _, m := range memos {
		switch m.Type {
		case TypeFact:
			facts = append(facts, m)
		case TypeObservation:
			existing = append(existing, m)
			for _, id := range m.EvidenceIDs {
				usedEvidence[id] = struct{}{}
			}
		}
	}

	var newFacts []*Memory
	for _, f := range facts {
		if _, used := usedEvidence[f.ID]; !used {
			newFacts = append(newFacts, f)
		}
	}
	res := &ReflectResult{Reviewed: len(newFacts)}
	if len(newFacts) < 2 {
		res.Note = "not enough new facts to consolidate (need ≥2)"
		return res, nil
	}

	observations, err := r.Consolidate(ctx, newFacts, existing)
	if err != nil {
		return nil, fmt.Errorf("agentmem: reflector: %w", err)
	}

	now := time.Now().UTC()
	for _, obs := range observations {
		if obs.Content == "" || len(obs.EvidenceIDs) < 2 {
			continue
		}
		newID := uuid.NewString()
		mem := &Memory{
			ID:          newID,
			Scope:       scope,
			Type:        TypeObservation,
			Content:     obs.Content,
			Importance:  obs.Confidence,
			Confidence:  obs.Confidence,
			EvidenceIDs: obs.EvidenceIDs,
			Conflicting: obs.Conflicting,
			SourceType:  SourceConsolidated,
			ValidFrom:   now,
			CreatedAt:   now,
		}
		if err := s.Save(ctx, mem); err != nil {
			return nil, fmt.Errorf("agentmem: save observation: %w", err)
		}
		res.NewIDs = append(res.NewIDs, newID)
		if obs.UpdateObsID != "" {
			if err := s.MarkStale(ctx, obs.UpdateObsID, newID); err != nil {
				return nil, err
			}
			res.Updated++
		} else {
			res.Created++
		}
	}
	return res, nil
}

func (s *Store) activeBankMemories(ctx context.Context, bank string) ([]*Memory, error) {
	rows, err := s.query(ctx, selectMemoryColumns+`
		FROM agentmem_memories
		WHERE bank_id = ? AND archived = 0
		  AND valid_to IS NULL AND (superseded_by IS NULL OR superseded_by = '')
		ORDER BY created_at ASC
	`, bank)
	if err != nil {
		return nil, fmt.Errorf("agentmem: load bank memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanAndAttach(ctx, rows)
}
