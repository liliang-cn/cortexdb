package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

type switchEmbedder struct{ down atomic.Bool }

func (s *switchEmbedder) Dim() int { return 4 }
func (s *switchEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if s.down.Load() {
		return nil, errors.New("dial tcp 192.168.123.123:11434: connect: host is down")
	}
	return []float32{1, 0, 0, float32(len(text) % 7)}, nil
}
func (s *switchEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := s.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// An embedding host that went to sleep shows in /healthz, in /metrics and
// in the probe, without anyone having to recall first; and it shows as
// degraded, not down, because the brain still answers by keyword.
func TestAnUnreachableEmbedderShowsInHealthAndMetrics(t *testing.T) {
	reg := observability.NewRegistry()
	emb := &switchEmbedder{}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "h.db")), cortexdb.WithEmbedder(newMeteredEmbedder(emb, reg)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	health := func(query string) (int, healthReport) {
		rec := httptest.NewRecorder()
		healthHandler(db, "h.db").ServeHTTP(rec, httptest.NewRequest("GET", "/healthz"+query, nil))
		var rep healthReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return rec.Code, rep
	}

	if _, err := db.ProbeEmbedder(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if code, rep := health(""); code != 200 || rep.Status != "ok" || !rep.Embedder.Healthy {
		t.Fatalf("healthy: %d %+v", code, rep)
	}

	emb.down.Store(true)
	if _, err := db.ProbeEmbedder(ctx, 0); err == nil {
		t.Fatal("probe of a sleeping host succeeded")
	}
	if code, rep := health(""); code != 200 || rep.Status != "degraded" || !strings.Contains(rep.Embedder.LastError, "host is down") {
		t.Fatalf("degraded: %d %+v", code, rep)
	}
	if code, _ := health("?strict=1"); code != 503 {
		t.Fatalf("strict health of a degraded brain: %d", code)
	}
	var out bytes.Buffer
	_ = reg.WriteText(&out)
	for _, want := range []string{"cortexdb_embedder_up 0", `cortexdb_embedder_requests_total{outcome="error",kind="single"} 1`, `cortexdb_embedder_requests_total{outcome="ok",kind="single"} 1`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("metrics lack %s:\n%s", want, out.String())
		}
	}

	emb.down.Store(false)
	if _, err := db.ProbeEmbedder(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, rep := health(""); rep.Status != "ok" {
		t.Fatalf("recovered: %+v", rep)
	}
}
