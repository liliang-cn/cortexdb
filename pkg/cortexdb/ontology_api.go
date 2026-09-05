package cortexdb

import (
	"context"
	"fmt"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// OntologySaveRequest stores or updates a v2 ontology schema.
type OntologySaveRequest struct {
	Schema     OntologySchema `json:"schema"`
	Activate   bool           `json:"activate,omitempty"`
	Deactivate bool           `json:"deactivate,omitempty"`
}

// OntologySaveResponse returns the persisted schema.
type OntologySaveResponse struct {
	Schema OntologySchema `json:"schema"`
}

// OntologyGetRequest fetches one ontology schema by ID.
type OntologyGetRequest struct {
	SchemaID string `json:"schema_id"`
}

// OntologyGetResponse returns one ontology schema.
type OntologyGetResponse struct {
	Schema OntologySchema `json:"schema"`
}

// OntologyListRequest lists ontology schemas.
type OntologyListRequest struct {
	ActiveOnly bool `json:"active_only,omitempty"`
}

// OntologyListResponse returns ontology schemas.
type OntologyListResponse struct {
	Schemas []OntologySchema `json:"schemas"`
}

// OntologyDeleteRequest deletes one ontology schema by ID.
type OntologyDeleteRequest struct {
	SchemaID string `json:"schema_id"`
}

// OntologyDeleteResponse confirms ontology deletion.
type OntologyDeleteResponse struct {
	SchemaID string `json:"schema_id"`
	Deleted  bool   `json:"deleted"`
}

func (db *DB) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	schema, err := db.saveOntologySchemaRecord(ctx, req)
	if err != nil {
		return nil, err
	}
	return &OntologySaveResponse{Schema: *schema}, nil
}

func (db *DB) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	schema, err := db.loadOntologySchema(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyGetResponse{Schema: *schema}, nil
}

func (db *DB) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	schemas, err := db.listOntologySchemaRecords(ctx, req.ActiveOnly)
	if err != nil {
		return nil, err
	}
	return &OntologyListResponse{Schemas: schemas}, nil
}

func (db *DB) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	deleted, err := db.deleteOntologySchemaRecord(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyDeleteResponse{SchemaID: req.SchemaID, Deleted: deleted}, nil
}

// OntologyDiffRequest compares a candidate schema against a stored one.
type OntologyDiffRequest struct {
	SchemaID  string         `json:"schema_id"`
	Candidate OntologySchema `json:"candidate"`
}

// OntologyDiffResponse reports the differences and whether any break data.
type OntologyDiffResponse struct {
	Diff OntologyDiff `json:"diff"`
}

// DiffOntologySchema compares a candidate schema against the stored one of
// the same ID, so a caller can see what applying it would invalidate before
// it is applied. The stored schema is the `before` side: the question being
// answered is what happens to the data already written under it.
func (db *DB) DiffOntologySchema(ctx context.Context, req OntologyDiffRequest) (*OntologyDiffResponse, error) {
	stored, err := db.loadOntologySchema(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyDiffResponse{Diff: DiffOntologySchemas(*stored, req.Candidate)}, nil
}

// ObjectSetResolveRequest evaluates an object set and returns its members.
type ObjectSetResolveRequest struct {
	ObjectSet ObjectSet `json:"object_set"`
	Limit     int       `json:"limit,omitempty"`
}

// ResolvedObject is one member of a resolved object set.
type ResolvedObject struct {
	ObjectID   string            `json:"object_id"`
	ObjectType string            `json:"object_type"`
	Title      string            `json:"title,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// ObjectSetResolveResponse returns the resolved members, plus how many there
// were before the limit was applied.
type ObjectSetResolveResponse struct {
	Objects []ResolvedObject `json:"objects"`
	Total   int              `json:"total"`
}

// ResolveObjectSetObjects evaluates an object set and loads its members.
func (db *DB) ResolveObjectSetObjects(ctx context.Context, req ObjectSetResolveRequest) (*ObjectSetResolveResponse, error) {
	resolved, err := db.ResolveObjectSet(ctx, req.ObjectSet)
	if err != nil {
		return nil, err
	}

	nodeIDs := make([]string, 0, len(resolved))
	for id := range resolved {
		nodeIDs = append(nodeIDs, id)
	}
	// Sorted before the limit is applied, because the set being paged is a Go
	// map: without an order, asking twice for the first object would hand back
	// a different one each time.
	sort.Strings(nodeIDs)

	total := len(nodeIDs)
	if req.Limit > 0 && len(nodeIDs) > req.Limit {
		nodeIDs = nodeIDs[:req.Limit]
	}

	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load resolved objects: %w", err)
	}

	// Indexed rather than ranged over, because the batch load answers in
	// whatever order the rows come back; walking the ids keeps the one order
	// chosen above as the single source of both the page and its sequence.
	byID := make(map[string]*graph.GraphNode, len(nodes))
	for _, node := range nodes {
		if node != nil {
			byID[node.ID] = node
		}
	}

	objects := make([]ResolvedObject, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node, ok := byID[nodeID]
		if !ok {
			// A static object set may name ids that do not exist, and a node
			// can be deleted between resolution and this load.
			continue
		}
		properties := make(map[string]string, len(node.Properties))
		for name, raw := range node.Properties {
			properties[name] = fmt.Sprintf("%v", raw)
		}
		objects = append(objects, ResolvedObject{
			ObjectID:   node.ID,
			ObjectType: node.NodeType,
			Title:      node.Content,
			Properties: properties,
		})
	}

	return &ObjectSetResolveResponse{Objects: objects, Total: total}, nil
}

func (t *GraphRAGToolbox) ResolveObjectSet(ctx context.Context, req ObjectSetResolveRequest) (*ObjectSetResolveResponse, error) {
	return t.db.ResolveObjectSetObjects(ctx, req)
}

func (t *GraphRAGToolbox) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	return t.db.SaveOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	return t.db.GetOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	return t.db.ListOntologySchemas(ctx, req)
}

func (t *GraphRAGToolbox) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	return t.db.DeleteOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) DiffOntologySchema(ctx context.Context, req OntologyDiffRequest) (*OntologyDiffResponse, error) {
	return t.db.DiffOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) DraftOntology(ctx context.Context, req OntologyDraftRequest) (*OntologyDraftResponse, error) {
	return t.db.DraftOntology(ctx, req)
}
