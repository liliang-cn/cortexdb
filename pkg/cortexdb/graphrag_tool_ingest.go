package cortexdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// IngestDocument stores a document's chunks and graph nodes. With an embedder
// the chunks are embedded as knowledge_save embeds them; without one they get
// lexical vectors, so the tool works on a brain that has no embedder at all.
func (t *GraphRAGToolbox) IngestDocument(ctx context.Context, req ToolIngestDocumentRequest) (*ToolIngestDocumentResponse, error) {
	if req.DocumentID == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, ErrEmptyText
	}

	condition, err := newIngestCondition(req)
	if err != nil {
		return nil, err
	}

	ingestOpts := GraphRAGIngestOptions{
		Collection:   req.Collection,
		ChunkSize:    req.ChunkSize,
		ChunkOverlap: req.ChunkOverlap,
	}
	applyGraphRAGIngestDefaults(&ingestOpts)

	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	// The content's hash travels with the document (metadata _content_sha256,
	// also a property of its node) so the two questions a sync job asks can be
	// answered without re-reading anything: has this document changed since it
	// was stored, and is this content already stored under another id. The
	// chunking travels with it too, so "unchanged" can mean the stored chunks
	// are the ones this request would make.
	metadata := cloneStringMap(req.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	t.db.stampIngestMarks(metadata, req.Content, ingestOpts)
	// A media document's content is its file: the hash is the file's, so
	// dedupe, if_sha256 and the unchanged check all speak of the file, and
	// the marks name what its text was derived with and where each chunk
	// sits in it (media.go).
	if req.media != nil {
		req.media.stamp(metadata)
	}
	contentHash := metadata[contentHashKey]

	existing, err := t.db.store.GetDocument(ctx, req.DocumentID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("get existing document: %w", err)
	}
	var stored map[string]any
	if existing != nil {
		stored = existing.Metadata
	}
	// A condition that does not hold refuses before anything is made, the
	// unchanged path included; the transaction checks it again under the
	// write lock.
	if condition != nil {
		hash, _ := stored[contentHashKey].(string)
		if err := condition.holds(req.DocumentID, existing != nil, hash); err != nil {
			return nil, err
		}
	}
	if err := carryExpiry(metadata, stored, req.ExpiresAt); err != nil {
		return nil, err
	}
	// Signed as knowledge_save signs, before the comparison below: the same
	// text by another key is written again, under that key.
	stampWriter(ctx, metadata, plainString)
	if err := setReview(ctx, metadata, stored, req.review()); err != nil {
		return nil, err
	}
	// Content that is not blank splits into at least one chunk.
	chunks := splitGraphRAGText(req.Content, ingestOpts.ChunkSize, ingestOpts.ChunkOverlap)
	if req.media != nil {
		chunks = req.media.texts()
	}

	// A re-ingest upserts the record, the document node and the chunks under
	// their ids: chunk i of the new text is chunk i of the old one, as on
	// main, and the graph the caller attached to it (entities, mention
	// edges, relations) stays. Only the chunks past the new text's end are
	// retired, with their nodes and the edges that hang off them, so none of
	// them is served again. The same text keeps every chunk. And when the
	// request says exactly what the record says, and the chunk rows and the
	// graph nodes are all there, nothing is written: the sync job that
	// re-ingests every followed source on every pass must not rewrite every
	// unchanged document, and "unchanged" must not be said of a document
	// whose graph delete_document_graph took away, which main rebuilds.
	retire := retireNothing
	if existing != nil {
		retire = retireChunks
		if sameIngest(existing, req, metadata) {
			chunkIDs, storedIn, err := t.db.storedChunks(ctx, req.DocumentID)
			if err != nil {
				return nil, err
			}
			if storedIn == ingestOpts.Collection && len(chunkIDs) == len(chunks) {
				present, err := t.db.graphNodesPresent(ctx, append([]string{graphDocumentNodeID(req.DocumentID)}, chunkIDs...))
				if err != nil {
					return nil, err
				}
				if present {
					return &ToolIngestDocumentResponse{
						DocumentNodeID: graphDocumentNodeID(req.DocumentID),
						ChunkNodeIDs:   chunkIDs,
						Collection:     ingestOpts.Collection,
						ContentSHA256:  contentHash,
						Unchanged:      true,
					}, nil
				}
			}
		}
	}

	// The vectors come from where knowledge_save takes them: the embedder
	// when there is one, so a document ingested here is found by the same
	// semantic search as one saved there, and lexical vectors otherwise. With
	// an embedder this used to write lexical vectors of another width, which
	// the vector index refused, so every ingested chunk was invisible to it.
	chunkVectors, docVector, err := t.documentVectors(ctx, ingestOpts.Collection, firstNonEmpty(req.Title, req.Content), chunks)
	if err != nil {
		return nil, err
	}

	// The record carries what the caller said about the document, not only
	// its text: a search hit reports Title, SourceURL, Author and Metadata
	// from this record (knowledge_helpers.go), so a document ingested here
	// without them answered every question with no provenance. The same
	// fields are what knowledge_save keeps, so both ingest paths now agree.
	version := 1
	if existing != nil {
		version = existing.Version + 1
	}
	plan := &knowledgeMutationPlan{
		document: &core.Document{
			ID:        req.DocumentID,
			Title:     req.Title,
			Content:   req.Content,
			SourceURL: req.SourceURL,
			Author:    req.Author,
			Version:   version,
			Metadata:  stringMapToAnyMap(metadata),
		},
		graphOps: &graph.BatchGraphOperation{},
	}

	docNodeID := graphDocumentNodeID(req.DocumentID)
	// The document node gets the same provenance as properties, so a Cypher
	// query can select documents by source or by any metadata key. The
	// caller's keys cannot displace the two the graph relies on.
	docProps := map[string]interface{}{}
	for k, v := range metadata {
		if !recordOnlyKey(k) && !documentOnlyKey(k) {
			docProps[k] = v
		}
	}
	if req.SourceURL != "" {
		docProps["source_url"] = req.SourceURL
	}
	if req.Author != "" {
		docProps["author"] = req.Author
	}
	docProps["document_id"] = req.DocumentID
	docProps["title"] = req.Title
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, &graph.GraphNode{
		ID:         docNodeID,
		Vector:     docVector,
		Content:    firstNonEmpty(req.Title, req.Content),
		NodeType:   "document",
		Properties: docProps,
	})

	chunkIDs := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		chunkID := graphChunkNodeID(req.DocumentID, i)
		chunkIDs = append(chunkIDs, chunkID)

		chunkMetadata := map[string]string{
			"graph_kind":  "chunk",
			"document_id": req.DocumentID,
			"chunk_index": fmt.Sprintf("%d", i),
			"title":       req.Title,
		}
		for k, v := range metadata {
			if !recordOnlyKey(k) && !documentOnlyKey(k) {
				chunkMetadata[k] = v
			}
		}
		chunkProps := map[string]interface{}{
			"document_id": req.DocumentID,
			"chunk_index": i,
			"title":       req.Title,
		}
		if req.media != nil {
			req.media.markChunk(i, chunkMetadata, chunkProps)
		}

		chunkVector := chunkVectors[i]
		plan.embeddings = append(plan.embeddings, &core.Embedding{
			ID:         chunkID,
			Collection: ingestOpts.Collection,
			Vector:     chunkVector,
			Content:    chunk,
			DocID:      req.DocumentID,
			Metadata:   chunkMetadata,
		})
		plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, &graph.GraphNode{
			ID:         chunkID,
			Vector:     chunkVector,
			Content:    chunk,
			NodeType:   "chunk",
			Properties: chunkProps,
		})
		plan.graphOps.EdgeUpserts = append(plan.graphOps.EdgeUpserts, &graph.GraphEdge{
			ID:         fmt.Sprintf("edge:doc_chunk:%s:%d", req.DocumentID, i),
			FromNodeID: docNodeID,
			ToNodeID:   chunkID,
			EdgeType:   "has_chunk",
			Weight:     1.0,
		})
		if i > 0 {
			plan.graphOps.EdgeUpserts = append(plan.graphOps.EdgeUpserts, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:chunk_next:%s:%d", req.DocumentID, i),
				FromNodeID: graphChunkNodeID(req.DocumentID, i-1),
				ToNodeID:   chunkID,
				EdgeType:   "next",
				Weight:     1.0,
			})
		}
	}

	// Record, chunks, nodes and edges land in one transaction, the one
	// knowledge_save uses, so a failure half-way leaves nothing behind. The
	// dedupe lookup runs in that transaction too, once the write lock is
	// held, so two ingests of the same content that race store one copy.
	// A media document's graph comes from its derived text alone, as a
	// knowledge_save document's does from its text: extract its entities,
	// and on a re-ingest rebuild the graph, so an entity only the old
	// transcript named does not outlive it.
	if req.media != nil {
		if err := t.appendMediaEntities(ctx, req, ingestOpts.Collection, chunks, chunkIDs, plan); err != nil {
			return nil, err
		}
		if existing != nil {
			retire = retireGraph
		}
		plan.mediaVectors = req.media.vectorRows(req.DocumentID, ingestOpts.Collection, chunkIDs)
	}

	plan.condition = condition
	if req.Dedupe {
		plan.dedupe = &contentLookup{hash: contentHash, collection: ingestOpts.Collection}
	}
	if err := t.db.applyKnowledgeMutation(ctx, req.DocumentID, retire, plan); err != nil {
		var dup duplicateContent
		if errors.As(err, &dup) {
			// Nothing is stored, so no chunk: an empty list, not null.
			return &ToolIngestDocumentResponse{
				ChunkNodeIDs:  []string{},
				Collection:    ingestOpts.Collection,
				ContentSHA256: contentHash,
				DuplicateOf:   dup.holder,
			}, nil
		}
		return nil, err
	}

	return &ToolIngestDocumentResponse{
		DocumentNodeID: docNodeID,
		ChunkNodeIDs:   chunkIDs,
		Collection:     ingestOpts.Collection,
		ContentSHA256:  contentHash,
	}, nil
}

