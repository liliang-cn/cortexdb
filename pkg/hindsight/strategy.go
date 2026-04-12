package hindsight

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

// StrategyOptions configures the Hindsight memoryflow recall strategy.
type StrategyOptions struct {
	// BankID identifies the Hindsight memory bank used as a cognitive recall scope.
	BankID string
	// Namespace overrides the derived memory namespace. Defaults to hindsight:<bank>.
	Namespace string
	// EntityNames are always added to the recall plan as Hindsight entity cues.
	EntityNames []string
	// Keywords are always added to the recall plan as Hindsight lexical cues.
	Keywords []string
	// RetrievalMode overrides the recall retrieval mode. Defaults to graph when
	// UseKG is true and entity cues exist, otherwise lexical.
	RetrievalMode string
	// UseKG asks CortexDB's normal recall path to prefer graph-aware retrieval when
	// entity cues are available. It does not write to the KG.
	UseKG bool
}

// Strategy is a memoryflow recall strategy plugin. It does not replace
// memoryflow; it enriches recall requests with Hindsight-style bank, entity, and
// lexical cues, then delegates to the normal memoryflow recall path.
type Strategy struct {
	db   *cortexdb.DB
	opts StrategyOptions
}

// NewStrategy returns a memoryflow-compatible Hindsight recall strategy.
func NewStrategy(db *cortexdb.DB, opts StrategyOptions) *Strategy {
	opts.BankID = strings.TrimSpace(opts.BankID)
	opts.Namespace = strings.TrimSpace(opts.Namespace)
	if opts.Namespace == "" && opts.BankID != "" {
		opts.Namespace = hindsightMemoryNamespace(opts.BankID)
	}
	return &Strategy{db: db, opts: opts}
}

// Recall enriches the request with Hindsight strategy cues and delegates to next.
func (s *Strategy) Recall(ctx context.Context, req memoryflow.RecallRequest, next memoryflow.RecallFunc) (*memoryflow.RecallResponse, error) {
	if next == nil {
		return nil, cortexdb.ErrEmptyText
	}
	req = s.enrichRecallRequest(req)
	return next(ctx, req)
}

func (s *Strategy) enrichRecallRequest(req memoryflow.RecallRequest) memoryflow.RecallRequest {
	_ = s.db // Reserved for later KG-aware strategy phases.
	if strings.TrimSpace(req.Namespace) == "" && s.opts.Namespace != "" {
		req.Namespace = s.opts.Namespace
	}
	if strings.TrimSpace(req.State.Namespace) == "" && s.opts.Namespace != "" {
		req.State.Namespace = s.opts.Namespace
	}

	plan := cortexdb.RetrievalPlan{Query: req.Query}
	if req.Plan != nil {
		plan = *req.Plan
		if strings.TrimSpace(plan.Query) == "" {
			plan.Query = req.Query
		}
	}
	plan.Keywords = mergeStrategyStrings(plan.Keywords, s.opts.Keywords)
	plan.EntityNames = mergeStrategyStrings(plan.EntityNames, s.opts.EntityNames)
	plan.Keywords = mergeStrategyStrings(plan.Keywords, req.State.Tags, req.State.Taxonomy.Tags)
	plan.EntityNames = mergeStrategyStrings(plan.EntityNames, req.State.Taxonomy.Entities)

	if s.opts.BankID != "" {
		plan.Keywords = mergeStrategyStrings(plan.Keywords, s.opts.BankID)
	}
	if req.State.Wing != "" || req.State.Room != "" {
		plan.Keywords = mergeStrategyStrings(plan.Keywords, req.State.Wing, req.State.Room)
	}
	if s.opts.RetrievalMode != "" {
		plan.RetrievalMode = s.opts.RetrievalMode
	} else if s.opts.UseKG && len(plan.EntityNames) > 0 {
		plan.RetrievalMode = cortexdb.RetrievalModeGraph
	} else if plan.RetrievalMode == "" {
		plan.RetrievalMode = cortexdb.RetrievalModeLexical
	}

	req.Plan = &plan
	if req.RetrievalMode == "" {
		req.RetrievalMode = plan.RetrievalMode
	}
	return req
}

func hindsightMemoryNamespace(bankID string) string {
	return "hindsight:" + sanitizeBankID(bankID)
}

func sanitizeBankID(bankID string) string {
	bankID = strings.TrimSpace(strings.ToLower(bankID))
	if bankID == "" {
		return "default"
	}
	re := regexp.MustCompile(`[^a-z0-9_-]+`)
	value := strings.Trim(re.ReplaceAllString(bankID, "-"), "-_")
	if value == "" {
		value = "bank"
	}
	if len(value) <= 48 {
		return value
	}
	hash := sha1.Sum([]byte(bankID))
	return value[:40] + "-" + hex.EncodeToString(hash[:])[:8]
}

func mergeStrategyStrings(groups ...any) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, group := range groups {
		switch values := group.(type) {
		case string:
			addStrategyString(&out, seen, values)
		case []string:
			for _, value := range values {
				addStrategyString(&out, seen, value)
			}
		}
	}
	return out
}

func addStrategyString(out *[]string, seen map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, value)
}
