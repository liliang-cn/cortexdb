package cortexdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// The engine's own metadata keys for a review decision. They are written
// from the explicit review_status, reviewed_by and reviewed_at fields of the
// write tools, and from nowhere else: a copy in a caller's metadata is
// dropped, and a caller's key named review_status is the caller's, stored
// as it came and never read. The leading underscore marks the engine's
// keys, as _expires_at and importflow's _table do.
const (
	reviewStatusKey = "_review_status"
	reviewedByKey   = "_reviewed_by"
	reviewedAtKey   = "_reviewed_at"

	ReviewProposed = "proposed"
	ReviewAccepted = "accepted"
	ReviewRejected = "rejected"
)

// ReviewGateNote is the sentence every tool that mentions the gate carries:
// what the gate is, and what it is not.
const ReviewGateNote = "The review gate is a curation filter, not access control: knowledge_get and memory_get still read a hidden row by id, any writer can save accepted, and plan.filters.review_status lists proposed documents to any reader (memories ignore that filter)."

// includeProposedDescription is the schema text of include_proposed on
// knowledge_search and aggregate_metadata.
const includeProposedDescription = "Documents only. Set true to serve proposed documents too while the review gate is on (CORTEXDB_REVIEW_GATE=1); rejected ones stay hidden, and a review_status filter still decides. Without the gate nothing is hidden and it changes nothing. A hit's metadata carries its _review_status."

// reviewFields are the explicit fields of a write. An empty status keeps
// the stored decision, whatever the write does with metadata: a decision
// is never cleared, only replaced by another.
type reviewFields struct{ Status, By, At string }

type reviewState struct{ status, by, at string }

func reviewOf(stored map[string]any) reviewState {
	var r reviewState
	r.status, _ = stringFromAny(stored[reviewStatusKey])
	r.by, _ = stringFromAny(stored[reviewedByKey])
	r.at, _ = stringFromAny(stored[reviewedAtKey])
	return r
}

// decideReview checks a decision against the stored one and returns the
// state to store. A proposal is not a decision and takes no reviewer and no
// instant. An acceptance or a rejection names who made it: the calling key
// when there is one, which a reviewed_by naming someone else cannot
// replace, else the reviewed_by field; its instant is the reviewed_at field
// when given, the stored instant when the same reviewer repeats the same
// decision, else now.
func decideReview(ctx context.Context, f reviewFields, prev reviewState) (reviewState, error) {
	switch f.Status {
	case "":
		return prev, nil
	case ReviewProposed:
		if strings.TrimSpace(f.By) != "" || f.At != "" {
			return reviewState{}, fmt.Errorf("review_status=proposed takes no reviewed_by or reviewed_at: a proposal is not a decision")
		}
		return reviewState{status: ReviewProposed}, nil
	case ReviewAccepted, ReviewRejected:
		by := strings.TrimSpace(f.By)
		if writer := writerOf(ctx); writer != "" {
			if by != "" && by != writer {
				return reviewState{}, fmt.Errorf("reviewed_by %q is not the calling key %q: a decision names the key that made it", by, writer)
			}
			by = writer
		}
		if by == "" {
			return reviewState{}, fmt.Errorf("review_status=%s needs reviewed_by: a decision names who made it", f.Status)
		}
		at := f.At
		switch {
		case at == "" && f.Status == prev.status && by == prev.by && prev.at != "":
			at = prev.at
		case at == "":
			at = time.Now().UTC().Format(time.RFC3339)
		default:
			if _, err := time.Parse(time.RFC3339, at); err != nil {
				return reviewState{}, fmt.Errorf("reviewed_at must be an RFC 3339 instant, got %q", at)
			}
		}
		return reviewState{status: f.Status, by: by, at: at}, nil
	default:
		return reviewState{}, fmt.Errorf("review_status must be %s, %s or %s, got %q", ReviewProposed, ReviewAccepted, ReviewRejected, f.Status)
	}
}

