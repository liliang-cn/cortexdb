package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mediaToolDefinitions declares ingest_media and search_media.
func mediaToolDefinitions() []ToolDefinition {
	segment := toolObjectSchema([]string{"text"}, map[string]any{
		"start_ms": toolIntegerSchema("Where the segment starts in the recording or video, in milliseconds."),
		"end_ms":   toolIntegerSchema("Where it ends, in milliseconds."),
		"text":     toolStringSchema("What is said, or shown, in it."),
		"speaker":  toolStringSchema("Optional speaker; heads the text in its chunk."),
	})
	return []ToolDefinition{
		{
			Name:    "ingest_media",
			Mutates: true,
			Description: "Store an image, a recording or a video as a document whose text is derived from the file: a caption and " +
				"the text visible in an image, the transcript of audio, the transcript and frame captions of a video, on one " +
				"timeline. The text is searched like any document (knowledge_search, recall), and a hit carries media: the " +
				"media_type and, for audio and video, the start_ms/end_ms of each matching chunk. Either send data (base64) " +
				"for the brain's describer and transcriber, or derive the text yourself and send caption, text or segments with " +
				"the file's sha256. The file's hash is the document's content hash: an identical re-ingest writes nothing, dedupe " +
				"refuses a file the collection holds, if_sha256 compares file hashes. The bytes are not stored; source_url says " +
				"where the file is.",
			InputSchema: toolObjectSchema(
				[]string{"document_id", "media_type"},
				map[string]any{
					"document_id": toolStringSchema("Stable document ID."),
					"media_type":  toolStringSchema("MIME type of the file: image/*, audio/* or video/*."),
					"data":        toolStringSchema("Optional file content, base64. Capped (32 MiB by default); prefer sending the text you derived."),
					"sha256":      toolStringSchema("Lower-case hex SHA-256 of the file. Required without data; checked against data when both are sent."),
					"size_bytes":  toolIntegerSchema("Optional size of the file, when data is not sent."),
					"duration_ms": toolIntegerSchema("Optional duration of a recording or video; the end of the last segment otherwise."),
					"caption":     toolStringSchema("Optional caption you derived: what the image shows."),
					"text":        toolStringSchema("Optional text you read in the image (OCR), or any further derived text."),
					"segments":    map[string]any{"type": "array", "description": "Optional timed text you derived (a transcript, frame captions).", "items": segment},
					"derived_by":  toolStringSchema("Optional name of the model that derived caption, text or segments; recorded on the document."),
					"title":       toolStringSchema("Optional human-readable title."),
					"source_url":  toolStringSchema("Where the file is (path or URL). Returned with every hit, so a client can open it."),
					"author":      toolStringSchema("Optional author."),
					"collection":  toolStringSchema("Optional chunk collection name."),
					"chunk_size":  toolIntegerSchema("Optional chunk size in words; consecutive segments are joined up to it."),
					"metadata":    toolMapSchema("Optional document metadata, as on ingest_document."),
					"dedupe":      toolBooleanSchema("When true, a file already stored under another document_id of the collection is not ingested again; the response names it in duplicate_of."),
					"if_absent":   toolBooleanSchema("When true, written only if the brain has no document of this document_id, as on ingest_document."),
					"if_sha256":   toolStringSchema("Written only if the stored document of this id holds the file of this hash, as on ingest_document."),
					"expires_at":  toolStringSchema("Optional RFC 3339 instant, as on ingest_document."),
					"review_status": toolEnumSchema("Optional review decision, as on ingest_document. "+ReviewGateNote,
						ReviewProposed, ReviewAccepted, ReviewRejected),
					"reviewed_by": toolStringSchema("Who accepted or rejected, as on ingest_document."),
					"reviewed_at": toolStringSchema("Optional RFC 3339 instant of the decision, as on ingest_document."),
				},
			),
		},
		{
			Name: "search_media",
			Description: "Find images, recordings and videos by what they show or sound like, in the brain's media embedder space " +
				"(CLIP-style): a text query finds the images and video frames that match it even where no caption says so, and " +
				"an image (data, base64) finds the images and frames that look like it. Hits are grouped by document as on " +
				"knowledge_search, with media_score (the best cosine) and media spans. knowledge_search already fuses these hits " +
				"into its answer for a text query; use this tool to search with an image, or the media space alone. Errors " +
				"when the brain has no media embedder.",
			InputSchema: toolObjectSchema(nil, map[string]any{
				"query":      toolStringSchema("Text to search with. Give query or data, not both."),
				"data":       toolStringSchema("A file to search with, base64: an image finds what looks like it."),
				"media_type": toolStringSchema("MIME type of data: image/*, audio/* or video/*."),
				"collection": toolStringSchema("Optional collection; the default knowledge collection otherwise."),
				"top_k":      toolIntegerSchema("Optional number of chunks to return."),
			}),
		},
	}
}

// callMediaTool dispatches the media tools from JSON.
func (t *GraphRAGToolbox) callMediaTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "ingest_media":
		var req ToolIngestMediaRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.IngestMedia(ctx, req)
		return resp, true, err
	case "search_media":
		var req ToolSearchMediaRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.SearchMedia(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}

// addMediaMCPTools registers the media tools on the MCP server.
func addMediaMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addGraphRAGMCPTool(server, definitions["ingest_media"], func(ctx context.Context, req ToolIngestMediaRequest) (ToolIngestMediaResponse, error) {
		resp, err := toolbox.IngestMedia(ctx, req)
		if err != nil || resp == nil {
			return ToolIngestMediaResponse{}, err
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["search_media"], func(ctx context.Context, req ToolSearchMediaRequest) (ToolSearchMediaResponse, error) {
		resp, err := toolbox.SearchMedia(ctx, req)
		if err != nil || resp == nil {
			return ToolSearchMediaResponse{}, err
		}
		return *resp, nil
	})
}
