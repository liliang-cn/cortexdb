package liveview

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The poller used to re-read the whole graph every couple of seconds and diff
// it, on the stated grounds that there was no change feed to subscribe to.
// There is one now (changes_since), and the re-read was not cheap: on the
// shared brain graph_list_all of 2,000 nodes and 5,600 edges takes about a
// second of server time, so one page left open held a third of a core on a
// two-core host. The view now asks the feed whether any node or edge changed
// since the last look — one indexed read — and re-reads only when one did.

// feedPages bounds how far one check pages through events of other kinds
// (memories being recalled write the log too) before giving up and re-reading.
const feedPages = 20

// changesFunc asks a brain's change feed for one page.
type changesFunc func(ctx context.Context, req cortexdb.ChangesSinceRequest) (*cortexdb.ChangesSinceResponse, error)

// graphChanges turns a feed into the Source.Changes hook: given the cursor of
// the last look, it returns the cursor for the next and whether any node or
// edge changed in between. A negative cursor asks for the head, changing
// nothing. A change jumps the cursor to the head, because the caller is about
// to re-read everything anyway.
func graphChanges(ask changesFunc) func(ctx context.Context, after int64) (int64, bool, error) {
	return func(ctx context.Context, after int64) (int64, bool, error) {
		if after < 0 {
			r, err := ask(ctx, cortexdb.ChangesSinceRequest{After: -1})
			if err != nil {
				return after, false, err
			}
			return r.NextCursor, false, nil
		}
		for page := 0; page < feedPages; page++ {
			r, err := ask(ctx, cortexdb.ChangesSinceRequest{
				After: after, Limit: 1000, Kinds: []string{graph.ChangeKindNode, graph.ChangeKindEdge},
			})
			if err != nil {
				return after, false, err
			}
			if r.Pruned || len(r.Events) > 0 {
				return r.Head, true, nil
			}
			after = r.NextCursor
			if !r.More {
				return after, false, nil
			}
		}
		return after, true, nil
	}
}

// ChangeLog reads one page of a brain's change feed: the oldest events after
// a cursor, or with Before set the newest ones below it, optionally explained
// from the graph history (see cortexdb.ChangesSinceRequest). It is the same
// call on a local file and a shared brain, and a Source whose brain has no feed
// leaves it nil.
type ChangeLog = changesFunc

// localChangeLog reads the feed of a database this process holds open.
func localChangeLog(db *cortexdb.DB) ChangeLog {
	return func(ctx context.Context, req cortexdb.ChangesSinceRequest) (*cortexdb.ChangesSinceResponse, error) {
		return db.ChangesSince(ctx, req)
	}
}

// localChanges reads the feed of a database this process holds open.
func localChanges(db *cortexdb.DB) func(ctx context.Context, after int64) (int64, bool, error) {
	return graphChanges(localChangeLog(db))
}

// remoteChangeLog reads the feed of a shared brain through changes_since. A
// brain too old to have the tool answers with an error; one that has it but
// predates Before and Explain ignores them, which readers of a backward page
// notice by the events arriving oldest first.
func remoteChangeLog(addr, token string) ChangeLog {
	return func(ctx context.Context, req cortexdb.ChangesSinceRequest) (*cortexdb.ChangesSinceResponse, error) {
		conn, err := dial(addr, token)
		if err != nil {
			return nil, err
		}
		args, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(callCtx, &rpcv1.CallToolRequest{Name: "changes_since", ArgsJson: string(args)})
		if err != nil {
			return nil, fmt.Errorf("changes_since on %s: %w", addr, err)
		}
		var out cortexdb.ChangesSinceResponse
		if err := json.Unmarshal([]byte(resp.GetResultJson()), &out); err != nil {
			return nil, fmt.Errorf("decode changes_since: %w", err)
		}
		return &out, nil
	}
}

// remoteChanges reads the feed of a shared brain through changes_since. A
// brain too old to have the tool answers with an error, and the poller falls
// back to re-reading on every tick, as before.
func remoteChanges(addr, token string) func(ctx context.Context, after int64) (int64, bool, error) {
	return graphChanges(remoteChangeLog(addr, token))
}
