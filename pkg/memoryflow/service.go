package memoryflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

const transcriptTurnsMetadataKey = "transcript_turns_json"

type transcriptEpisodeDraft struct {
	Content string
	Turns   []TranscriptTurn
}

// Service is the opinionated workflow layer on top of CortexDB memory and knowledge primitives.
type Service struct {
	db          *cortexdb.DB
	planner     QueryPlanner
	extractor   SessionExtractor
	policy      PromotionPolicy
	conventions ConventionSet
}

// Option configures the workflow service.
type Option func(*Service)

// WithPromotionPolicy injects a promotion policy.
func WithPromotionPolicy(policy PromotionPolicy) Option {
	return func(s *Service) {
		s.policy = policy
	}
}

// WithPlanner injects a query planner.
func WithPlanner(planner QueryPlanner) Option {
	return func(s *Service) {
		s.planner = planner
	}
}

// WithExtractor injects a session extractor.
func WithExtractor(extractor SessionExtractor) Option {
	return func(s *Service) {
		s.extractor = extractor
	}
}

// WithConventions injects deterministic taxonomy conventions.
func WithConventions(conventions ConventionSet) Option {
	return func(s *Service) {
		s.conventions = conventions
	}
}

// WithProjectConfig applies a project config to the service.
func WithProjectConfig(cfg ProjectConfig) Option {
	return func(s *Service) {
		if cfg.Conventions.Project == "" {
			cfg.Conventions.Project = cfg.Project
		}
		s.conventions = cfg.Conventions
	}
}

// New constructs a memory workflow facade.
func New(db *cortexdb.DB, planner QueryPlanner, extractor SessionExtractor, opts ...Option) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	svc := &Service{
		db:          db,
		planner:     planner,
		extractor:   extractor,
		policy:      DefaultPromotionPolicy{},
		conventions: DefaultConventionSet(""),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// IngestTranscript stores a transcript as exchange-shaped episodic memory chunks.
func (s *Service) IngestTranscript(ctx context.Context, req IngestTranscriptRequest) (*IngestTranscriptResponse, error) {
	if len(req.Transcript.Turns) == 0 {
		return nil, cortexdb.ErrEmptyText
	}

	exchanges := buildTranscriptEpisodes(req.Transcript)
	memoryIDs := make([]string, 0, len(exchanges))
	namespace := firstNonEmpty(req.Namespace, req.State().Namespace)
	scope := firstNonEmpty(req.Scope, cortexdb.MemoryScopeSession)
	sessionID := firstNonEmpty(req.Transcript.SessionID, req.State().SessionID)
	userID := firstNonEmpty(req.Transcript.UserID, req.State().UserID)
	taxonomy := mergeTaxonomy(req.Taxonomy, req.State().Taxonomy, Taxonomy{
		Wing:   req.Wing,
		Room:   req.Room,
		Source: req.Transcript.Source,
		Tags:   req.Tags,
	})
	taxonomy = ResolveTaxonomy(taxonomy, SourceHint{
		Project: s.conventions.Project,
		Source:  req.Transcript.Source,
		Title:   req.Transcript.Title,
		Tags:    req.Tags,
	}, s.conventions)

	for i, exchange := range exchanges {
		memoryID := fmt.Sprintf("episode:%s:%03d", safeID(firstNonEmpty(sessionID, "session")), i)
		metadata := taxonomyMetadata(taxonomy, cloneAnyMap(req.Metadata))
		metadata["ingest_mode"] = "transcript"
		metadata["exchange_index"] = i
		if req.Transcript.Title != "" {
			metadata["title"] = req.Transcript.Title
		}
		if payload, err := json.Marshal(exchange.Turns); err == nil {
			metadata[transcriptTurnsMetadataKey] = string(payload)
		}

		if _, err := s.db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
			MemoryID:  memoryID,
			UserID:    userID,
			SessionID: sessionID,
			Scope:     scope,
			Namespace: namespace,
			Role:      "episode",
			Content:   exchange.Content,
			Metadata:  metadata,
		}); err != nil {
			return nil, err
		}
		memoryIDs = append(memoryIDs, memoryID)
	}

	return &IngestTranscriptResponse{MemoryIDs: memoryIDs, Count: len(memoryIDs)}, nil
}

