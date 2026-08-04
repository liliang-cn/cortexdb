package memoryflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// LLM-driven memory maintenance: decide which existing memories a new statement
// supersedes, instead of only ever appending.
//
// This mirrors graphflow's UpdateGraphFromText, with one deliberate difference:
// there is no delete. A graph fact removed in error is re-derivable — run the
// extractor over the source text again. A memory is frequently the only record
// that something was said, and "which older memory does this replace?" is
// exactly the judgement a model makes confidently and wrongly. So the
// destructive op is absent by construction: the model may retire a memory, which
// keeps the row, links it forward, and is undone by clearing one metadata key.

const (
	MemoryEditOpAdd = "add"
	// MemoryEditOpUpdate rewrites a memory in place. Use it when the earlier
	// wording was wrong; use supersede when the earlier statement was true then
	// and is not true now, so the history is worth keeping.
	MemoryEditOpUpdate = "update"
	// MemoryEditOpSupersede retires a memory in favour of a new one.
	MemoryEditOpSupersede = "supersede"
)

// supersededByKey links a retired memory to the one that replaced it.
// supersededAtKey records when, so a mistaken pass can be found by time.
const (
	supersededByKey = "superseded_by"
	supersededAtKey = "superseded_at"
	supersedesKey   = "supersedes"
)

// MemoryEdit is one proposed mutation.
type MemoryEdit struct {
	Op string `json:"op"` // add|update|supersede

	// MemoryID is the target of update/supersede.
	MemoryID string `json:"memory_id,omitempty"`
	// Content is the new text for add/update, and the replacement written by
	// supersede.
	Content string `json:"content,omitempty"`

	// Reason is the model's justification; surfaced in dry runs, and stored on
	// the retired memory so a later reader can see why it was retired.
	Reason string `json:"reason,omitempty"`
}

// MemoryEditPlan is a set of mutations.
type MemoryEditPlan struct {
	Edits []MemoryEdit `json:"edits"`
}

// MemoryEditOptions configures how a plan is produced and applied.
type MemoryEditOptions struct {
	// AllowSupersede must be set for supersede edits to apply. Retiring a
	// memory is opt-in for the same reason deleting a graph fact is.
	AllowSupersede bool
	// MaxSupersedes caps how many memories one pass may retire
	// (0 = default 10), so a confused model cannot retire a whole brain.
	MaxSupersedes int
	// DryRun reports what would change without touching anything.
	DryRun bool

	// MaxCandidates bounds how many existing memories are shown to the model
	// (0 = default 30). More context is not automatically better: a long list
	// makes a wrong supersede more likely, not less.
	MaxCandidates int

	// Scope, UserID, Namespace place newly written memories. They default to
	// the same global scope SaveMemory uses.
	Scope     string
	UserID    string
	Namespace string
}

// MemoryEditReport summarizes an applied (or simulated) plan.
type MemoryEditReport struct {
	// Added counts standalone additions. A supersede also writes a replacement,
	// but that write is counted by Superseded — not here — so the two numbers
	// never describe the same row twice.
	Added      int          `json:"added"`
	Updated    int          `json:"updated"`
	Superseded int          `json:"superseded"`
	Skipped    []string     `json:"skipped,omitempty"`
	Applied    []MemoryEdit `json:"applied,omitempty"`
	DryRun     bool         `json:"dry_run,omitempty"`
}

const defaultMaxSupersedes = 10

