package cortexdb

import (
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func resolveRetrievalPlan(input retrievalPlanInput) retrievalPlanResolution {
	var existingPlan RetrievalPlan
	if input.Plan != nil {
		existingPlan = *input.Plan
	}

	// Folded in before the filters are merged, so everything downstream sees one spelling.
	if strings.TrimSpace(existingPlan.Collection) != "" {
		if existingPlan.Filters == nil {
			existingPlan.Filters = &RetrievalFilters{}
		}
		if strings.TrimSpace(existingPlan.Filters.Collection) == "" {
			existingPlan.Filters.Collection = strings.TrimSpace(existingPlan.Collection)
		}
	}

	plan := RetrievalPlan{
		Query:            firstNonEmpty(input.Query, existingPlan.Query),
		Keywords:         mergeUniqueStrings(input.Keywords, existingPlan.Keywords),
		AlternateQueries: mergeUniqueStrings(input.AlternateQueries, existingPlan.AlternateQueries),
		EntityNames:      mergeUniqueStrings(input.EntityNames, existingPlan.EntityNames),
		RetrievalMode:    normalizeRetrievalMode(firstNonEmpty(input.RetrievalMode, existingPlan.RetrievalMode)),
		Filters:          mergeRetrievalFilters(existingPlan.Filters, input.Filters),
	}

	decision := resolveRetrievalDecision(
		plan.RetrievalMode,
		input.DisableGraph,
		plan.Query,
		plan.EntityNames,
		input.SupportsGraph,
		input.EmptyQueryUsesGraph,
		input.UnsupportedEffectiveMode,
		input.UnsupportedReason,
		input.PreferSemantic,
		input.GraphRequiresEntityNames,
	)

	return retrievalPlanResolution{
		Plan:     plan,
		Decision: decision,
	}
}

func normalizeRetrievalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", RetrievalModeAuto:
		return RetrievalModeAuto
	case RetrievalModeLexical:
		return RetrievalModeLexical
	case RetrievalModeGraph:
		return RetrievalModeGraph
	default:
		return RetrievalModeAuto
	}
}

func resolveRetrievalDecision(mode string, disableGraph bool, query string, entityNames []string, supportsGraph bool, emptyQueryUsesGraph bool, unsupportedEffectiveMode string, unsupportedReason string, preferSemantic bool, graphRequiresEntityNames bool) RetrievalDecision {
	requested := normalizeRetrievalMode(mode)
	decision := RetrievalDecision{
		RequestedMode: requested,
		EffectiveMode: requested,
	}

	if disableGraph {
		decision.EffectiveMode = RetrievalModeLexical
		decision.Reason = "graph disabled by request"
		return decision
	}

	if !supportsGraph {
		effective := normalizeRetrievalMode(unsupportedEffectiveMode)
		if requested == RetrievalModeLexical {
			effective = RetrievalModeLexical
		}
		if effective == RetrievalModeGraph {
			effective = RetrievalModeAuto
		}
		if effective == "" {
			effective = RetrievalModeLexical
		}
		decision.EffectiveMode = effective
		if requested == RetrievalModeGraph {
			decision.Reason = "graph mode requested, but this API does not use graph expansion"
		} else {
			decision.Reason = firstNonEmpty(unsupportedReason, "this API does not use graph expansion")
		}
		return decision
	}

	switch requested {
	case RetrievalModeLexical:
		decision.EffectiveMode = RetrievalModeLexical
		decision.Reason = "lexical mode requested"
	case RetrievalModeGraph:
		decision.EffectiveMode = RetrievalModeGraph
		decision.UseGraph = true
		decision.Reason = "graph mode requested explicitly"
	default:
		if len(entityNames) > 0 {
			decision.EffectiveMode = RetrievalModeGraph
			decision.UseGraph = true
			decision.Reason = "auto mode used graph because entity_names were provided"
			return decision
		}
		if strings.TrimSpace(query) == "" {
			if emptyQueryUsesGraph {
				decision.EffectiveMode = RetrievalModeGraph
				decision.UseGraph = true
				decision.Reason = "auto mode used graph because chunk enrichment was requested without query text"
			} else {
				decision.EffectiveMode = RetrievalModeLexical
				decision.Reason = "auto mode stayed lexical because no entity signal was found"
			}
			return decision
		}
		if !graphRequiresEntityNames && len(extractEntityNames(extractTitleEntities(query))) > 0 {
			decision.EffectiveMode = RetrievalModeGraph
			decision.UseGraph = true
			decision.Reason = "auto mode detected entity-like terms in the query"
			return decision
		}
		if preferSemantic {
			// Keep a non-lexical effective mode so the caller routes to vector
			// (semantic) retrieval; auto silently falling back to lexical made a
			// configured embedder useless on the knowledge path.
			decision.EffectiveMode = RetrievalModeAuto
			decision.Reason = "auto mode used semantic vector search because an embedder is available"
		} else {
			decision.EffectiveMode = RetrievalModeLexical
			decision.Reason = "auto mode stayed lexical because no entity signal was found"
		}
	}

	return decision
}