// Recall resolves a retrieval plan and delegates to the CortexDB brain facade.
func (s *Service) Recall(ctx context.Context, req RecallRequest) (*RecallResponse, error) {
	plan, err := s.resolvePlan(ctx, req.Query, req.State, req.Plan)
	if err != nil {
		return nil, err
	}
	response, err := s.db.Brain().Recall(ctx, cortexdb.BrainRecallRequest{
		Query:            req.Query,
		UserID:           firstNonEmpty(req.UserID, req.State.UserID),
		SessionID:        firstNonEmpty(req.SessionID, req.State.SessionID),
		Scope:            firstNonEmpty(req.Scope, cortexdb.MemoryScopeSession),
		Namespace:        firstNonEmpty(req.Namespace, req.State.Namespace),
		Collection:       req.Collection,
		TopKMemories:     req.TopKMemories,
		TopKKnowledge:    req.TopKKnowledge,
		RetrievalMode:    req.RetrievalMode,
		DisableMemory:    req.DisableMemory,
		DisableKnowledge: req.DisableKnowledge,
		DisableGraph:     req.DisableGraph,
		GraphLight:       req.GraphLight,
		MaxContextChars:  req.MaxContextChars,
		MaxContextChunks: req.MaxContextChunks,
		Plan:             plan,
	})
	if err != nil {
		return nil, err
	}

	return &RecallResponse{
		Plan:     response.MemoryPlan,
		Decision: response.MemoryDecision,
		Response: *response,
	}, nil
}

// WakeUp assembles a compact startup context by prepending identity to a recall pack.
func (s *Service) WakeUp(ctx context.Context, req WakeUpRequest) (*WakeUpResponse, error) {
	layersResp, err := s.WakeUpLayers(ctx, WakeUpLayersRequest{
		Identity: req.Identity,
		Recall:   req.Recall,
	})
	if err != nil {
		return nil, err
	}
	text := ""
	for _, layer := range layersResp.Layers {
		if layer.Level == WakeUpLevelL2 {
			text = layer.Text
			break
		}
	}
	if text == "" && len(layersResp.Layers) > 0 {
		text = layersResp.Layers[len(layersResp.Layers)-1].Text
	}

	return &WakeUpResponse{
		Text:   strings.TrimSpace(text),
		Recall: layersResp.Recall,
	}, nil
}

// WakeUpLayers returns explicit L0-L3 wake-up tiers.
func (s *Service) WakeUpLayers(ctx context.Context, req WakeUpLayersRequest) (*WakeUpLayersResponse, error) {
	recall := req.Recall
	if recall.TopKMemories == 0 {
		recall.TopKMemories = 4
	}
	if recall.TopKKnowledge == 0 {
		recall.TopKKnowledge = 3
	}
	if recall.MaxContextChars == 0 {
		recall.MaxContextChars = 1200
	}

	recallResp, err := s.Recall(ctx, recall)
	if err != nil {
		return nil, err
	}

	layers := []WakeUpLayer{
		{Level: WakeUpLevelL0, Title: "Identity", Text: strings.TrimSpace(req.Identity)},
		{Level: WakeUpLevelL1, Title: "Compact Recall", Text: buildLayerL1(req.Identity, recallResp.Response)},
		{Level: WakeUpLevelL2, Title: "Context Pack", Text: buildLayerL2(req.Identity, recallResp.Response)},
		{Level: WakeUpLevelL3, Title: "Detailed Recall", Text: buildLayerL3(req.Identity, recallResp.Response)},
	}
	return &WakeUpLayersResponse{Layers: layers, Recall: *recallResp}, nil
}

