package rpcserver

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type contractService struct {
	rpcv1.UnimplementedContractServiceServer
	db *cortexdb.DB
}

// gradeCountToProto widens the library's int counts to int64 on the wire.
// A shelf big enough to overflow an int32 is a shelf whose tally still has to
// be right, and the cost of the wider field is nothing.
func gradeCountToProto(c graph.PropertyCount) *rpcv1.GradeCount {
	return &rpcv1.GradeCount{Nodes: int64(c.Nodes), Edges: int64(c.Edges)}
}

func gradedRecordToProto(r cortexdb.GradedRecord) *rpcv1.GradedRecord {
	return &rpcv1.GradedRecord{
		Id:       r.ID,
		Edge:     r.Edge,
		Type:     r.Type,
		Content:  r.Content,
		From:     r.From,
		To:       r.To,
		Grade:    r.Grade,
		State:    r.State,
		Why:      r.Why,
		Source:   r.Source,
		Producer: r.Producer,
		At:       r.At,
		By:       r.By,
	}
}

func (s *contractService) ContractTally(ctx context.Context, _ *rpcv1.ContractTallyRequest) (*rpcv1.ContractTallyResponse, error) {
	t, err := s.db.ContractTally(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &rpcv1.ContractTallyResponse{
		Verified:       gradeCountToProto(t.Verified),
		SelfConsistent: gradeCountToProto(t.SelfConsistent),
		Asserted:       gradeCountToProto(t.Asserted),
		Held:           gradeCountToProto(t.Held),
		Refused:        gradeCountToProto(t.Refused),
		Untagged:       gradeCountToProto(t.Untagged),
	}
	// Left nil when empty rather than an allocated empty map: proto3 encodes
	// both as an absent field, and "no producer is writing a grade we do not
	// know" is exactly what a reader wants to see nothing about.
	if len(t.Unknown) > 0 {
		resp.Unknown = make(map[string]*rpcv1.GradeCount, len(t.Unknown))
		for grade, c := range t.Unknown {
			resp.Unknown[grade] = gradeCountToProto(c)
		}
	}
	return resp, nil
}

// NeedsAttention delegates to NeedsAttentionTool rather than to NeedsAttention,
// because the truncation reporting is the part a capped caller cannot
// reconstruct: asking for one more row than wanted is how it knows it stopped
// short, and the true total then comes from the tally. Reimplementing that
// here would be a second place for the number to be wrong, and the wrong
// version — a total equal to the rows returned — looks right in every test
// that does not overflow the cap.
func (s *contractService) NeedsAttention(ctx context.Context, req *rpcv1.NeedsAttentionRequest) (*rpcv1.NeedsAttentionResponse, error) {
	res, err := s.db.NeedsAttentionTool(ctx, cortexdb.NeedsAttentionRequest{Limit: int(req.GetLimit())})
	if err != nil {
		return nil, toStatus(err)
	}
	out := &rpcv1.NeedsAttentionResponse{
		Records:   make([]*rpcv1.GradedRecord, 0, len(res.Records)),
		Truncated: res.Truncated,
		Total:     int64(res.Total),
	}
	for _, r := range res.Records {
		out.Records = append(out.Records, gradedRecordToProto(r))
	}
	return out, nil
}