// The marks every write path leaves on a document's metadata (record, node
// and chunks): the content's hash, and the chunking the chunks were made
// with. Together with the record's own fields they say whether a re-ingest
// would store what is already there. Their names start with "_", the
// prefix importflow gives its own _table: the keys are the engine's. A
// caller's chunk_size or content_sha256 is the caller's and is stored as
// given, and a caller's copy of a mark is dropped before the mark is set.
const (
	contentHashKey  = "_content_sha256"
	chunkSizeKey    = "_chunk_size"
	chunkOverlapKey = "_chunk_overlap"
	// vectorsKey says how the chunks' vectors were made: "embedder:<dim>",
	// or "lexical" without an embedder. Two ingests of the same text agree on
	// "unchanged" only when they would also make the same vectors, so a
	// document stored before an embedder was configured is re-embedded the
	// next time it is ingested rather than kept with vectors the index refuses.
	vectorsKey = "_vectors"
)

// EngineDocumentKeys are the metadata keys the facade rewrites on every
// write of a document's metadata (ingest_document, SaveKnowledge,
// UpdateKnowledge): the marks of its content and its chunking, its expiry,
// which only the expires_at field sets, and its review decision, which only
// the review fields set. A caller's copy of them is replaced, or dropped by
// InsertGraphDocument, which writes no mark and keeps the stored expiry and
// decision, so the allowlist lets a document read back be sent again with
// them. Other engine keys, such as importflow's _table, are not rewritten
// and are not listed.
func EngineDocumentKeys() []string {
	return []string{contentHashKey, chunkSizeKey, chunkOverlapKey, vectorsKey, expiresAtKey, reviewStatusKey, reviewedByKey, reviewedAtKey}
}