// CloseSession optionally promotes extracted durable knowledge at session end.
func (s *Service) CloseSession(ctx context.Context, req CloseSessionRequest) (*CloseSessionResponse, error) {
	if !req.Promote {
		return &CloseSessionResponse{}, nil
	}
	extractor := s.extractor
	if extractor == nil {
		extractor = HeuristicExtractor{}
	}

	promotions, err := extractor.Extract(ctx, req.Transcript, req.State)
	if err != nil {
		return nil, err
	}
	if s.policy != nil {
		promotions, err = s.policy.Select(ctx, req.Transcript, req.State, promotions)
		if err != nil {
			return nil, err
		}
	}

	out := make([]cortexdb.KnowledgeRecord, 0, len(promotions))
	for i, promotion := range promotions {
		knowledgeID := promotion.KnowledgeID
		if strings.TrimSpace(knowledgeID) == "" {
			knowledgeID = fmt.Sprintf("knowledge:%s:%03d", safeID(firstNonEmpty(req.Transcript.SessionID, req.State.SessionID, "session")), i)
		}
		metadata := cloneStringMap(req.Metadata)
		for key, value := range promotion.Metadata {
			metadata[key] = value
		}
		saveResp, err := s.db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: knowledgeID,
			Title:       promotion.Title,
			Content:     promotion.Content,
			SourceURL:   promotion.SourceURL,
			Author:      firstNonEmpty(promotion.Author, req.Author),
			Collection:  firstNonEmpty(promotion.Collection, req.Collection),
			Metadata:    metadata,
			Entities:    promotion.Entities,
			Relations:   promotion.Relations,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, saveResp.Knowledge)
	}

	return &CloseSessionResponse{Promotions: out, Count: len(out)}, nil
}

// ListEpisodes lists stored exchange-shaped episodic memories for a resolved bucket.
func (s *Service) ListEpisodes(ctx context.Context, req ListEpisodesRequest) (*ListEpisodesResponse, error) {
	_, bucketID, err := resolveWorkflowMemoryBucket(req.Scope, req.UserID, req.SessionID, req.Namespace)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	messages, err := s.db.Vector().GetSessionHistory(ctx, bucketID, limit)
	if err != nil {
		return nil, err
	}
	episodes := make([]Episode, 0, len(messages))
	for _, message := range messages {
		if message.Role != "episode" {
			continue
		}
		record := workflowMemoryRecordFromMessage(bucketID, req.UserID, message)
		episode, err := episodeFromMemoryRecord(record)
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, episode)
	}
	return &ListEpisodesResponse{Episodes: episodes}, nil
}

// GetTranscript reconstructs the original transcript order from stored episodes.
func (s *Service) GetTranscript(ctx context.Context, req GetTranscriptRequest) (*GetTranscriptResponse, error) {
	episodesResp, err := s.ListEpisodes(ctx, ListEpisodesRequest{
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Scope:     req.Scope,
		Namespace: req.Namespace,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}

	transcript := Transcript{
		SessionID: req.SessionID,
		UserID:    req.UserID,
		Source:    "",
	}
	for i, episode := range episodesResp.Episodes {
		if i == 0 {
			transcript.Source = episode.Taxonomy.Source
			if title, ok := stringFromMetadataAny(episode.Memory.Metadata, "title"); ok {
				transcript.Title = title
			}
		}
		transcript.Turns = append(transcript.Turns, episode.Turns...)
	}

	return &GetTranscriptResponse{
		Transcript: transcript,
		Episodes:   episodesResp.Episodes,
	}, nil
}

// AppendDiaryEntry stores one diary entry in a dedicated diary namespace.
func (s *Service) AppendDiaryEntry(ctx context.Context, req DiaryEntryRequest) (*DiaryEntryResponse, error) {
	if strings.TrimSpace(req.Content) == "" {
		return nil, cortexdb.ErrEmptyText
	}
	scope := firstNonEmpty(req.Scope, cortexdb.MemoryScopeSession)
	namespace := firstNonEmpty(req.Namespace, "diary")
	sessionID := firstNonEmpty(req.SessionID, "diary")
	metadata := taxonomyMetadata(req.Taxonomy, cloneAnyMap(req.Metadata))
	metadata = taxonomyMetadata(ResolveTaxonomy(req.Taxonomy, SourceHint{
		Project: s.conventions.Project,
		Source:  req.Taxonomy.Source,
		Path:    req.PathHint,
	}, s.conventions), metadata)
	metadata["kind"] = firstNonEmpty(req.Taxonomy.Kind, "diary")
	entryID := strings.TrimSpace(req.EntryID)
	if entryID == "" {
		entryID = "diary:" + uuid.NewString()
	}
	resp, err := s.db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
		MemoryID:   entryID,
		UserID:     req.UserID,
		SessionID:  sessionID,
		Scope:      scope,
		Namespace:  namespace,
		Role:       "diary",
		Content:    req.Content,
		Metadata:   metadata,
		Importance: req.Importance,
	})
	if err != nil {
		return nil, err
	}
	return &DiaryEntryResponse{Entry: resp.Memory}, nil
}

