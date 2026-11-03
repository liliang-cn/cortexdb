package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// openBrainDB opens the CortexDB database, wiring an OpenAI-compatible embedder
// when one is configured via the environment. Without embedder env vars it
// stays in no-embedder lexical mode (the default). This is the single open path
// used by the server and every one-shot mode, so semantic retrieval turns on
// consistently wherever the bundled binary points at an embeddings endpoint
// (e.g. a local Ollama: CORTEXDB_EMBED_BASE_URL=http://localhost:11434/v1).
//
// Env (CORTEXDB_EMBED_* preferred; OPENAI_* accepted for parity with
// cortexdb-grpc):
//
//	CORTEXDB_EMBED_BASE_URL / OPENAI_BASE_URL  embeddings base URL (enables it)
//	CORTEXDB_EMBED_API_KEY  / OPENAI_API_KEY   API key (Ollama accepts any)
//	CORTEXDB_EMBED_MODEL                       model (default text-embedding-3-small)
//	CORTEXDB_EMBED_DIM                         dimension (default 1536; embeddinggemma=768)
//	CORTEXDB_EMBED_BATCH_SIZE                  texts per request (default 4)
//	CORTEXDB_EMBED_TIMEOUT_SECONDS             request timeout (default 300)
//	CORTEXDB_MEMORY_SEMANTIC_FLOOR             cosine below which a semantic memory
//	                                           hit is noise (default calibrated on
//	                                           embeddinggemma; other models differ)
//	CORTEXDB_MEMORY_KEYWORD_FLOOR              rarity-weighted coverage a keyword
//	CORTEXDB_KNOWLEDGE_VECTOR_FLOOR            memory hit, the cosine and coverage
//	CORTEXDB_KNOWLEDGE_KEYWORD_FLOOR           a knowledge chunk must reach (0 = off)
func openBrainDB(dbPath string) (*cortexdb.DB, error) {
	var opts []cortexdb.Option
	if e := cortexdb.EmbedderFromEnv(); e != nil {
		opts = append(opts, cortexdb.WithEmbedder(e))
	}
	if r := newReranker(); r != nil {
		opts = append(opts, cortexdb.WithReranker(r))
	}
	if v := os.Getenv("CORTEXDB_MEMORY_SEMANTIC_FLOOR"); v != "" {
		floor, err := strconv.ParseFloat(v, 64)
		if err != nil || floor <= 0 || floor >= 1 {
			return nil, fmt.Errorf("invalid CORTEXDB_MEMORY_SEMANTIC_FLOOR %q: want a cosine between 0 and 1", v)
		}
		opts = append(opts, cortexdb.WithMemorySemanticFloor(floor))
	}
	// Off unless asked: a brain that never reviews anything searches as before.
	if v := os.Getenv("CORTEXDB_REVIEW_GATE"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid CORTEXDB_REVIEW_GATE %q: want 1 or 0", v)
		}
		if on {
			opts = append(opts, cortexdb.WithReviewGate())
		}
	}
	floors, err := cortexdb.RelevanceFloorOptionsFromEnv()
	if err != nil {
		return nil, err
	}
	opts = append(opts, floors...)
	recallLog, err := cortexdb.RecallLogOptionsFromEnv()
	if err != nil {
		return nil, err
	}
	opts = append(opts, recallLog...)
	if qt := newQueryTransformer(); qt != nil {
		opts = append(opts, cortexdb.WithQueryTransformer(qt))
	}
	return cortexdb.Open(cortexdb.DefaultConfig(dbPath), opts...)
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// newOpenAIEmbedder is CortexDB's OpenAI-compatible embedder with this program's timeout.
func newOpenAIEmbedder(baseURL, apiKey, model string, dim int) *cortexdb.OpenAIEmbedder {
	return cortexdb.NewOpenAIEmbedder(cortexdb.OpenAIEmbedderConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: model, Dim: dim,
		Batch:   envIntOr("CORTEXDB_EMBED_BATCH_SIZE", 4),
		Timeout: time.Duration(envIntOr("CORTEXDB_EMBED_TIMEOUT_SECONDS", 300)) * time.Second,
	})
}