// EngineMediaKeys are the marks only ingest_media writes on a document
// (media.go). Every other write of a document's metadata drops a caller's
// copy of them, since it rebuilds the chunks they describe from the text,
// so the allowlist lets a media document read back be sent again with them.
func EngineMediaKeys() []string {
	return slices.Clone(mediaKeys)
}

// EngineMemoryKeys are the metadata keys the facade rewrites on every write
// of a memory's metadata (memory_save, memory_update, and their typed
// routes): its review decision, which only the review fields set. A
// caller's copy of them is replaced, so the allowlist lets a memory read
// back be sent again with them.
func EngineMemoryKeys() []string {
	return []string{reviewStatusKey, reviewedByKey, reviewedAtKey}
}

func contentSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// stampIngestMarks writes the hash of content, the resolved chunking and the
// source of the vectors into metadata. Every path that (re)builds a
// document's chunks calls it, so the keys always describe the chunks that are
// stored, whatever a caller put under their names.
func (db *DB) stampIngestMarks(metadata map[string]string, content string, opts GraphRAGIngestOptions) {
	// The media marks describe chunks only ingest_media makes; any other
	// write rebuilds the chunks from the text, so they no longer hold.
	for _, key := range mediaKeys {
		delete(metadata, key)
	}
	metadata[contentHashKey] = contentSHA256(content)
	metadata[chunkSizeKey] = strconv.Itoa(opts.ChunkSize)
	metadata[chunkOverlapKey] = strconv.Itoa(opts.ChunkOverlap)
	metadata[vectorsKey] = db.vectorSource()
}

