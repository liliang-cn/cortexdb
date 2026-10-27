package liveview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The recalls page, at /recalls: the brain's recall log read back as a
// timeline — each recall's query, who asked, how long it took and what it
// returned, with how each hit was found — and, per record, how often recalls
// reach it and which never do. A recall opens on its explain record; a hit
// can be marked useful or noise, and the marks download as recall-eval
// golden cases. The graph page reads the same log to shade nodes by how often
// recalls reached them (?color=recalls) and to replay one recall by lighting
// the nodes it returned (?replay=<recall id>).
//
// Every answer comes through the brain's own tools ([RecallTools]), so a
// shared brain holds the page to the key it was opened with: a key with a
// scope sees its own recalls. The log is opt-in on the brain
// (CORTEXDB_RECALL_LOG=1); a brain without it answers so, and the page says
// that in words rather than showing an empty timeline.

// RecallTools are the recall-log tools the page may run beside
// [ExploreTools]. All read but recall_feedback, which writes a mark into the
// recall log and nothing else; the page sends it only from a same-origin JSON
// POST (see handleRecallFeedback).
var RecallTools = []string{
	"recall_log_list",
	"recall_log_get",
	"recall_log_stats",
	"recall_feedback",
	"recall_feedback_export",
}

const (
	heatLimit        = 500
	feedbackBodySize = 4 << 10
)

func (s *Server) handleRecallsPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// json.Marshal escapes < and >, so the name cannot close the script.
	name, _ := json.Marshal(s.src.Describe)
	_, _ = w.Write([]byte(strings.Replace(recallsHTML, "__SOURCE__", string(name), 1)))
}

// handleRecalls answers a page of the timeline.
func (s *Server) handleRecalls(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := cortexdb.RecallLogListRequest{
		RecordID: strings.TrimSpace(q.Get("record_id")),
		Contains: strings.TrimSpace(q.Get("contains")),
		Project:  strings.TrimSpace(q.Get("project")),
		Key:      strings.TrimSpace(q.Get("key")),
		Empty:    q.Get("empty") == "1",
	}
	req.Limit, _ = strconv.Atoi(q.Get("limit"))
	req.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var out cortexdb.RecallLogListResponse
	err := callInto(ctx, s.src.Call, "recall_log_list", req, &out)
	writeExplore(w, out, err)
}

// handleRecall answers one recall with its explain record.
func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var out cortexdb.RecallLogEntry
	err := callInto(ctx, s.src.Call, "recall_log_get", cortexdb.RecallLogGetRequest{RecallID: r.URL.Query().Get("id")}, &out)
	writeExplore(w, out, err)
}

// handleRecallStats answers the per-record view.
func (s *Server) handleRecallStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := cortexdb.RecallStatsRequest{RecordID: strings.TrimSpace(q.Get("record_id")), Key: strings.TrimSpace(q.Get("key"))}
	req.Limit, _ = strconv.Atoi(q.Get("limit"))
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var out cortexdb.RecallStatsResponse
	err := callInto(ctx, s.src.Call, "recall_log_stats", req, &out)
	writeExplore(w, out, err)
}

// RecallHeat is how often recalls reached each node, for shading the graph.
type RecallHeat struct {
	Nodes map[string]int `json:"nodes"`
	Max   int            `json:"max"`
}

// handleRecallHeat answers the node counts the graph page shades by.
func (s *Server) handleRecallHeat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var stats cortexdb.RecallStatsResponse
	err := callInto(ctx, s.src.Call, "recall_log_stats", cortexdb.RecallStatsRequest{Limit: heatLimit}, &stats)
	heat := RecallHeat{Nodes: map[string]int{}}
	for _, rec := range stats.MostRecalled {
		if rec.NodeID == "" {
			continue
		}
		heat.Nodes[rec.NodeID] += rec.Count
		if heat.Nodes[rec.NodeID] > heat.Max {
			heat.Max = heat.Nodes[rec.NodeID]
		}
	}
	writeExplore(w, heat, err)
}

// errNotSameOrigin refuses a write a page on another origin sent.
var errNotSameOrigin = errors.New("feedback is accepted from this page only")

// handleRecallFeedback marks one hit. The view is an unauthenticated loopback
// listener, so a page on any other site the reader has open could post to
// it: a write is taken only as a JSON body (which a cross-site form cannot
// send without a preflight this server never grants) and, when the browser
// names an origin, only from this one.
func (s *Server) handleRecallFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeBadRequest(w, fmt.Errorf("POST a JSON body"))
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": errNotSameOrigin.Error()})
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, feedbackBodySize+1))
	if err != nil || len(body) > feedbackBodySize {
		writeBadRequest(w, fmt.Errorf("feedback body too large"))
		return
	}
	var req cortexdb.RecallFeedbackRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeBadRequest(w, err)
		return
	}
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var out cortexdb.RecallFeedbackResponse
	err = callInto(ctx, s.src.Call, "recall_feedback", req, &out)
	writeExplore(w, out, err)
}

// handleRecallGolden downloads the feedback as golden-set JSONL, the format
// cortexdb-mcp-stdio --recall-eval reads.
func (s *Server) handleRecallGolden(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var out cortexdb.RecallFeedbackExportResponse
	if err := callInto(ctx, s.src.Call, "recall_feedback_export", cortexdb.RecallFeedbackExportRequest{}, &out); err != nil {
		writeExplore(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="recall-feedback-golden.jsonl"`)
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	for _, c := range out.Cases {
		_ = enc.Encode(c)
	}
}
