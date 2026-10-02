package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ollamaEmbedder calls Ollama's native /api/embed endpoint with plain net/http,
// keeping pkg/ free of any model SDK as the repository requires.
type ollamaEmbedder struct {
	baseURL string
	model   string
	dim     int
	batch   int
	client  *http.Client
}

// newOllamaEmbedder probes the model once to learn its dimension, so a wrong
// model name fails before any ingestion starts instead of an hour into it.
func newOllamaEmbedder(ctx context.Context, baseURL, model string) (*ollamaEmbedder, error) {
	e := &ollamaEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		batch:   32,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
	vs, err := e.call(ctx, []string{"dimension probe"})
	if err != nil {
		return nil, fmt.Errorf("probe embedder %s at %s: %w", model, baseURL, err)
	}
	e.dim = len(vs[0])
	return e, nil
}

func (e *ollamaEmbedder) Dim() int { return e.dim }

func (e *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vs, err := e.call(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vs[0], nil
}

func (e *ollamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += e.batch {
		end := min(start+e.batch, len(texts))
		vs, err := e.call(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vs...)
	}
	return out, nil
}

// call embeds one request's worth of texts, retrying transient failures: a
// benchmark run makes tens of thousands of these calls against a local server
// that other work shares, and one dropped connection should not cost the run.
func (e *ollamaEmbedder) call(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": e.model, "input": texts, "truncate": true})
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * time.Second):
			}
		}
		vs, err := e.post(ctx, body)
		if err == nil {
			if len(vs) != len(texts) {
				return nil, fmt.Errorf("embed: %d vectors for %d texts", len(vs), len(texts))
			}
			return vs, nil
		}
		var perm permanentError
		if errors.As(err, &perm) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

type permanentError struct{ error }

func (e *ollamaEmbedder) post(ctx context.Context, body []byte) ([][]float32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		err := fmt.Errorf("embed: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, permanentError{err}
		}
		return nil, err
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Embeddings, nil
}

// cachedEmbedder remembers document embeddings on disk, keyed by a hash of the
// model and the exact text, so a rerun — or another mode over the same
// corpus — spends no time re-embedding passages it has embedded before. A local
// model produces ~25-35 chunks a second here, which is the difference between a
// benchmark taking an hour and taking a minute.
//
// Only EmbedBatch is cached. The facade embeds a document's chunks, and the
// entity names it extracts from them, in batches, and embeds a query on its
// own, so leaving Embed uncached keeps a real model call
// inside every measured query: a latency figure that skipped the query
// embedding would flatter the vector modes against lexical.
type cachedEmbedder struct {
	inner *ollamaEmbedder
	path  string

	mu       sync.Mutex
	vectors  map[[32]byte][]float32
	pending  *bufio.Writer
	file     *os.File
	hits     int
	computed int
}

// openCachedEmbedder loads the cache file at path, creating it if absent. A
// truncated trailing record — a run killed mid-write — is ignored rather than
// treated as corruption, and is overwritten by the next append.
func openCachedEmbedder(inner *ollamaEmbedder, path string) (*cachedEmbedder, error) {
	c := &cachedEmbedder{inner: inner, path: path, vectors: map[[32]byte][]float32{}}
	recLen := 32 + 4*inner.Dim()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	whole := len(data) - len(data)%recLen
	for off := 0; off < whole; off += recLen {
		var key [32]byte
		copy(key[:], data[off:off+32])
		v := make([]float32, inner.Dim())
		for i := range v {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[off+32+4*i:]))
		}
		c.vectors[key] = v
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(int64(whole)); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(int64(whole), io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	c.file = f
	c.pending = bufio.NewWriterSize(f, 1<<20)
	return c, nil
}

func (c *cachedEmbedder) key(text string) [32]byte {
	return sha256.Sum256([]byte(c.inner.model + "\x00" + text))
}

func (c *cachedEmbedder) Dim() int { return c.inner.Dim() }

func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return c.inner.Embed(ctx, text)
}

func (c *cachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	var missIdx []int
	var missTexts []string
	c.mu.Lock()
	for i, t := range texts {
		if v, ok := c.vectors[c.key(t)]; ok {
			out[i] = v
			c.hits++
		} else {
			missIdx = append(missIdx, i)
			missTexts = append(missTexts, t)
		}
	}
	c.mu.Unlock()
	if len(missTexts) == 0 {
		return out, nil
	}
	vs, err := c.inner.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec := make([]byte, 4)
	for j, i := range missIdx {
		out[i] = vs[j]
		k := c.key(missTexts[j])
		if _, ok := c.vectors[k]; ok {
			continue
		}
		c.vectors[k] = vs[j]
		c.computed++
		if _, err := c.pending.Write(k[:]); err != nil {
			return nil, err
		}
		for _, x := range vs[j] {
			binary.LittleEndian.PutUint32(rec, math.Float32bits(x))
			if _, err := c.pending.Write(rec); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Facade chunking defaults (pkg/cortexdb applyGraphRAGIngestDefaults and
// enforceChunkCharLimit at v2.114.1): 120 words a chunk, and no more than four
// characters a word.
const (
	facadeChunkWords = 120
	facadeChunkRunes = facadeChunkWords * 4
)

// predictedChunk returns the exact text the facade will embed for a document
// short enough to be a single chunk, and false for anything longer.
func predictedChunk(content string) (string, bool) {
	words := strings.Fields(content)
	if len(words) == 0 || len(words) > facadeChunkWords {
		return "", false
	}
	text := strings.Join(words, " ")
	if len([]rune(text)) > facadeChunkRunes {
		return "", false
	}
	return text, true
}

// Warm embeds, in full batches, the chunks the facade is about to ask for one
// at a time. SaveKnowledge embeds each document's chunks in one call, so a
// corpus of one-chunk passages (MuSiQue, LoCoMo turns) becomes one model round
// trip per passage — about eleven a second here, against thirty in batches of
// thirty-two. Only documents that are certainly a single chunk are predicted;
// a wrong prediction would cost one wasted embedding, never a wrong vector,
// because the cache is keyed by the exact text.
func (c *cachedEmbedder) Warm(ctx context.Context, contents []string) error {
	var texts []string
	seen := map[[32]byte]struct{}{}
	c.mu.Lock()
	for _, content := range contents {
		text, ok := predictedChunk(content)
		if !ok {
			continue
		}
		k := c.key(text)
		if _, cached := c.vectors[k]; cached {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		texts = append(texts, text)
	}
	c.mu.Unlock()
	const step = 512
	for start := 0; start < len(texts); start += step {
		if _, err := c.EmbedBatch(ctx, texts[start:min(start+step, len(texts))]); err != nil {
			return err
		}
	}
	return nil
}

// Close flushes new vectors to disk.
func (c *cachedEmbedder) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.pending.Flush(); err != nil {
		c.file.Close()
		return err
	}
	return c.file.Close()
}

// Stats reports how many document chunks the facade got from the cache, and
// how many vectors this run had to compute, warming included.
func (c *cachedEmbedder) Stats() (hits, computed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.computed
}