// ApplyMemoryEdits applies a mutation plan deterministically. It is the single
// write path, and is useful on its own when the caller — or an agent that has
// already reasoned about it — has decided on the edits.
//
// One bad edit never fails the plan: unknown ops, missing targets and edits over
// the cap are recorded in Skipped and the rest still apply. A half-applied plan
// is far better than a rejected one when the alternative is losing the good
// edits alongside the bad.
func ApplyMemoryEdits(ctx context.Context, db *cortexdb.DB, plan MemoryEditPlan, opts MemoryEditOptions) (*MemoryEditReport, error) {
	if db == nil {
		return nil, fmt.Errorf("memoryflow: nil db")
	}
	maxSupersedes := opts.MaxSupersedes
	if maxSupersedes <= 0 {
		maxSupersedes = defaultMaxSupersedes
	}

	rep := &MemoryEditReport{DryRun: opts.DryRun}

	for _, e := range plan.Edits {
		switch e.Op {
		case MemoryEditOpAdd:
			if e.Content == "" {
				rep.skip(e, "add with no content")
				continue
			}
			if !opts.DryRun {
				if _, err := db.SaveMemory(ctx, opts.saveRequest(memoryIDFor(e.Content), e.Content, nil)); err != nil {
					rep.skip(e, fmt.Sprintf("save failed: %v", err))
					continue
				}
			}
			rep.Added++
			rep.Applied = append(rep.Applied, e)

		case MemoryEditOpUpdate:
			if e.MemoryID == "" || e.Content == "" {
				rep.skip(e, "update needs memory_id and content")
				continue
			}
			if !memoryExists(ctx, db, e.MemoryID) {
				rep.skip(e, "update target does not exist: "+e.MemoryID)
				continue
			}
			if !opts.DryRun {
				content := e.Content
				if _, err := db.UpdateMemory(ctx, cortexdb.MemoryUpdateRequest{
					MemoryID: e.MemoryID, Content: &content,
				}); err != nil {
					rep.skip(e, fmt.Sprintf("update failed: %v", err))
					continue
				}
			}
			rep.Updated++
			rep.Applied = append(rep.Applied, e)

		case MemoryEditOpSupersede:
			if !opts.AllowSupersede {
				rep.skip(e, "supersede proposed but not allowed (set AllowSupersede)")
				continue
			}
			if rep.Superseded >= maxSupersedes {
				rep.skip(e, fmt.Sprintf("supersede cap of %d reached", maxSupersedes))
				continue
			}
			if e.MemoryID == "" || e.Content == "" {
				rep.skip(e, "supersede needs memory_id and the replacement content")
				continue
			}
			if !memoryExists(ctx, db, e.MemoryID) {
				rep.skip(e, "supersede target does not exist: "+e.MemoryID)
				continue
			}
			if !opts.DryRun {
				if err := supersede(ctx, db, e, opts); err != nil {
					rep.skip(e, fmt.Sprintf("supersede failed: %v", err))
					continue
				}
			}
			rep.Superseded++
			rep.Applied = append(rep.Applied, e)

		default:
			rep.skip(e, "unknown op "+e.Op)
		}
	}

	return rep, nil
}

// supersede writes the replacement, then marks the old memory as retired and
// points it at the new one.
//
// The replacement is written first on purpose: if the process dies between the
// two steps the brain has one extra memory, which is recoverable by reading. The
// other order would leave a memory marked retired with nothing to point at.
func supersede(ctx context.Context, db *cortexdb.DB, e MemoryEdit, opts MemoryEditOptions) error {
	newID := memoryIDFor(e.Content)

	if _, err := db.SaveMemory(ctx, opts.saveRequest(newID, e.Content, map[string]any{
		supersedesKey: e.MemoryID,
	})); err != nil {
		return fmt.Errorf("write replacement: %w", err)
	}

	meta := map[string]any{
		supersededByKey: newID,
		supersededAtKey: time.Now().UTC().Format(time.RFC3339),
	}
	if e.Reason != "" {
		meta["superseded_reason"] = e.Reason
	}
	if _, err := db.UpdateMemory(ctx, cortexdb.MemoryUpdateRequest{
		MemoryID: e.MemoryID, Metadata: meta,
	}); err != nil {
		return fmt.Errorf("mark %s retired: %w", e.MemoryID, err)
	}
	return nil
}

func memoryExists(ctx context.Context, db *cortexdb.DB, id string) bool {
	got, err := db.GetMemory(ctx, cortexdb.MemoryGetRequest{MemoryID: id})
	return err == nil && got != nil && got.Memory.ID != ""
}

func (o MemoryEditOptions) saveRequest(id, content string, meta map[string]any) cortexdb.MemorySaveRequest {
	scope := o.Scope
	if scope == "" {
		scope = "global"
	}
	return cortexdb.MemorySaveRequest{
		MemoryID:  id,
		Content:   content,
		Scope:     scope,
		UserID:    o.UserID,
		Namespace: o.Namespace,
		Metadata:  meta,
	}
}

func (r *MemoryEditReport) skip(e MemoryEdit, why string) {
	label := e.Op
	if e.MemoryID != "" {
		label += " " + e.MemoryID
	}
	r.Skipped = append(r.Skipped, label+": "+why)
}

// memoryIDFor derives an ID from the content, so re-running the same edit
// updates the same row instead of leaving a second copy behind. A model that
// proposes the same addition twice — across a retry, or two passes over the
// same transcript — must not grow the brain by two.
func memoryIDFor(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "mem-" + hex.EncodeToString(sum[:6])
}