// vectorSource is the value of the _vectors mark for a write made now.
func (db *DB) vectorSource() string {
	if db.HasEmbedder() {
		return "embedder:" + strconv.Itoa(db.embedder.Dim())
	}
	return "lexical"
}

// documentVectors makes a document's chunk vectors and its node's vector:
// with the embedder, the chunks' embeddings and their average, as
// knowledge_save makes them; without it, lexical vectors of the collection's
// width. The collection is created for the vectors' width if it is missing.
func (t *GraphRAGToolbox) documentVectors(ctx context.Context, collection, docText string, chunks []string) ([][]float32, []float32, error) {
	if t.db.HasEmbedder() {
		if err := t.db.ensureGraphRAGCollection(ctx, collection); err != nil {
			return nil, nil, err
		}
		chunkVectors, err := t.db.embedder.EmbedBatch(ctx, chunks)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
		}
		if len(chunkVectors) != len(chunks) {
			return nil, nil, fmt.Errorf("%w: %d vectors for %d chunks", ErrEmbeddingFailed, len(chunkVectors), len(chunks))
		}
		return chunkVectors, averageVectors(chunkVectors, t.db.embedder.Dim()), nil
	}
	vectorDim, err := t.lexicalVectorDim(ctx, collection)
	if err != nil {
		return nil, nil, err
	}
	if err := t.ensureLexicalCollection(ctx, collection, vectorDim); err != nil {
		return nil, nil, err
	}
	chunkVectors := make([][]float32, len(chunks))
	for i, chunk := range chunks {
		chunkVectors[i] = lexicalVectorForText(chunk, vectorDim)
	}
	return chunkVectors, lexicalVectorForText(docText, vectorDim), nil
}

// graphNodesPresent reports whether every one of ids is a live graph node.
func (db *DB) graphNodesPresent(ctx context.Context, ids []string) (bool, error) {
	nodes, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return false, fmt.Errorf("check the graph nodes of the document: %w", err)
	}
	return len(nodes) == len(ids), nil
}

// sameIngest reports whether a stored document already says everything this
// request says: same title, source, author and metadata, the marks included,
// so the same content (by hash) chunked the same way. Then the request has
// nothing to change.
func sameIngest(existing *core.Document, req ToolIngestDocumentRequest, metadata map[string]string) bool {
	if existing.Title != req.Title || existing.SourceURL != req.SourceURL || existing.Author != req.Author {
		return false
	}
	stored := anyMapToStringMap(existing.Metadata)
	if len(stored) != len(metadata) {
		return false
	}
	for k, v := range metadata {
		if stored[k] != v {
			return false
		}
	}
	return true
}

// storedChunks lists a stored document's chunk ids in chunk order, and the
// collection they sit in. The order is by the chunk_index each chunk
// carries, not by the text of the ids, whose three-digit padding runs out at
// the thousandth chunk. A document with no chunks sits in no collection.
func (db *DB) storedChunks(ctx context.Context, documentID string) ([]string, string, error) {
	chunks, err := db.store.GetByDocID(ctx, documentID)
	if err != nil {
		return nil, "", fmt.Errorf("list chunks of %s: %w", documentID, err)
	}
	sort.Slice(chunks, func(i, j int) bool {
		a, _ := strconv.Atoi(chunks[i].Metadata["chunk_index"])
		b, _ := strconv.Atoi(chunks[j].Metadata["chunk_index"])
		return a < b
	})
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	if len(chunks) == 0 {
		return ids, "", nil
	}
	first, err := db.store.GetByID(ctx, chunks[0].ID)
	if err != nil {
		return nil, "", fmt.Errorf("find collection of %s: %w", documentID, err)
	}
	return ids, first.Collection, nil
}

