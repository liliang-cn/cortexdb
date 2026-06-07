package rpcserver

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type knowledgeService struct {
	rpcv1.UnimplementedKnowledgeServiceServer
	db *cortexdb.DB
}

func knowledgeRecordToProto(r cortexdb.KnowledgeRecord) *rpcv1.KnowledgeRecord {
	return &rpcv1.KnowledgeRecord{
		Id:         r.ID,
		Title:      r.Title,
		Content:    r.Content,
		SourceUrl:  r.SourceURL,
		Author:     r.Author,
		Collection: r.Collection,
		Metadata:   r.Metadata,
		ChunkIds:   r.ChunkIDs,
		Entities:   r.Entities,
		CreatedAt:  tsFromTime(r.CreatedAt),
		UpdatedAt:  tsFromTime(r.UpdatedAt),
	}
}

func saveResponseToProto(resp *cortexdb.KnowledgeSaveResponse) *rpcv1.SaveKnowledgeResponse {
	return &rpcv1.SaveKnowledgeResponse{
		Knowledge:       knowledgeRecordToProto(resp.Knowledge),
		DocumentNodeId:  resp.DocumentNodeID,
		EntityNodeIds:   resp.EntityNodeIDs,
		RelationEdgeIds: resp.RelationEdgeIDs,
	}
}

func (s *knowledgeService) SaveKnowledge(ctx context.Context, req *rpcv1.SaveKnowledgeRequest) (*rpcv1.SaveKnowledgeResponse, error) {
	resp, err := s.db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
		KnowledgeID:  req.GetKnowledgeId(),
		Title:        req.GetTitle(),
		Content:      req.GetContent(),
		SourceURL:    req.GetSourceUrl(),
		Author:       req.GetAuthor(),
		Collection:   req.GetCollection(),
		ChunkSize:    int(req.GetChunkSize()),
		ChunkOverlap: int(req.GetChunkOverlap()),
		Metadata:     req.GetMetadata(),
		Entities:     entityInputsFromProto(req.GetEntities()),
		Relations:    relationInputsFromProto(req.GetRelations()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return saveResponseToProto(resp), nil
}

func (s *knowledgeService) UpdateKnowledge(ctx context.Context, req *rpcv1.UpdateKnowledgeRequest) (*rpcv1.SaveKnowledgeResponse, error) {
	in := cortexdb.KnowledgeUpdateRequest{
		KnowledgeID: req.GetKnowledgeId(),
		Metadata:    req.GetMetadata(),
		Entities:    entityInputsFromProto(req.GetEntities()),
		Relations:   relationInputsFromProto(req.GetRelations()),
	}
	in.Title = req.Title
	in.Content = req.Content
	in.SourceURL = req.SourceUrl
	in.Author = req.Author
	in.Collection = req.Collection
	if req.ChunkSize != nil {
		v := int(*req.ChunkSize)
		in.ChunkSize = &v
	}
	if req.ChunkOverlap != nil {
		v := int(*req.ChunkOverlap)
		in.ChunkOverlap = &v
	}
	resp, err := s.db.UpdateKnowledge(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return saveResponseToProto(resp), nil
}

func (s *knowledgeService) GetKnowledge(ctx context.Context, req *rpcv1.GetKnowledgeRequest) (*rpcv1.GetKnowledgeResponse, error) {
	resp, err := s.db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: req.GetKnowledgeId()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.GetKnowledgeResponse{Knowledge: knowledgeRecordToProto(resp.Knowledge)}, nil
}

func (s *knowledgeService) SearchKnowledge(ctx context.Context, req *rpcv1.SearchKnowledgeRequest) (*rpcv1.SearchKnowledgeResponse, error) {
	resp, err := s.db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		Query:               req.GetQuery(),
		Collection:          req.GetCollection(),
		TopK:                int(req.GetTopK()),
		MaxHops:             int(req.GetMaxHops()),
		MaxRelatedChunks:    int(req.GetMaxRelatedChunks()),
		MaxContextChunks:    int(req.GetMaxContextChunks()),
		MaxContextChars:     int(req.GetMaxContextChars()),
		PerDocumentLimit:    int(req.GetPerDocumentLimit()),
		DiversityLambda:     req.GetDiversityLambda(),
		DisableRerank:       req.GetDisableRerank(),
		EntityNames:         req.GetEntityNames(),
		Keywords:            req.GetKeywords(),
		AlternateQueries:    req.GetAlternateQueries(),
		RetrievalMode:       req.GetRetrievalMode(),
		DisableGraph:        req.GetDisableGraph(),
		GraphLight:          req.GetGraphLight(),
		MaxExpansionSeeds:   int(req.GetMaxExpansionSeeds()),
		MaxTraversalNodes:   int(req.GetMaxTraversalNodes()),
		MaxEntitiesPerChunk: int(req.GetMaxEntitiesPerChunk()),
		Plan:                planFromProto(req.GetPlan()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	hits := make([]*rpcv1.KnowledgeSearchHit, 0, len(resp.Results))
	for _, h := range resp.Results {
		hits = append(hits, &rpcv1.KnowledgeSearchHit{
			KnowledgeId: h.KnowledgeID,
			Title:       h.Title,
			SourceUrl:   h.SourceURL,
			Author:      h.Author,
			Snippet:     h.Snippet,
			Score:       h.Score,
			ChunkIds:    h.ChunkIDs,
			Entities:    h.Entities,
			Metadata:    h.Metadata,
		})
	}
	return &rpcv1.SearchKnowledgeResponse{
		Query:    resp.Query,
		Plan:     planToProto(resp.Plan),
		Decision: decisionToProto(resp.Decision),
		Results:  hits,
		Chunks:   chunksToProto(resp.Chunks),
		Entities: resp.Entities,
		Context:  resp.Context,
	}, nil
}

func (s *knowledgeService) DeleteKnowledge(ctx context.Context, req *rpcv1.DeleteKnowledgeRequest) (*rpcv1.DeleteKnowledgeResponse, error) {
	resp, err := s.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: req.GetKnowledgeId()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.DeleteKnowledgeResponse{KnowledgeId: resp.KnowledgeID, Deleted: resp.Deleted}, nil
}
