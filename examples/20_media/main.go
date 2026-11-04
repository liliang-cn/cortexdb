// Example 20_media ingests a folder of images, recordings and videos and
// answers text questions with the file and, for audio and video, the moment.
//
//	CORTEXDB_MEDIA_VISION_URL=http://localhost:11434 CORTEXDB_MEDIA_VISION_MODEL=qwen3.5:4b \
//	CORTEXDB_MEDIA_TRANSCRIBE_URL=http://127.0.0.1:43180/v1 CORTEXDB_MEDIA_TRANSCRIBE_MODEL=whisper-base \
//	go run ./examples/20_media -dir ~/Pictures/whiteboards -q "the whiteboard with the schema" -q "when did we choose PostgreSQL"
//
// The transcriber is any OpenAI-compatible endpoint; whisper.cpp serves one:
//
//	whisper-server -m ggml-base.bin --port 43180 --inference-path /v1/audio/transcriptions --convert
//
// With CORTEXDB_EMBED_* set (see cortexdb.EmbedderFromEnv) the text is also
// embedded; without, the questions are answered by words and the graph.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/media"
)

type questions []string

func (q *questions) String() string     { return strings.Join(*q, "; ") }
func (q *questions) Set(v string) error { *q = append(*q, v); return nil }

func main() {
	dir := flag.String("dir", ".", "folder of images, recordings and videos")
	dbPath := flag.String("db", filepath.Join(os.TempDir(), "cortexdb-media-example.db"), "brain file")
	var qs questions
	flag.Var(&qs, "q", "a question (repeatable)")
	var likes questions
	flag.Var(&likes, "like", "an image to find the ones that look like it (repeatable; needs a media embedder)")
	flag.Parse()

	opts, err := media.OptionsFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if e := cortexdb.EmbedderFromEnv(); e != nil {
		opts = append(opts, cortexdb.WithEmbedder(e))
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(*dbPath), opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	tools := db.GraphRAGTools()

	entries, err := os.ReadDir(*dir)
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range entries {
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(entry.Name())))
		major, _, _ := strings.Cut(mediaType, "/")
		if entry.IsDir() || (major != "image" && major != "audio" && major != "video") {
			continue
		}
		path := filepath.Join(*dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		start := time.Now()
		resp, err := tools.IngestMedia(ctx, cortexdb.ToolIngestMediaRequest{
			DocumentID: entry.Name(),
			Title:      entry.Name(),
			MediaType:  strings.Split(mediaType, ";")[0],
			Bytes:      data,
			SourceURL:  "file://" + path,
		})
		if err != nil {
			log.Printf("%s: %v", entry.Name(), err)
			continue
		}
		state := "stored"
		if resp.Unchanged {
			state = "unchanged"
		}
		fmt.Printf("%-28s %-10s %3d segments  %-9s %s  (%s)\n", entry.Name(), resp.MediaType, resp.Segments, state,
			resp.DerivedBy, time.Since(start).Round(time.Millisecond))
	}

	for _, q := range qs {
		resp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: q, TopK: 3})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\n? %s\n", q)
		for _, hit := range resp.Results {
			where := ""
			if hit.Media != nil && len(hit.Media.Spans) > 0 {
				spans := append([]cortexdb.MediaSpan(nil), hit.Media.Spans...)
				sort.Slice(spans, func(i, j int) bool { return spans[i].StartMS < spans[j].StartMS })
				where = " at " + clock(spans[0].StartMS)
			}
			if hit.MediaScore > 0 {
				where += fmt.Sprintf("  (looks like it: %.2f)", hit.MediaScore)
			}
			fmt.Printf("  %d. %s%s\n     %s\n", hit.Rank, hit.Title, where, hit.Snippet)
		}
	}

	for _, like := range likes {
		data, err := os.ReadFile(like)
		if err != nil {
			log.Fatal(err)
		}
		resp, err := tools.SearchMedia(ctx, cortexdb.ToolSearchMediaRequest{
			Bytes: data, MediaType: mime.TypeByExtension(strings.ToLower(filepath.Ext(like))), TopK: 3,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\n~ looks like %s\n", filepath.Base(like))
		for i, hit := range resp.Results {
			fmt.Printf("  %d. %s  %.2f\n", i+1, hit.Title, hit.MediaScore)
		}
	}
}

func clock(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