// UpsertEntities writes entity nodes and mention edges for caller-supplied structured extraction.
func (t *GraphRAGToolbox) UpsertEntities(ctx context.Context, req ToolUpsertEntitiesRequest) (*ToolUpsertEntitiesResponse, error) {
	if len(req.Entities) == 0 {
		return &ToolUpsertEntitiesResponse{}, nil
	}
	if err := t.db.guardStrictActions(ctx); err != nil {
		return nil, err
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	compiled, err := t.db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	if err := t.db.validateEntityInputs(ctx, req.Entities); err != nil {
		return nil, err
	}

	vectorDim, err := t.lexicalVectorDim(ctx, defaultGraphRAGCollection)
	if err != nil {
		return nil, err
	}

	nodes := make([]*graph.GraphNode, 0, len(req.Entities))
	edges := make([]*graph.GraphEdge, 0)
	entityIDs := make([]string, 0, len(req.Entities))

	// One batch for every entity whose type declares a Vectorized property;
	// the rest keep the lexical vector they always had.
	vectorized, err := t.db.embedVectorizedEntities(ctx, compiled, req.Entities, vectorDim)
	if err != nil {
		return nil, err
	}

	for i, entity := range req.Entities {
		if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
			continue
		}
		entityID, err := ontologyEntityNodeID(compiled, entity)
		if err != nil {
			return nil, err
		}
		entityIDs = append(entityIDs, entityID)

		properties := map[string]interface{}{}
		if entity.Name != "" {
			properties["name"] = entity.Name
		}
		if entity.Description != "" {
			properties["description"] = entity.Description
		}
		for k, v := range entity.Metadata {
			properties[k] = v
		}

		vector, ok := vectorized[i]
		if !ok {
			vector = lexicalVectorForText(strings.TrimSpace(entity.Name+" "+entity.Description), vectorDim)
		}
		nodes = append(nodes, &graph.GraphNode{
			ID:         entityID,
			Vector:     vector,
			Content:    firstNonEmpty(entity.Name, entity.ID),
			NodeType:   ontologyCanonicalNodeType(compiled, firstNonEmpty(entity.Type, "entity")),
			Properties: properties,
		})

		for _, chunkID := range entity.ChunkIDs {
			if chunkID == "" {
				continue
			}
			edges = append(edges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:mention:%s:%s", chunkID, entityID),
				FromNodeID: chunkID,
				ToNodeID:   entityID,
				EdgeType:   "mentions",
				Weight:     1.0,
			})
		}
	}

	// Provenance, before anything is written. The entity nodes carry the union
	// of every document that asserted them, and the mention edges get their
	// chunk endpoints created if the caller never wrote them as graph nodes.
	// Without the first, no query can answer "where did this entity come from"
	// and no purge can remove a document's entities without guessing. Without
	// the second, every mention edge from a caller that embeds its chunks
	// outside the graph (the normal shape for an external ingest pipeline) is
	// rejected by the foreign key — for a long time silently, so the graph had
	// entities but no record of what mentioned them.
	prior, err := t.db.mergePriorEntityProperties(ctx, nodes)
	if err != nil {
		return nil, err
	}
	if req.DocumentID != "" && len(nodes) > 0 {
		mergeEntitySourceDocuments(nodes, prior, req.DocumentID)
	}
	chunkCollections, err := t.db.mentionChunkCollections(ctx, edges)
	if err != nil {
		return nil, err
	}
	mergeMentionCollections(nodes, prior, edges, chunkCollections)
	if err := t.db.preserveDeclaredEntityTypes(ctx, nodes); err != nil {
		return nil, err
	}
	stubs, err := t.missingChunkStubs(ctx, edges, req.DocumentID, vectorDim)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, stubs...)

	if len(nodes) > 0 {
		nodeResult, err := t.db.graph.UpsertNodesBatch(ctx, nodes)
		if err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}
		if err := nodeResult.Err(); err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}
	}
	if len(edges) > 0 {
		// A mention edge whose chunk was never written as a node is rejected by the
		// foreign key. Reported, because the alternative — the count of edges the
		// caller asked for, minus the ones the store kept — was indistinguishable
		// from a graph that grew.
		edgeResult, err := t.db.graph.UpsertEdgesBatch(ctx, edges)
		if err != nil {
			return nil, fmt.Errorf("upsert mention edges: %w", err)
		}
		if err := edgeResult.Err(); err != nil {
			return nil, fmt.Errorf("upsert mention edges: %w", err)
		}
	}

	return &ToolUpsertEntitiesResponse{
		EntityNodeIDs:    entityIDs,
		MentionEdgeCount: len(edges),
	}, nil
}

