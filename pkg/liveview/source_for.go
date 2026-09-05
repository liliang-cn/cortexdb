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
		Contract: localContract(db),
		Ontology: localOntology(db),
		Draft:    localDraft(db),
		Close:    func() error { return nil },
	}
}
