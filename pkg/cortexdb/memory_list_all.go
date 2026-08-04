package cortexdb

import "context"

// Bulk memory listing, for the views that need every record rather than a
// search result: the HTML dashboard and the Markdown export.
//
// Without this the two one-shot modes can only open a local database file,
// which on a machine pointed at a shared brain is the wrong one — they would
// render a file nothing writes to any more.

// MemoryListAllRequest asks for every stored memory.
type MemoryListAllRequest struct {
	// Limit caps how many records come back (0 = defaultMemoryListLimit).
	// A brain with tens of thousands of memories should not be pulled into one
	// message by accident; a caller that wants them all raises this on purpose.
	Limit int `json:"limit,omitempty"`
}

// MemoryListAllResponse carries the records and says whether it had to stop.
type MemoryListAllResponse struct {
	Memories []MemoryRecord `json:"memories"`
	// Truncated is true when Limit cut the listing short. An export that
	// silently dropped records would look complete and not be.
	Truncated bool `json:"truncated,omitempty"`
}

const defaultMemoryListLimit = 5000

// ListAllMemoriesPaged wraps ListAllMemories with a bound and a truncation flag.
func (db *DB) ListAllMemoriesPaged(ctx context.Context, req MemoryListAllRequest) (*MemoryListAllResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultMemoryListLimit
	}
	all, err := db.ListAllMemories(ctx)
	if err != nil {
		return nil, err
	}
	resp := &MemoryListAllResponse{Memories: all}
	if len(all) > limit {
		resp.Memories = all[:limit]
		resp.Truncated = true
	}
	return resp, nil
}