// UpsertRelations writes relation edges between entities.
// relationEdgeID identifies an edge by what it connects and what it means.
//
// It used to end in the relation's index within the request, so the identity of an edge depended on
// where it happened to sit in the array: a caller that re-sent the same graph — a learning trace pushed
// again each turn, a document re-indexed — wrote a new row every time. One store had 102,855 edges of
// which 61,197 were distinct, a single edge repeated 292 times, and traversal weighted by however often
// the caller had happened to repeat itself.
//
// The document id stays part of the identity when there is one: a relation read from two sources is one
// edge per source, so the second does not overwrite the first's provenance. Callers with no document —
// a learning trace, an inference rule — collapse onto one edge, which is what they meant.
func relationEdgeID(fromID, toID, edgeType, documentID string) string {
	if documentID == "" {
		return fmt.Sprintf("edge:relation:%s:%s:%s", fromID, toID, edgeType)
	}
	return fmt.Sprintf("edge:relation:%s:%s:%s:doc:%s", fromID, toID, edgeType, documentID)
}

// mergeRelationProperties folds a repeat of the same edge into the one already collected: list-valued
// provenance is unioned, everything else is overlaid.
func mergeRelationProperties(into, from map[string]interface{}) {
	for key, value := range from {
		switch key {
		case "chunk_ids", "support_edge_ids":
			into[key] = unionStrings(into[key], value)
		default:
			into[key] = value
		}
	}
}

// unionStrings appends the strings of b to those of a, keeping order and dropping repeats.
func unionStrings(a, b interface{}) []string {
	merged := make([]string, 0)
	seen := make(map[string]struct{})
	for _, value := range []interface{}{a, b} {
		for _, item := range toStringSlice(value) {
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			merged = append(merged, item)
		}
	}
	return merged
}

func toStringSlice(value interface{}) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []interface{}:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				items = append(items, text)
			}
		}
		return items
	default:
		return nil
	}
}

