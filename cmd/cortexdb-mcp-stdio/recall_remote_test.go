package main

import (
	"strings"
	"testing"
)

// Auto-recall ran before the shared-brain branch and always opened the local
// database, so on a machine pointed at a remote brain every injected memory
// came from a file nothing writes to any more. The symptom is silent and looks
// like working software: memories are injected, they are simply the wrong ones
// — stale by however long ago the machine switched to the shared brain.

func TestRecallHitsAreFormattedTheSameWhereverTheyCameFrom(t *testing.T) {
	got := formatRecallHits([]recallHit{
		{Title: "hp 宿主机 e1000e 网卡挂死", Snippet: "持续大流量下 TX 环挂死"},
		{KnowledgeID: "no-title-doc", Snippet: "标题缺失时退回用 ID"},
	})

	if !strings.HasPrefix(got, "Relevant CortexDB memories for this prompt") {
		t.Fatalf("header changed; Claude Code keys off this line:\n%s", got)
	}
	for _, want := range []string{
		"- hp 宿主机 e1000e 网卡挂死: 持续大流量下 TX 环挂死",
		"- no-title-doc: 标题缺失时退回用 ID",
		"save it with memory_save / knowledge_save",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A hit with no snippet carries nothing worth injecting.
func TestRecallSkipsHitsWithNothingToShow(t *testing.T) {
	if got := formatRecallHits([]recallHit{{Title: "empty", Snippet: "   "}}); got != "" {
		t.Errorf("expected no output for a snippet-less hit, got:\n%s", got)
	}
	if got := formatRecallHits(nil); got != "" {
		t.Errorf("expected no output for no hits, got:\n%s", got)
	}
}

// The remote tool answers with the same JSON body the MCP client sees, so the
// hook has to read the hits out of it rather than out of a Go struct.
func TestRecallReadsHitsFromTheRemoteToolPayload(t *testing.T) {
	payload := `{"query":"hp 网卡","plan":{"query":"hp 网卡"},"results":[
		{"knowledge_id":"hp-e1000e-nic-hang-20260804","title":"hp 宿主机 e1000e 网卡挂死","snippet":"TX 环挂死"},
		{"knowledge_id":"other","title":"另一条","snippet":"第二条"}]}`

	hits, err := parseRecallPayload(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].Title != "hp 宿主机 e1000e 网卡挂死" || hits[0].Snippet != "TX 环挂死" {
		t.Errorf("first hit not read correctly: %+v", hits[0])
	}
	if hits[0].KnowledgeID != "hp-e1000e-nic-hang-20260804" {
		t.Errorf("knowledge_id not read: %+v", hits[0])
	}
}

// A server that answers with something unexpected must not break the prompt:
// the hook's whole contract is that it stays silent on every failure.
func TestRecallPayloadThatIsNotSearchResultsIsNotFatal(t *testing.T) {
	for _, body := range []string{"", "not json", `{"error":"boom"}`, `{"results":null}`} {
		hits, err := parseRecallPayload(body)
		if err == nil && len(hits) != 0 {
			t.Errorf("body %q should yield no hits, got %d", body, len(hits))
		}
	}
}
