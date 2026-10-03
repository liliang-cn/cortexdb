package liveview

import (
	"context"
	"database/sql"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// SourceFor is a Source over a brain the caller already holds open.
//
// OpenSource opens its own database from the environment, which is right for
// the MCP server and the command line and wrong for a process that has a
// *cortexdb.DB in hand and wants the view to read that one — opening the file
// a second time would be a second connection to a database the process is
// writing through the first. The Source it returns does not close the DB: it
// was not the one that opened it.
func SourceFor(db *cortexdb.DB, describe string) *Source {
	return &Source{
		Describe: describe,
		Read: func(ctx context.Context) ([]Node, []Edge, error) {
			var handle *sql.DB = db.SQL()
			return LoadLocal(ctx, handle)
		},
		Grades:   true,
		ReadAsOf: localReadAsOf(db),
		Record:   localRecord(db),
		Contract: localContract(db),
		Ontology: localOntology(db),
		Draft:    localDraft(db),
		Call:     localCaller(db),
		Close:    func() error { return nil },
	}
}

// RemoteSource is a Source over a shared brain served by cortexdb-grpc at
// addr, authenticated with token.
//
// OpenSource builds the same thing from CORTEXDB_REMOTE and
// CORTEXDB_GRPC_TOKEN; this is for a process that keeps the address and token
// in its own settings. Hand-assembling a Source from LoadRemote alone gives a
// view that draws the graph and nothing else: no find, ask or Cypher across
// the store, no node-by-node expansion, no inspector, no contract. Every hook
// here goes through the same read-only tool allowlist as the local source.
func RemoteSource(addr, token string) *Source {
	call := remoteCaller(addr, token)
	return &Source{
		Describe: "shared brain " + addr,
		Read: func(ctx context.Context) ([]Node, []Edge, error) {
			return LoadRemote(ctx, addr, token, 0, true)
		},
		Record:   callerRecord(call),
		Call:     call,
		Contract: remoteContract(addr, token),
		Ontology: remoteOntology(addr, token),
		Draft:    remoteDraft(addr, token),
		Close:    func() error { return nil },
	}
}