func (t *GraphRAGToolbox) UpsertRelations(ctx context.Context, req ToolUpsertRelationsRequest) (*ToolUpsertRelationsResponse, error) {
	if len(req.Relations) == 0 {
		return &ToolUpsertRelationsResponse{}, nil
	}
	if err := t.db.guardStrictActions(ctx); err != nil {
		return nil, err
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	compiled, err := t.db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	// Where the active schema says a link holds one value, a new value closes
	// the old one inside the write's own transaction; where it says nothing,
	// nothing is closed. Validation is told, so it does not refuse a second
	// value for a link this write is about to resolve.
	rule := t.db.singleValuedSupersession(ctx)
	vctx := ctx
	if rule != nil {
		vctx = withSupersedingWrite(ctx)
	}
	if err := t.db.validateRelationInputs(vctx, req.Relations); err != nil {
		return nil, err
	}

	edges := make([]*graph.GraphEdge, 0, len(req.Relations))
	edgeIDs := make([]string, 0, len(req.Relations))
	byID := make(map[string]*graph.GraphEdge, len(req.Relations))
	for _, rel := range req.Relations {
		if strings.TrimSpace(rel.From) == "" || strings.TrimSpace(rel.To) == "" {
			continue
		}
		fromID, err := t.db.ontologyRelationEndpointNodeID(ctx, compiled, rel.From)
		if err != nil {
			return nil, err
		}
		toID, err := t.db.ontologyRelationEndpointNodeID(ctx, compiled, rel.To)
		if err != nil {
			return nil, err
		}
		if fromID == "" || toID == "" {
			continue
		}
		edgeType := firstNonEmpty(rel.Type, "related_to")
		edgeID := relationEdgeID(fromID, toID, edgeType, req.DocumentID)
		edgeIDs = append(edgeIDs, edgeID)

		properties := map[string]interface{}{}
		if req.DocumentID != "" {
			properties["document_id"] = req.DocumentID
		}
		if len(rel.ChunkIDs) > 0 {
			properties["chunk_ids"] = rel.ChunkIDs
		}
		if rel.Inferred {
			properties["inferred"] = true
		}
		if rel.Provenance != "" {
			properties["provenance"] = rel.Provenance
		}
		if rel.RuleID != "" {
			properties["rule_id"] = rel.RuleID
		}
		if len(rel.SupportEdgeIDs) > 0 {
			properties["support_edge_ids"] = rel.SupportEdgeIDs
		}
		for k, v := range rel.Metadata {
			properties[k] = v
		}

		weight := rel.Weight
		if weight == 0 {
			weight = 1.0
		}
		// The same edge twice in one request is one edge. Merged rather than appended, because the
		// batch writer executes them in order and the second would otherwise overwrite the first's
		// chunk ids — losing provenance without saying so.
		if existing, ok := byID[edgeID]; ok {
			mergeRelationProperties(existing.Properties, properties)
			existing.Weight = weight
			continue
		}
		edge := &graph.GraphEdge{
			ID:         edgeID,
			FromNodeID: fromID,
			ToNodeID:   toID,
			EdgeType:   edgeType,
			Weight:     weight,
			Properties: properties,
		}
		byID[edgeID] = edge
		edges = append(edges, edge)
	}

	response := &ToolUpsertRelationsResponse{EdgeIDs: edgeIDs}
	if len(edges) > 0 {
		wctx := ctx
		if rule != nil {
			wctx = graph.WithSupersession(ctx, *rule)
		}
		result, err := t.db.graph.UpsertEdgesBatch(wctx, edges)
		if err != nil {
			return nil, fmt.Errorf("upsert relation edges: %w", err)
		}
		// The batch result used to be discarded. An edge the store rejects — most often because an
		// endpoint was never created as a node — was then dropped in silence, and the call returned the
		// ids of edges that are not in the graph. Nothing downstream could tell an empty graph from a
		// successful ingest.
		response.Written = result.SuccessCount
		if result.FailedCount > 0 {
			response.Rejected = rejectedEdgeMessages(result.Errors)
			if result.SuccessCount == 0 {
				return nil, fmt.Errorf("no relation could be written (%d rejected): %v",
					result.FailedCount, response.Rejected)
			}
		}
	}

	return response, nil
}

// mergeEntitySourceDocuments unions documentID into each entity node's
// source_document_ids property, keeping what other documents already recorded.
//
// A read-merge-write rather than a plain property, because the node upsert
// replaces properties wholesale: a re-extraction from document B would
// otherwise erase document A's claim to a shared entity — and with it the only
// record that lets a purge of A know to leave the entity alone.
// mergePriorEntityProperties fills in what this write did not mention.
//
// A caller that names an object it did not author supplies identity and
// nothing else — declaring the entities a document mentions is exactly that
// shape, and it is the shape the knowledge API asks for. Replacing the
// property map on such a write erased every domain property an earlier, fuller
// write had established: purpose, capacity, a foreign key. Silently, and with
// the ontology's required-property check still passing, because the primary
// key was the one thing the second write did carry.
//
// So an upsert updates rather than replaces: a property the request names wins,
// a property it leaves out survives. There is deliberately no way to remove a
// property by omitting it, because omission is what an incidental mention looks
// like and deletion should be something a caller asks for.
//
// This is the same instinct as the two mergers that were already here — source
// documents were unioned and a declared type was preserved, each one field at a
// time — generalised to the whole map. It shares its load with the
// source-document merge; preserveDeclaredEntityTypes still does its own, which
// is a redundancy worth removing and not worth pretending is already gone.
//
// Two limits are worth knowing.
//
// The prior read happens outside the write transaction, so two concurrent
// upserts of the same object can each miss the other's new property: "a
// property left out survives" holds against earlier COMMITTED state, not
// against a concurrent writer. Replace semantics lost the same race, so this
// is not a regression — but it used to be an accident and is now a promise,
// and a promise should say where it stops.
//
// A property an ontology revision no longer declares is carried forward rather
// than dropped, because nothing validates stored rows against the current
// schema. Such a value is refused on input and survives on rows written before
// the change; a re-upsert used to clear it and no longer does.
func (db *DB) mergePriorEntityProperties(ctx context.Context, nodes []*graph.GraphNode) (map[string]*graph.GraphNode, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	existing, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load prior entity state: %w", err)
	}
	prior := make(map[string]*graph.GraphNode, len(existing))
	for _, node := range existing {
		if node != nil {
			prior[node.ID] = node
		}
	}
	for _, node := range nodes {
		was, ok := prior[node.ID]
		if !ok || was.Properties == nil {
			continue
		}
		if node.Properties == nil {
			node.Properties = map[string]interface{}{}
		}
		for key, value := range was.Properties {
			if _, named := node.Properties[key]; !named {
				node.Properties[key] = value
			}
		}
	}
	return prior, nil
}