// ListDiaryEntries returns chronological diary entries for a resolved bucket.
func (s *Service) ListDiaryEntries(ctx context.Context, req DiaryListRequest) (*DiaryListResponse, error) {
	scope, bucketID, err := resolveWorkflowMemoryBucket(req.Scope, req.UserID, req.SessionID, firstNonEmpty(req.Namespace, "diary"))
	if err != nil {
		return nil, err
	}
	_ = scope
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	messages, err := s.db.Vector().GetSessionHistory(ctx, bucketID, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]cortexdb.MemoryRecord, 0, len(messages))
	for _, message := range messages {
		entries = append(entries, workflowMemoryRecordFromMessage(bucketID, req.UserID, message))
	}
	return &DiaryListResponse{Entries: entries}, nil
}

// PrepareReply returns deterministic protocol guidance plus wake-up layers.
func (s *Service) PrepareReply(ctx context.Context, req ReplyProtocolRequest) (*ReplyProtocolResponse, error) {
	wake, err := s.WakeUpLayers(ctx, WakeUpLayersRequest{
		Identity: req.Identity,
		Recall:   req.Recall,
	})
	if err != nil {
		return nil, err
	}
	shouldGround := len(wake.Recall.Response.Memories) > 0 ||
		len(wake.Recall.Response.Knowledge) > 0 ||
		len(wake.Recall.Response.Chunks) > 0
	mode := "direct"
	notes := []string{"lookup_completed"}
	steps := []ProtocolStep{
		{Name: "wake_up", Action: "assemble layered startup context", Required: true, Completed: true},
		{Name: "recall", Action: "retrieve episodic memory and durable knowledge", Required: true, Completed: true},
	}
	if shouldGround {
		mode = "grounded"
		notes = append(notes, "use_retrieved_context")
		steps = append(steps, ProtocolStep{
			Name:      "answer",
			Action:    "answer using retrieved context",
			Required:  true,
			Completed: false,
			Note:      "ground on recall pack before generating the reply",
		})
	} else {
		steps = append(steps, ProtocolStep{
			Name:      "answer",
			Action:    "answer directly",
			Required:  true,
			Completed: false,
			Note:      "no relevant recall results were found",
		})
	}
	return &ReplyProtocolResponse{
		WakeUp:          *wake,
		ShouldGround:    shouldGround,
		RecommendedMode: mode,
		Steps:           steps,
		Notes:           notes,
	}, nil
}

func (s *Service) resolvePlan(ctx context.Context, query string, state SessionState, explicit *cortexdb.RetrievalPlan) (*cortexdb.RetrievalPlan, error) {
	if explicit != nil {
		planCopy := *explicit
		return &planCopy, nil
	}
	if s.planner == nil {
		return nil, nil
	}
	return s.planner.Plan(ctx, query, state)
}

