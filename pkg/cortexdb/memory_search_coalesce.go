package cortexdb

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Identical memory searches that arrive together share one run.
//
// A shared brain sees bursts of the same question: a hive's queen hands a task
// to its workers and each of them, and the queen, recalls on the same text
// within a few milliseconds. Each recall is a full search — an embedding, a
// vector scan, up to four full-text queries — and on a two-core node sixteen
// of them at once queue for the CPU for seconds. The answer to a question
// asked twice in the same instant is the same answer, so it is computed once
// and handed to everyone who asked; each of them is still counted as a recall.

// sharedSearchTimeout bounds a search run on behalf of several callers, which
// does not end when the one that started it gives up.
const sharedSearchTimeout = 30 * time.Second

type memorySearchFlights struct {
	mu      sync.Mutex
	running map[string]*memorySearchFlight
}

type memorySearchFlight struct {
	done    chan struct{}
	waiters int // callers this answer is for, counted as recalls
	present int // callers still waiting for it
	cancel  context.CancelFunc
	resp    *MemorySearchResponse
	err     error
}

func (db *DB) searchMemoryShared(ctx context.Context, req MemorySearchRequest) (*MemorySearchResponse, error) {
	keyBytes, err := json.Marshal(req)
	if err != nil {
		return db.searchMemoryWithChains(ctx, req, true)
	}
	// Callers with different scopes do not share an answer: the run is
	// checked against the scope of whoever started it.
	key := string(keyBytes)
	if scope, ok := callerScopeOf(ctx); ok {
		key = scope.Key + "\x00" + key
	}

	f := &db.memorySearches
	f.mu.Lock()
	if f.running == nil {
		f.running = map[string]*memorySearchFlight{}
	}
	flight, joined := f.running[key]
	var runCtx context.Context
	if joined {
		flight.waiters++
		flight.present++
	} else {
		// Run detached from the caller that happened to arrive first, so its
		// leaving does not fail the others; its request values (the trace)
		// come along. It is cancelled when every caller has left: a search
		// nobody waits for only takes CPU from the ones still asked for.
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), sharedSearchTimeout)
		flight = &memorySearchFlight{done: make(chan struct{}), waiters: 1, present: 1, cancel: cancel}
		f.running[key] = flight
	}
	f.mu.Unlock()

	if !joined {
		go func() {
			cancel := flight.cancel
			defer cancel()
			resp, err := db.searchMemoryWithChains(runCtx, req, false)
			// Closed to newcomers before the recalls are counted, so every
			// caller counted is one this answer reaches.
			f.mu.Lock()
			if f.running[key] == flight {
				delete(f.running, key)
			}
			waiters := flight.waiters
			f.mu.Unlock()
			if err == nil && resp != nil {
				db.recordMemoryRecallsTimes(runCtx, resp.Results, waiters)
			}
			flight.resp, flight.err = resp, err
			close(flight.done)
		}()
	}

	select {
	case <-flight.done:
		if flight.err != nil {
			return nil, flight.err
		}
		return flight.resp.clone(), nil
	case <-ctx.Done():
		f.mu.Lock()
		flight.present--
		if flight.present == 0 {
			if f.running[key] == flight {
				delete(f.running, key)
			}
			flight.cancel()
		}
		f.mu.Unlock()
		return nil, ctx.Err()
	}
}

// searchMemoryWithChains is searchMemory with each hit's Replaces filled
// from the stored supersession chain, read at the search's as_of.
func (db *DB) searchMemoryWithChains(ctx context.Context, req MemorySearchRequest, recordRecalls bool) (*MemorySearchResponse, error) {
	resp, err := db.searchMemory(ctx, req, recordRecalls)
	if err != nil || resp == nil || len(resp.Results) == 0 {
		return resp, err
	}
	// The as_of was checked by searchMemory; this cannot fail on it.
	chainCtx, _ := withMemoryAsOf(ctx, req.AsOf)
	if err := db.attachReplaces(chainCtx, resp.Results); err != nil {
		return nil, err
	}
	return resp, nil
}

// clone gives each caller its own response: they are encoded and changed
// independently.
func (r *MemorySearchResponse) clone() *MemorySearchResponse {
	if r == nil {
		return nil
	}
	out := *r
	out.Results = make([]MemorySearchHit, len(r.Results))
	for i, hit := range r.Results {
		out.Results[i] = hit
		out.Results[i].Replaces = append([]MemoryReplaced(nil), hit.Replaces...)
		if hit.Memory.Metadata != nil {
			md := make(map[string]any, len(hit.Memory.Metadata))
			for k, v := range hit.Memory.Metadata {
				md[k] = v
			}
			out.Results[i].Memory.Metadata = md
		}
	}
	return &out
}
