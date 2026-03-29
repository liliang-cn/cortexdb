package cortexdb

import "context"

// BrainRemember stores a memory item through the tool surface.
func (t *GraphRAGToolbox) BrainRemember(ctx context.Context, req BrainRememberRequest) (*BrainRememberResponse, error) {
	return t.db.Brain().Remember(ctx, req)
}

// BrainRecall retrieves fused memory and knowledge context through the tool surface.
func (t *GraphRAGToolbox) BrainRecall(ctx context.Context, req BrainRecallRequest) (*BrainRecallResponse, error) {
	return t.db.Brain().Recall(ctx, req)
}

// BrainBuildContextPack assembles a context pack through the tool surface.
func (t *GraphRAGToolbox) BrainBuildContextPack(ctx context.Context, req BrainBuildContextPackRequest) (*BrainBuildContextPackResponse, error) {
	return t.db.Brain().BuildContextPack(ctx, req)
}

// BrainPromoteToKnowledge promotes memories into durable knowledge through the tool surface.
func (t *GraphRAGToolbox) BrainPromoteToKnowledge(ctx context.Context, req BrainPromoteToKnowledgeRequest) (*BrainPromoteToKnowledgeResponse, error) {
	return t.db.Brain().PromoteToKnowledge(ctx, req)
}

// BrainExpandEntityContext expands graph context around entities through the tool surface.
func (t *GraphRAGToolbox) BrainExpandEntityContext(ctx context.Context, req BrainExpandEntityContextRequest) (*BrainExpandEntityContextResponse, error) {
	return t.db.Brain().ExpandEntityContext(ctx, req)
}

// BrainNeighbors resolves and returns graph neighbors through the tool surface.
func (t *GraphRAGToolbox) BrainNeighbors(ctx context.Context, req BrainNeighborsRequest) (*BrainNeighborsResponse, error) {
	return t.db.Brain().Neighbors(ctx, req)
}

// BrainShortestPath resolves and returns a graph shortest path through the tool surface.
func (t *GraphRAGToolbox) BrainShortestPath(ctx context.Context, req BrainShortestPathRequest) (*BrainShortestPathResponse, error) {
	return t.db.Brain().ShortestPath(ctx, req)
}

// BrainReflect synthesizes a structured reflection through the tool surface.
func (t *GraphRAGToolbox) BrainReflect(ctx context.Context, req BrainReflectRequest) (*BrainReflectResponse, error) {
	return t.db.Brain().Reflect(ctx, req)
}

// BrainConsolidate reflects, stores a summary memory, and optionally promotes it to knowledge through the tool surface.
func (t *GraphRAGToolbox) BrainConsolidate(ctx context.Context, req BrainConsolidateRequest) (*BrainConsolidateResponse, error) {
	return t.db.Brain().Consolidate(ctx, req)
}
