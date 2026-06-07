package rpcserver

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type memoryService struct {
	rpcv1.UnimplementedMemoryServiceServer
	db *cortexdb.DB
}

func memoryRecordToProto(r cortexdb.MemoryRecord) *rpcv1.MemoryRecord {
	rec := &rpcv1.MemoryRecord{
		Id:         r.ID,
		UserId:     r.UserID,
		SessionId:  r.SessionID,
		Scope:      r.Scope,
		Namespace:  r.Namespace,
		Role:       r.Role,
		Content:    r.Content,
		Metadata:   structFromAnyMap(r.Metadata),
		Importance: r.Importance,
		TtlSeconds: int32(r.TTLSeconds),
		CreatedAt:  tsFromTime(r.CreatedAt),
	}
	if r.ExpiresAt != nil {
		rec.ExpiresAt = tsFromTime(*r.ExpiresAt)
	}
	return rec
}

func (s *memoryService) SaveMemory(ctx context.Context, req *rpcv1.SaveMemoryRequest) (*rpcv1.SaveMemoryResponse, error) {
	resp, err := s.db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
		MemoryID:   req.GetMemoryId(),
		UserID:     req.GetUserId(),
		SessionID:  req.GetSessionId(),
		Scope:      req.GetScope(),
		Namespace:  req.GetNamespace(),
		Role:       req.GetRole(),
		Content:    req.GetContent(),
		Metadata:   anyMapFromStruct(req.GetMetadata()),
		Importance: req.GetImportance(),
		TTLSeconds: int(req.GetTtlSeconds()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.SaveMemoryResponse{Memory: memoryRecordToProto(resp.Memory)}, nil
}

func (s *memoryService) UpdateMemory(ctx context.Context, req *rpcv1.UpdateMemoryRequest) (*rpcv1.SaveMemoryResponse, error) {
	in := cortexdb.MemoryUpdateRequest{
		MemoryID: req.GetMemoryId(),
		Metadata: anyMapFromStruct(req.GetMetadata()),
	}
	in.Content = req.Content
	in.Importance = req.Importance
	if req.TtlSeconds != nil {
		v := int(*req.TtlSeconds)
		in.TTLSeconds = &v
	}
	resp, err := s.db.UpdateMemory(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.SaveMemoryResponse{Memory: memoryRecordToProto(resp.Memory)}, nil
}

func (s *memoryService) GetMemory(ctx context.Context, req *rpcv1.GetMemoryRequest) (*rpcv1.GetMemoryResponse, error) {
	resp, err := s.db.GetMemory(ctx, cortexdb.MemoryGetRequest{MemoryID: req.GetMemoryId()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.GetMemoryResponse{Memory: memoryRecordToProto(resp.Memory)}, nil
}

func (s *memoryService) SearchMemory(ctx context.Context, req *rpcv1.SearchMemoryRequest) (*rpcv1.SearchMemoryResponse, error) {
	resp, err := s.db.SearchMemory(ctx, cortexdb.MemorySearchRequest{
		Query:            req.GetQuery(),
		UserID:           req.GetUserId(),
		SessionID:        req.GetSessionId(),
		Scope:            req.GetScope(),
		Namespace:        req.GetNamespace(),
		TopK:             int(req.GetTopK()),
		Keywords:         req.GetKeywords(),
		AlternateQueries: req.GetAlternateQueries(),
		RetrievalMode:    req.GetRetrievalMode(),
		Plan:             planFromProto(req.GetPlan()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	hits := make([]*rpcv1.MemorySearchHit, 0, len(resp.Results))
	for _, h := range resp.Results {
		hits = append(hits, &rpcv1.MemorySearchHit{
			Memory: memoryRecordToProto(h.Memory),
			Score:  h.Score,
		})
	}
	return &rpcv1.SearchMemoryResponse{
		Query:    resp.Query,
		Plan:     planToProto(resp.Plan),
		Decision: decisionToProto(resp.Decision),
		Results:  hits,
	}, nil
}

func (s *memoryService) DeleteMemory(ctx context.Context, req *rpcv1.DeleteMemoryRequest) (*rpcv1.DeleteMemoryResponse, error) {
	resp, err := s.db.DeleteMemory(ctx, cortexdb.MemoryDeleteRequest{MemoryID: req.GetMemoryId()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.DeleteMemoryResponse{MemoryId: resp.MemoryID, Deleted: resp.Deleted}, nil
}
