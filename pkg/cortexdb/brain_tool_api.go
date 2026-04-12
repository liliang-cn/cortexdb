package cortexdb

import "context"

// KnowledgeMemoryRemember stores a memory item through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryRemember(ctx context.Context, req KnowledgeMemoryRememberRequest) (*KnowledgeMemoryRememberResponse, error) {
	return t.db.KnowledgeMemory().Remember(ctx, req)
}

// KnowledgeMemoryRecall retrieves fused memory and knowledge context through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryRecall(ctx context.Context, req KnowledgeMemoryRecallRequest) (*KnowledgeMemoryRecallResponse, error) {
	return t.db.KnowledgeMemory().Recall(ctx, req)
}

// KnowledgeMemoryBuildContextPack assembles a context pack through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryBuildContextPack(ctx context.Context, req KnowledgeMemoryBuildContextPackRequest) (*KnowledgeMemoryBuildContextPackResponse, error) {
	return t.db.KnowledgeMemory().BuildContextPack(ctx, req)
}

// KnowledgeMemoryPromoteToKnowledge promotes memories into durable knowledge through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryPromoteToKnowledge(ctx context.Context, req KnowledgeMemoryPromoteToKnowledgeRequest) (*KnowledgeMemoryPromoteToKnowledgeResponse, error) {
	return t.db.KnowledgeMemory().PromoteToKnowledge(ctx, req)
}

// KnowledgeMemoryExpandEntityContext expands graph context around entities through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryExpandEntityContext(ctx context.Context, req KnowledgeMemoryExpandEntityContextRequest) (*KnowledgeMemoryExpandEntityContextResponse, error) {
	return t.db.KnowledgeMemory().ExpandEntityContext(ctx, req)
}

// KnowledgeMemoryNeighbors resolves and returns graph neighbors through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryNeighbors(ctx context.Context, req KnowledgeMemoryNeighborsRequest) (*KnowledgeMemoryNeighborsResponse, error) {
	return t.db.KnowledgeMemory().Neighbors(ctx, req)
}

// KnowledgeMemoryShortestPath resolves and returns a graph shortest path through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryShortestPath(ctx context.Context, req KnowledgeMemoryShortestPathRequest) (*KnowledgeMemoryShortestPathResponse, error) {
	return t.db.KnowledgeMemory().ShortestPath(ctx, req)
}

// KnowledgeMemoryReflect synthesizes a structured reflection through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryReflect(ctx context.Context, req KnowledgeMemoryReflectRequest) (*KnowledgeMemoryReflectResponse, error) {
	return t.db.KnowledgeMemory().Reflect(ctx, req)
}

// KnowledgeMemoryConsolidate reflects, stores a summary memory, and optionally promotes it to knowledge through the tool surface.
func (t *GraphRAGToolbox) KnowledgeMemoryConsolidate(ctx context.Context, req KnowledgeMemoryConsolidateRequest) (*KnowledgeMemoryConsolidateResponse, error) {
	return t.db.KnowledgeMemory().Consolidate(ctx, req)
}
