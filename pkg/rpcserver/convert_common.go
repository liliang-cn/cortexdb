package rpcserver

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func tsFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func structFromAnyMap(m map[string]any) *structpb.Struct {
	if len(m) == 0 {
		return nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil // non-JSON-able values cannot cross the wire; drop
	}
	return s
}

func anyMapFromStruct(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

func planFromProto(p *rpcv1.RetrievalPlan) *cortexdb.RetrievalPlan {
	if p == nil {
		return nil
	}
	out := &cortexdb.RetrievalPlan{
		Query:            p.GetQuery(),
		Keywords:         p.GetKeywords(),
		AlternateQueries: p.GetAlternateQueries(),
		EntityNames:      p.GetEntityNames(),
		RetrievalMode:    p.GetRetrievalMode(),
	}
	if f := p.GetFilters(); f != nil {
		out.Filters = &cortexdb.RetrievalFilters{
			Collection:  f.GetCollection(),
			DocumentIDs: f.GetDocumentIds(),
			UserID:      f.GetUserId(),
			SessionID:   f.GetSessionId(),
			Scope:       f.GetScope(),
			Namespace:   f.GetNamespace(),
		}
	}
	return out
}

func planToProto(p cortexdb.RetrievalPlan) *rpcv1.RetrievalPlan {
	out := &rpcv1.RetrievalPlan{
		Query:            p.Query,
		Keywords:         p.Keywords,
		AlternateQueries: p.AlternateQueries,
		EntityNames:      p.EntityNames,
		RetrievalMode:    p.RetrievalMode,
	}
	if p.Filters != nil {
		out.Filters = &rpcv1.RetrievalFilters{
			Collection:  p.Filters.Collection,
			DocumentIds: p.Filters.DocumentIDs,
			UserId:      p.Filters.UserID,
			SessionId:   p.Filters.SessionID,
			Scope:       p.Filters.Scope,
			Namespace:   p.Filters.Namespace,
		}
	}
	return out
}

func decisionToProto(d cortexdb.RetrievalDecision) *rpcv1.RetrievalDecision {
	return &rpcv1.RetrievalDecision{
		RequestedMode: d.RequestedMode,
		EffectiveMode: d.EffectiveMode,
		UseGraph:      d.UseGraph,
		Reason:        d.Reason,
	}
}

func chunksToProto(chunks []cortexdb.GraphRAGChunkResult) []*rpcv1.GraphRagChunkResult {
	out := make([]*rpcv1.GraphRagChunkResult, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, &rpcv1.GraphRagChunkResult{
			Id:          c.ID,
			DocumentId:  c.DocumentID,
			Content:     c.Content,
			Score:       c.Score,
			BaseScore:   c.BaseScore,
			RerankScore: c.RerankScore,
			Entities:    c.Entities,
		})
	}
	return out
}

func entityInputsFromProto(in []*rpcv1.ToolEntityInput) []cortexdb.ToolEntityInput {
	out := make([]cortexdb.ToolEntityInput, 0, len(in))
	for _, e := range in {
		out = append(out, cortexdb.ToolEntityInput{
			ID:          e.GetId(),
			Name:        e.GetName(),
			Type:        e.GetType(),
			Description: e.GetDescription(),
			ChunkIDs:    e.GetChunkIds(),
			Metadata:    e.GetMetadata(),
		})
	}
	return out
}

func relationInputsFromProto(in []*rpcv1.ToolRelationInput) []cortexdb.ToolRelationInput {
	out := make([]cortexdb.ToolRelationInput, 0, len(in))
	for _, r := range in {
		out = append(out, cortexdb.ToolRelationInput{
			From:           r.GetFrom(),
			To:             r.GetTo(),
			Type:           r.GetType(),
			Weight:         r.GetWeight(),
			ChunkIDs:       r.GetChunkIds(),
			Metadata:       r.GetMetadata(),
			Inferred:       r.GetInferred(),
			Provenance:     r.GetProvenance(),
			RuleID:         r.GetRuleId(),
			SupportEdgeIDs: r.GetSupportEdgeIds(),
		})
	}
	return out
}