// setReview puts the decision on the metadata a write is about to store,
// after dropping any copy of the engine's keys the caller sent.
func setReview(ctx context.Context, metadata map[string]string, stored map[string]any, f reviewFields) error {
	delete(metadata, reviewStatusKey)
	delete(metadata, reviewedByKey)
	delete(metadata, reviewedAtKey)
	r, err := decideReview(ctx, f, reviewOf(stored))
	if err != nil {
		return err
	}
	if r.status != "" {
		metadata[reviewStatusKey] = r.status
	}
	if r.by != "" {
		metadata[reviewedByKey] = r.by
	}
	if r.at != "" {
		metadata[reviewedAtKey] = r.at
	}
	return nil
}

// setReviewAny is setReview for a memory's metadata.
func setReviewAny(ctx context.Context, metadata map[string]any, stored map[string]any, f reviewFields) error {
	plain := map[string]string{}
	if err := setReview(ctx, plain, stored, f); err != nil {
		return err
	}
	delete(metadata, reviewStatusKey)
	delete(metadata, reviewedByKey)
	delete(metadata, reviewedAtKey)
	for k, v := range plain {
		metadata[k] = v
	}
	return nil
}

// WithReviewGate makes the brain leave proposed and rejected rows out of
// search and recall unless a filter asks for them by status. Off, every row
// is served and the status is plain metadata. The gate is a curation
// filter, not access control (ReviewGateNote).
func WithReviewGate() Option {
	return func(db *DB) {
		db.reviewGate = true
	}
}

type proposalsContextKey struct{}

// includeProposed returns the context of a read that asked for the
// proposed documents too (include_proposed): under the gate such a read
// has a rejected document hidden and no other (reviewHiddenPredicateFor).
// Any caller may ask, as any caller may filter on review_status: the gate
// is a curation filter, not access control (ReviewGateNote).
func includeProposed(ctx context.Context, include bool) context.Context {
	if !include {
		return ctx
	}
	return context.WithValue(ctx, proposalsContextKey{}, true)
}

// reviewHides reports whether the gate leaves a row of this status out.
func reviewHides(status string) bool {
	return status == ReviewProposed || status == ReviewRejected
}

// reviewHiddenPredicate is the SQL that says the gate hides a document for
// its review status. With a wanted status, any other status is hidden, and
// for accepted a document with no status counts as accepted; a wanted value
// outside the three statuses, which no document carries, hides every
// document. With none, under the gate, a proposed or rejected document is
// hidden; without the gate, nothing is. Only the three constants reach the
// SQL. The "IN" form is the one the planner answers from
// idx_documents_review_status. An empty predicate means nothing is hidden.
func (db *DB) reviewHiddenPredicate(want string) string {
	expr := db.Dialect().JSONTextGuarded("metadata", reviewStatusKey)
	switch want {
	case "":
		if !db.reviewGate {
			return ""
		}
		return `(` + expr + ` IN ('` + ReviewProposed + `', '` + ReviewRejected + `'))`
	case ReviewAccepted:
		return `(` + expr + ` > '' AND ` + expr + ` <> '` + ReviewAccepted + `')`
	case ReviewProposed, ReviewRejected:
		return `(` + expr + ` IS NULL OR ` + expr + ` <> '` + want + `')`
	default:
		return `(1 = 1)`
	}
}

// reviewHiddenPredicateFor is reviewHiddenPredicate for a read under ctx. A
// call that asked for the proposals too (includeProposed) and wants no
// status has, under the gate, a rejected document hidden and no other.
func (db *DB) reviewHiddenPredicateFor(ctx context.Context, want string) string {
	if want == "" && db.reviewGate && ctx.Value(proposalsContextKey{}) != nil {
		return `(` + db.Dialect().JSONTextGuarded("metadata", reviewStatusKey) + ` IN ('` + ReviewRejected + `'))`
	}
	return db.reviewHiddenPredicate(want)
}

// memoryUnreviewed reports whether the gate leaves a memory out of recall.
func (db *DB) memoryUnreviewed(record MemoryRecord) bool {
	return db.reviewGate && reviewHides(record.ReviewStatus)
}

// storedMetadata is a record's metadata, or nil for no record.
func storedMetadata(existing *core.Document) map[string]any {
	if existing == nil {
		return nil
	}
	return existing.Metadata
}
