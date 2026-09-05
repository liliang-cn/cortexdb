package rpcserver

// The decision ledger over gRPC. Conversion only, as the other services here
// are: every rule about what a decision may be — the actor it needs, the
// premises that must exist, the contract it has to satisfy — lives in
// pkg/cortexdb, so a Rust or Node client gets the same refusals as a Go one
// rather than a second, drifting copy of the policy.

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type decisionService struct {
	rpcv1.UnimplementedDecisionServiceServer
	db *cortexdb.DB
}

func ledgerPremiseToProto(p cortexdb.DecisionPremise) *rpcv1.DecisionPremise {
	return &rpcv1.DecisionPremise{
		Id:       p.ID,
		Edge:     p.Edge,
		Type:     p.Type,
		Content:  p.Content,
		From:     p.From,
		To:       p.To,
		Decision: p.Decision,
		Grade:    p.Grade,
		Source:   p.Source,
		Missing:  p.Missing,
	}
}

func ledgerDecisionToProto(d cortexdb.DecisionRecord) *rpcv1.Decision {
	out := &rpcv1.Decision{
		Id:         d.ID,
		Kind:       d.Kind,
		Actor:      d.Actor,
		At:         d.At,
		Verdict:    d.Verdict,
		Subject:    d.Subject,
		Note:       d.Note,
		Grade:      d.Grade,
		Source:     d.Source,
		Producer:   d.Producer,
		State:      d.State,
		Why:        d.Why,
		Supersedes: d.Supersedes,
	}
	for _, p := range d.Premises {
		out.Premises = append(out.Premises, ledgerPremiseToProto(p))
	}
	return out
}

// RecordDecision delegates to RecordDecisionTool rather than to
// RecordDecision, so the RFC 3339 parse of `at` is the one in pkg/cortexdb.
// Parsing it here would be a second place for a timestamp to be accepted that
// the library refuses — and the wrong version, one that quietly falls back to
// now, looks right in every test that does not pass a malformed time.
func (s *decisionService) RecordDecision(ctx context.Context, req *rpcv1.RecordDecisionRequest) (*rpcv1.RecordDecisionResponse, error) {
	rec, err := s.db.RecordDecisionTool(ctx, cortexdb.DecisionRecordToolRequest{
		ID:         req.GetId(),
		Kind:       req.GetKind(),
		Actor:      req.GetActor(),
		Note:       req.GetNote(),
		Verdict:    req.GetVerdict(),
		Subject:    req.GetSubject(),
		Premises:   req.GetPremises(),
		Supersedes: req.GetSupersedes(),
		At:         req.GetAt(),
		Source:     req.GetSource(),
		Producer:   req.GetProducer(),
		Grade:      req.GetGrade(),
		State:      req.GetState(),
		Why:        req.GetWhy(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.RecordDecisionResponse{Decision: ledgerDecisionToProto(rec.Decision)}, nil
}

func (s *decisionService) DecisionChain(ctx context.Context, req *rpcv1.DecisionChainRequest) (*rpcv1.DecisionChainResponse, error) {
	chain, err := s.db.DecisionChain(ctx, req.GetId(), int(req.GetMaxDepth()))
	if err != nil {
		return nil, toStatus(err)
	}
	out := &rpcv1.DecisionChainResponse{
		Root:      chain.Root,
		Depth:     int32(chain.Depth),
		Truncated: chain.Truncated,
		Decisions: make([]*rpcv1.Decision, 0, len(chain.Decisions)),
	}
	for _, d := range chain.Decisions {
		out.Decisions = append(out.Decisions, ledgerDecisionToProto(d))
	}
	return out, nil
}

// Precedents delegates to PrecedentsTool for the truncation reporting, which
// is the part a capped caller cannot reconstruct: asking for one more row than
// wanted is how it knows it stopped short. Reimplementing it here would be a
// second place for that flag to be wrong, and the wrong version — never
// truncated — passes every test that does not overflow the cap.
func (s *decisionService) Precedents(ctx context.Context, req *rpcv1.PrecedentsRequest) (*rpcv1.PrecedentsResponse, error) {
	res, err := s.db.PrecedentsTool(ctx, cortexdb.DecisionPrecedentsToolRequest{
		Kind:    req.GetKind(),
		Subject: req.GetSubject(),
		Exclude: req.GetExclude(),
		Limit:   int(req.GetLimit()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	out := &rpcv1.PrecedentsResponse{
		Count:     int32(res.Count),
		Truncated: res.Truncated,
		Decisions: make([]*rpcv1.Decision, 0, len(res.Decisions)),
	}
	for _, d := range res.Decisions {
		out.Decisions = append(out.Decisions, ledgerDecisionToProto(d))
	}
	return out, nil
}