func buildExchanges(transcript Transcript) []string {
	drafts := buildTranscriptEpisodes(transcript)
	out := make([]string, 0, len(drafts))
	for _, draft := range drafts {
		out = append(out, draft.Content)
	}
	return out
}

func buildTranscriptEpisodes(transcript Transcript) []transcriptEpisodeDraft {
	var exchanges []string
	var current []string
	var currentTurns []TranscriptTurn
	var drafts []transcriptEpisodeDraft

	for _, turn := range transcript.Turns {
		content := strings.TrimSpace(turn.Content)
		if content == "" {
			continue
		}
		role := firstNonEmpty(strings.TrimSpace(turn.Role), "unknown")
		line := strings.ToUpper(role) + ": " + content
		if strings.EqualFold(role, "user") && len(current) > 0 {
			exchanges = append(exchanges, strings.Join(current, "\n"))
			drafts = append(drafts, transcriptEpisodeDraft{
				Content: strings.Join(current, "\n"),
				Turns:   append([]TranscriptTurn(nil), currentTurns...),
			})
			current = nil
			currentTurns = nil
		}
		current = append(current, line)
		currentTurns = append(currentTurns, turn)
	}
	if len(current) > 0 {
		exchanges = append(exchanges, strings.Join(current, "\n"))
		drafts = append(drafts, transcriptEpisodeDraft{
			Content: strings.Join(current, "\n"),
			Turns:   append([]TranscriptTurn(nil), currentTurns...),
		})
	}
	return drafts
}

func buildLayerL1(identity string, recall cortexdb.BrainRecallResponse) string {
	var builder strings.Builder
	if identity = strings.TrimSpace(identity); identity != "" {
		builder.WriteString(identity)
		builder.WriteString("\n\n")
	}
	builder.WriteString("Recent memory cues:\n")
	for i, hit := range recall.Memories {
		if i >= 3 {
			break
		}
		room := stringFromMetadata(hit.Memory.Metadata, "room")
		if room != "" {
			builder.WriteString("- [" + room + "] ")
		} else {
			builder.WriteString("- ")
		}
		builder.WriteString(trimRunes(hit.Memory.Content, 120))
		builder.WriteString("\n")
	}
	for i, hit := range recall.Knowledge {
		if i >= 2 {
			break
		}
		builder.WriteString("- [knowledge] ")
		builder.WriteString(firstNonEmpty(hit.Title, trimRunes(hit.Snippet, 120)))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func buildLayerL2(identity string, recall cortexdb.BrainRecallResponse) string {
	var builder strings.Builder
	if identity = strings.TrimSpace(identity); identity != "" {
		builder.WriteString(identity)
		builder.WriteString("\n\n")
	}
	builder.WriteString(recall.ContextPack.Text)
	return strings.TrimSpace(builder.String())
}

func buildLayerL3(identity string, recall cortexdb.BrainRecallResponse) string {
	var builder strings.Builder
	if identity = strings.TrimSpace(identity); identity != "" {
		builder.WriteString(identity)
		builder.WriteString("\n\n")
	}
	for _, section := range recall.ContextPack.Sections {
		if strings.TrimSpace(section.Text) == "" {
			continue
		}
		title := firstNonEmpty(section.Title, section.Kind)
		builder.WriteString("## ")
		builder.WriteString(title)
		builder.WriteString("\n")
		builder.WriteString(section.Text)
		builder.WriteString("\n\n")
	}
	if len(recall.Entities) > 0 {
		builder.WriteString("Entities: ")
		builder.WriteString(strings.Join(recall.Entities, ", "))
	}
	return strings.TrimSpace(builder.String())
}

func (req IngestTranscriptRequest) State() SessionState {
	return SessionState{
		UserID:     req.Transcript.UserID,
		SessionID:  req.Transcript.SessionID,
		Namespace:  req.Namespace,
		Wing:       req.Wing,
		Room:       req.Room,
		Tags:       append([]string(nil), req.Tags...),
		Taxonomy:   mergeTaxonomy(req.Taxonomy, Taxonomy{Wing: req.Wing, Room: req.Room, Source: req.Transcript.Source, Tags: req.Tags}),
		Transcript: &req.Transcript,
	}
}

// ResolveTaxonomy resolves workflow taxonomy for external callers using the service conventions.
func (s *Service) ResolveTaxonomy(base Taxonomy, hint SourceHint) Taxonomy {
	return ResolveTaxonomy(base, hint, s.conventions)
}

func safeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, ":", "_")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeTaxonomy(items ...Taxonomy) Taxonomy {
	var out Taxonomy
	labelMap := make(map[string]string)
	tagSet := make(map[string]struct{})
	entitySet := make(map[string]struct{})
	for _, item := range items {
		if item.Kind != "" {
			out.Kind = item.Kind
		}
		if item.Wing != "" {
			out.Wing = item.Wing
		}
		if item.Room != "" {
			out.Room = item.Room
		}
		if item.Source != "" {
			out.Source = item.Source
		}
		for _, tag := range item.Tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tagSet[tag] = struct{}{}
			}
		}
		for _, entity := range item.Entities {
			entity = strings.TrimSpace(entity)
			if entity != "" {
				entitySet[entity] = struct{}{}
			}
		}
		for key, value := range item.Labels {
			if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
				labelMap[key] = value
			}
		}
	}
	for tag := range tagSet {
		out.Tags = append(out.Tags, tag)
	}
	for entity := range entitySet {
		out.Entities = append(out.Entities, entity)
	}
	if len(labelMap) > 0 {
		out.Labels = labelMap
	}
	return out
}