// mergeEntitySourceDocuments records that this document asserted these
// entities, keeping every document that asserted them before.
//
// Without it no query can answer "where did this entity come from" and no
// purge can remove a document's entities without guessing.
func mergeEntitySourceDocuments(nodes []*graph.GraphNode, prior map[string]*graph.GraphNode, documentID string) {
	for _, node := range nodes {
		if node.Properties == nil {
			node.Properties = map[string]interface{}{}
		}
		var before []string
		if was, ok := prior[node.ID]; ok && was.Properties != nil {
			before = toStringSlice(was.Properties["source_document_ids"])
		}
		node.Properties["source_document_ids"] = unionStrings(before, []string{documentID})
	}
}

// missingChunkStubs returns a stub graph node for every mention-edge chunk that
// does not exist as a node yet.
//
// Callers that ingest through IngestDocument have real chunk nodes and get no
// stubs. Callers that embed their chunks outside the graph — an external
// pipeline with its own chunker — reference chunk ids the graph has never seen,
// and every mention edge they ask for dies on the foreign key. A stub carries
// no content (the text lives in the caller's embedding store, under the same
// id); it exists so the edge can, and so a purge by document can find it.
func (t *GraphRAGToolbox) missingChunkStubs(ctx context.Context, edges []*graph.GraphEdge, documentID string, vectorDim int) ([]*graph.GraphNode, error) {
	ids := make([]string, 0, len(edges))
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.EdgeType != "mentions" {
			continue
		}
		if _, ok := seen[edge.FromNodeID]; ok {
			continue
		}
		seen[edge.FromNodeID] = struct{}{}
		ids = append(ids, edge.FromNodeID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	existing, err := t.db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("check chunk nodes: %w", err)
	}
	have := make(map[string]struct{}, len(existing))
	for _, node := range existing {
		if node != nil {
			have[node.ID] = struct{}{}
		}
	}
	stubs := make([]*graph.GraphNode, 0)
	for _, id := range ids {
		if _, ok := have[id]; ok {
			continue
		}
		properties := map[string]interface{}{"stub": true}
		if documentID != "" {
			properties["document_id"] = documentID
		}
		stubs = append(stubs, &graph.GraphNode{
			ID: id,
			// The store requires a vector; derived from the id so it is
			// deterministic, and never expected to win a similarity search.
			Vector:     lexicalVectorForText(id, vectorDim),
			NodeType:   "chunk",
			Properties: properties,
		})
	}
	return stubs, nil
}

// rejectedEdgeMessages turns the batch's errors into something a caller can read, capped so one broken
// ingest of a whole book does not answer with ten thousand lines.
func rejectedEdgeMessages(errs []error) []string {
	const maxReported = 20
	messages := make([]string, 0, len(errs))
	for i, err := range errs {
		if i == maxReported {
			messages = append(messages, fmt.Sprintf("... and %d more", len(errs)-maxReported))
			break
		}
		messages = append(messages, err.Error())
	}
	return messages
}

// genericEntityNodeType is what an entity gets when nobody said what it is.
const genericEntityNodeType = "entity"

// preserveDeclaredEntityTypes stops an untyped mention from overwriting a type
// somebody declared.
//
// Entity nodes upsert with ON CONFLICT ... SET node_type = excluded.node_type,
// and the automatic extractor types everything it finds "entity". So a document
// merely mentioning BA117 in prose demoted the Flight that had been saved with a
// type, and the next link referencing it failed ontology validation — "connects
// Airport and Flight, not Airport and entity". Widening the pattern to see
// acronyms at all is what made this reachable.
func (db *DB) preserveDeclaredEntityTypes(ctx context.Context, nodes []*graph.GraphNode) error {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.NodeType == genericEntityNodeType {
			ids = append(ids, node.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	existing, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return fmt.Errorf("check declared entity types: %w", err)
	}
	declared := make(map[string]string, len(existing))
	for _, node := range existing {
		if node == nil {
			continue
		}
		if nodeType := strings.TrimSpace(node.NodeType); nodeType != "" && nodeType != genericEntityNodeType {
			declared[node.ID] = nodeType
		}
	}
	for _, node := range nodes {
		if node == nil || node.NodeType != genericEntityNodeType {
			continue
		}
		if nodeType, ok := declared[node.ID]; ok {
			node.NodeType = nodeType
			if node.Properties != nil {
				node.Properties["type"] = nodeType
			}
		}
	}
	return nil
}
