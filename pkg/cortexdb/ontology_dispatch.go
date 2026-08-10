package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
)

func (t *GraphRAGToolbox) callOntologyTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "ontology_save":
		var req OntologySaveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.SaveOntologySchema(ctx, req)
		return resp, true, err
	case "ontology_get":
		var req OntologyGetRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.GetOntologySchema(ctx, req)
		return resp, true, err
	case "ontology_list":
		var req OntologyListRequest
		if len(input) > 0 {
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, true, fmt.Errorf("decode %s: %w", name, err)
			}
		}
		resp, err := t.ListOntologySchemas(ctx, req)
		return resp, true, err
	case "ontology_diff":
		var req OntologyDiffRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.DiffOntologySchema(ctx, req)
		return resp, true, err
	case "object_set_resolve":
		var req ObjectSetResolveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ResolveObjectSet(ctx, req)
		return resp, true, err
	case "ontology_action_list":
		var req ActionListRequest
		if len(input) > 0 {
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, true, fmt.Errorf("decode %s: %w", name, err)
			}
		}
		resp, err := t.ListActionTypes(ctx, req)
		return resp, true, err
	case "ontology_action_apply":
		var req ActionApplyRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ApplyAction(ctx, req)
		return resp, true, err
	case "ontology_delete":
		var req OntologyDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.DeleteOntologySchema(ctx, req)
		return resp, true, err
	default:
		return nil, false, nil
	}
}
