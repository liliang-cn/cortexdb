package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type item struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		resp := struct {
			Data []item `json:"data"`
		}{}
		for i := range req.Input {
			resp.Data = append(resp.Data, item{Embedding: []float32{1, 2, 3}, Index: i})
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := newOpenAIEmbedder(srv.URL, "key1", "test-model", 3)
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil || len(vec) != 3 {
		t.Fatalf("embed: %v len=%d", err, len(vec))
	}
	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 {
		t.Fatalf("batch: %v", err)
	}
	if e.Dim() != 3 {
		t.Fatalf("dim = %d", e.Dim())
	}
}

func TestOpenAIEmbedderAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := newOpenAIEmbedder(srv.URL, "bad", "test-model", 3)
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected error on 401")
	}
}
