package rpcserver

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type graphragService struct {
	rpcv1.UnimplementedGraphRagServiceServer
	db *cortexdb.DB
}

func scoredTextsToProto(in []core.ScoredEmbedding) []*rpcv1.ScoredText {
	out := make([]*rpcv1.ScoredText, 0, len(in))
	for _, e := range in {
		out = append(out, &rpcv1.ScoredText{
			Id:         e.ID,
			Content:    e.Content,
			DocId:      e.DocID,
			Collection: e.Collection,
			Metadata:   e.Metadata,
			Score:      e.Score,
		})
	}
	return out
}

func (s *graphragService) InsertGraphDocument(ctx context.Context, req *rpcv1.InsertGraphDocumentRequest) (*rpcv1.InsertGraphDocumentResponse, error) {
	resp, err := s.db.InsertGraphDocument(ctx, cortexdb.GraphRAGDocument{
		ID:       req.GetId(),
		Title:    req.GetTitle(),
		Content:  req.GetContent(),
		Metadata: req.GetMetadata(),
	}, cortexdb.GraphRAGIngestOptions{
		Collection:   req.GetCollection(),
		ChunkSize:    int(req.GetChunkSize()),
		ChunkOverlap: int(req.GetChunkOverlap()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.InsertGraphDocumentResponse{
		DocumentNodeId: resp.DocumentNodeID,
		ChunkNodeIds:   resp.ChunkNodeIDs,
		EntityNodeIds:  resp.EntityNodeIDs,
	}, nil
}

func (s *graphragService) SearchGraphRag(ctx context.Context, req *rpcv1.SearchGraphRagRequest) (*rpcv1.SearchGraphRagResponse, error) {
	resp, err := s.db.SearchGraphRAG(ctx, req.GetQuery(), cortexdb.GraphRAGQueryOptions{
		Collection:          req.GetCollection(),
		TopK:                int(req.GetTopK()),
		MaxHops:             int(req.GetMaxHops()),
		MaxRelatedChunks:    int(req.GetMaxRelatedChunks()),
		MaxContextChunks:    int(req.GetMaxContextChunks()),
		MaxContextChars:     int(req.GetMaxContextChars()),
		PerDocumentLimit:    int(req.GetPerDocumentLimit()),
		Rerank:              req.GetRerank(),
		DisableRerank:       req.GetDisableRerank(),
		DiversityLambda:     req.GetDiversityLambda(),
		DisableGraph:        req.GetDisableGraph(),
		RetrievalMode:       req.GetRetrievalMode(),
		GraphLight:          req.GetGraphLight(),
		MaxExpansionSeeds:   int(req.GetMaxExpansionSeeds()),
		MaxTraversalNodes:   int(req.GetMaxTraversalNodes()),
		MaxEntitiesPerChunk: int(req.GetMaxEntitiesPerChunk()),
		Plan:                planFromProto(req.GetPlan()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.SearchGraphRagResponse{
		Query:    resp.Query,
		Plan:     planToProto(resp.Plan),
		Decision: decisionToProto(resp.Decision),
		Chunks:   chunksToProto(resp.Chunks),
		Entities: resp.Entities,
		Context:  resp.Context,
	}, nil
}

func (s *graphragService) InsertText(ctx context.Context, req *rpcv1.InsertTextRequest) (*rpcv1.InsertTextResponse, error) {
	if err := s.db.InsertText(ctx, req.GetId(), req.GetText(), req.GetMetadata()); err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.InsertTextResponse{}, nil
}

func (s *graphragService) InsertTextBatch(ctx context.Context, req *rpcv1.InsertTextBatchRequest) (*rpcv1.InsertTextBatchResponse, error) {
	if err := s.db.InsertTextBatch(ctx, req.GetTexts(), req.GetMetadata()); err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.InsertTextBatchResponse{}, nil
}

func (s *graphragService) SearchText(ctx context.Context, req *rpcv1.SearchTextRequest) (*rpcv1.SearchTextResponse, error) {
	var (
		results []core.ScoredEmbedding
		err     error
	)
	if collection := req.GetCollection(); collection != "" {
		results, err = s.db.SearchTextInCollection(ctx, collection, req.GetQuery(), int(req.GetTopK()))
	} else {
		results, err = s.db.SearchText(ctx, req.GetQuery(), int(req.GetTopK()))
	}
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.SearchTextResponse{Results: scoredTextsToProto(results)}, nil
}

func (s *graphragService) HybridSearchText(ctx context.Context, req *rpcv1.HybridSearchTextRequest) (*rpcv1.HybridSearchTextResponse, error) {
	results, err := s.db.HybridSearchText(ctx, req.GetQuery(), int(req.GetTopK()))
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.HybridSearchTextResponse{Results: scoredTextsToProto(results)}, nil
}