func shouldUseGraphRetrieval(mode string, disableGraph bool, query string, entityNames []string) bool {
	return resolveRetrievalDecision(mode, disableGraph, query, entityNames, true, false, RetrievalModeLexical, "", false, false).UseGraph
}

func shouldLoadChunkEntities(mode string, disableGraph bool, query string) bool {
	return resolveRetrievalDecision(mode, disableGraph, query, nil, true, true, RetrievalModeLexical, "", false, false).UseGraph
}

func mergeRetrievalFilters(base, override *RetrievalFilters) *RetrievalFilters {
	if base == nil && override == nil {
		return nil
	}

	merged := RetrievalFilters{}
	if base != nil {
		merged.Collection = strings.TrimSpace(base.Collection)
		merged.DocumentIDs = mergeUniqueStrings(base.DocumentIDs)
		merged.UserID = strings.TrimSpace(base.UserID)
		merged.SessionID = strings.TrimSpace(base.SessionID)
		merged.Scope = strings.TrimSpace(base.Scope)
		merged.Namespace = strings.TrimSpace(base.Namespace)
	}
	if override != nil {
		if value := strings.TrimSpace(override.Collection); value != "" {
			merged.Collection = value
		}
		merged.DocumentIDs = mergeUniqueStrings(override.DocumentIDs, merged.DocumentIDs)
		if value := strings.TrimSpace(override.UserID); value != "" {
			merged.UserID = value
		}
		if value := strings.TrimSpace(override.SessionID); value != "" {
			merged.SessionID = value
		}
		if value := strings.TrimSpace(override.Scope); value != "" {
			merged.Scope = value
		}
		if value := strings.TrimSpace(override.Namespace); value != "" {
			merged.Namespace = value
		}
	}
	if retrievalFiltersEmpty(merged) {
		return nil
	}
	return &merged
}

func retrievalFiltersEmpty(filters RetrievalFilters) bool {
	return filters.Collection == "" &&
		len(filters.DocumentIDs) == 0 &&
		filters.UserID == "" &&
		filters.SessionID == "" &&
		filters.Scope == "" &&
		filters.Namespace == ""
}

func mergeUniqueStrings(groups ...[]string) []string {
	var merged []string
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
		}
	}
	return merged
}

func applyRetrievalPlanCollection(collection string, filters *RetrievalFilters) string {
	if value := strings.TrimSpace(collection); value != "" {
		return value
	}
	if filters == nil {
		return ""
	}
	return strings.TrimSpace(filters.Collection)
}

func filterScoredEmbeddings(results []core.ScoredEmbedding, filters *RetrievalFilters) []core.ScoredEmbedding {
	if len(results) == 0 || filters == nil || len(filters.DocumentIDs) == 0 {
		return results
	}

	allowed := make(map[string]struct{}, len(filters.DocumentIDs))
	for _, documentID := range filters.DocumentIDs {
		if documentID == "" {
			continue
		}
		allowed[documentID] = struct{}{}
	}
	if len(allowed) == 0 {
		return results
	}

	filtered := make([]core.ScoredEmbedding, 0, len(results))
	for _, result := range results {
		if _, ok := allowed[result.DocID]; ok {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func allowDocumentID(filters *RetrievalFilters, documentID string) bool {
	if filters == nil || len(filters.DocumentIDs) == 0 {
		return true
	}
	for _, candidate := range filters.DocumentIDs {
		if candidate == documentID {
			return true
		}
	}
	return false
}