func taxonomyMetadata(t Taxonomy, metadata map[string]any) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if t.Kind != "" {
		metadata["kind"] = t.Kind
	}
	if t.Wing != "" {
		metadata["wing"] = t.Wing
	}
	if t.Room != "" {
		metadata["room"] = t.Room
	}
	if t.Source != "" {
		metadata["source"] = t.Source
	}
	if len(t.Tags) > 0 {
		metadata["tags"] = strings.Join(t.Tags, ",")
	}
	if len(t.Entities) > 0 {
		metadata["entities"] = strings.Join(t.Entities, ",")
	}
	for key, value := range t.Labels {
		metadata["label_"+key] = value
	}
	return metadata
}

func resolveWorkflowMemoryBucket(scope, userID, sessionID, namespace string) (string, string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "default"
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "":
		if strings.TrimSpace(sessionID) != "" {
			scope = cortexdb.MemoryScopeSession
		} else if strings.TrimSpace(userID) != "" {
			scope = cortexdb.MemoryScopeUser
		} else {
			scope = cortexdb.MemoryScopeGlobal
		}
	case cortexdb.MemoryScopeGlobal, cortexdb.MemoryScopeUser, cortexdb.MemoryScopeSession:
	default:
		return "", "", fmt.Errorf("unsupported memory scope: %s", scope)
	}
	switch scope {
	case cortexdb.MemoryScopeGlobal:
		return scope, fmt.Sprintf("memory:%s:%s", scope, namespace), nil
	case cortexdb.MemoryScopeUser:
		if strings.TrimSpace(userID) == "" {
			return "", "", fmt.Errorf("user_id is required for user scope")
		}
		return scope, fmt.Sprintf("memory:%s:%s:%s", scope, userID, namespace), nil
	case cortexdb.MemoryScopeSession:
		if strings.TrimSpace(sessionID) == "" {
			return "", "", fmt.Errorf("session_id is required for session scope")
		}
		return scope, fmt.Sprintf("memory:%s:%s:%s", scope, sessionID, namespace), nil
	default:
		return "", "", fmt.Errorf("unsupported memory scope: %s", scope)
	}
}

