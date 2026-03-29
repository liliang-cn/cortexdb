package graph

import "context"

// SyncDeletedNodeIDs removes committed graph-node deletions from the optional HNSW index.
func (g *GraphStore) SyncDeletedNodeIDs(_ context.Context, nodeIDs []string) {
	if g.hnswIndex == nil || len(nodeIDs) == 0 {
		return
	}
	for _, nodeID := range nodeIDs {
		if nodeID == "" {
			continue
		}
		g.hnswIndex.index.Remove(nodeID)
	}
}