func workflowMemoryRecordFromMessage(sessionID, userID string, message *core.Message) cortexdb.MemoryRecord {
	record := cortexdb.MemoryRecord{
		ID:        message.ID,
		UserID:    userID,
		SessionID: sessionID,
		Role:      message.Role,
		Content:   message.Content,
		CreatedAt: message.CreatedAt,
		Metadata:  cloneAnyMap(message.Metadata),
	}
	if scope, ok := stringFromMetadataAny(record.Metadata, "scope"); ok {
		record.Scope = scope
	}
	if namespace, ok := stringFromMetadataAny(record.Metadata, "namespace"); ok {
		record.Namespace = namespace
	}
	if importance, ok := floatFromMetadata(record.Metadata, "importance"); ok {
		record.Importance = importance
	}
	if ttl, ok := intFromMetadata(record.Metadata, "ttl_seconds"); ok {
		record.TTLSeconds = ttl
	}
	if expiresAt, ok := stringFromMetadataAny(record.Metadata, "expires_at"); ok {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			record.ExpiresAt = &parsed
		}
	}
	return record
}

func episodeFromMemoryRecord(record cortexdb.MemoryRecord) (Episode, error) {
	episode := Episode{
		Memory: record,
		Taxonomy: Taxonomy{
			Kind:   stringFromMetadata(record.Metadata, "kind"),
			Wing:   stringFromMetadata(record.Metadata, "wing"),
			Room:   stringFromMetadata(record.Metadata, "room"),
			Source: stringFromMetadata(record.Metadata, "source"),
			Tags:   splitMetadataList(stringFromMetadata(record.Metadata, "tags")),
			Entities: splitMetadataList(stringFromMetadata(record.Metadata, "entities")),
		},
	}
	if exchangeIndex, ok := intFromMetadata(record.Metadata, "exchange_index"); ok {
		episode.ExchangeIndex = exchangeIndex
	}
	if payload, ok := stringFromMetadataAny(record.Metadata, transcriptTurnsMetadataKey); ok && strings.TrimSpace(payload) != "" {
		var turns []TranscriptTurn
		if err := json.Unmarshal([]byte(payload), &turns); err != nil {
			return Episode{}, fmt.Errorf("decode transcript turns: %w", err)
		}
		episode.Turns = turns
		return episode, nil
	}
	episode.Turns = fallbackTurnsFromContent(record.Content)
	return episode, nil
}

func stringFromMetadata(metadata map[string]any, key string) string {
	value, _ := stringFromMetadataAny(metadata, key)
	return value
}

func stringFromMetadataAny(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	raw, ok := metadata[key]
	if !ok {
		return "", false
	}
	switch value := raw.(type) {
	case string:
		return value, true
	default:
		bytes, err := json.Marshal(value)
		if err != nil {
			return "", false
		}
		return string(bytes), true
	}
}

func floatFromMetadata(metadata map[string]any, key string) (float64, bool) {
	if metadata == nil {
		return 0, false
	}
	raw, ok := metadata[key]
	if !ok {
		return 0, false
	}
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	default:
		return 0, false
	}
}

func intFromMetadata(metadata map[string]any, key string) (int, bool) {
	if metadata == nil {
		return 0, false
	}
	raw, ok := metadata[key]
	if !ok {
		return 0, false
	}
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func trimRunes(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func splitMetadataList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func fallbackTurnsFromContent(content string) []TranscriptTurn {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	turns := make([]TranscriptTurn, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "USER: "):
			turns = append(turns, TranscriptTurn{Role: "user", Content: strings.TrimPrefix(line, "USER: ")})
		case strings.HasPrefix(line, "ASSISTANT: "):
			turns = append(turns, TranscriptTurn{Role: "assistant", Content: strings.TrimPrefix(line, "ASSISTANT: ")})
		case strings.HasPrefix(line, "SYSTEM: "):
			turns = append(turns, TranscriptTurn{Role: "system", Content: strings.TrimPrefix(line, "SYSTEM: ")})
		case line != "":
			turns = append(turns, TranscriptTurn{Role: "unknown", Content: line})
		}
	}
	return turns
}
