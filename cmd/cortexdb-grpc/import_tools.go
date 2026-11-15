package main

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

// importToolRefusals is why a confined key may not call each import tool.
//
// importflow_run writes documents, triples and tables, and holds none of them
// to a caller's scope: a plan names its own namespace, the graph is shared,
// and an imported table carries no scope at all, which is the reason the
// three imported_tables_* tools are refused to the same keys. The planning
// tools write nothing, and are refused with it because a plan is of use only
// to a key that may run it.
var importToolRefusals = map[string]string{
	"importflow_run":         "writes documents, graph triples and imported tables, none of which it holds to a scope",
	"importflow_plan":        "plans an import, which a confined key may not run",
	"importflow_ddl_plan":    "plans an import, which a confined key may not run",
	"importflow_ddl_plan_ai": "plans an import, which a confined key may not run",
}

// importTools are importflow's tools as this server offers them beside the
// facade's toolbox, which cannot import importflow: the same toolbox the
// local MCP server registers, so a shared brain imports a CSV or a SQL dump
// the way a local one does.
//
// Two things differ from the local server, both because the caller is on
// another machine. The toolbox reads no local files: a path would be opened
// on this host, which is no use to the caller and would let any key read this
// host's files, so the source text travels inline. And there is no model
// here — this binary configures none — so the planning tools answer in their
// no-model mode.
func importTools(db *cortexdb.DB) []rpcserver.Tool {
	toolbox := importflow.NewToolbox(importflow.New(db))
	definitions := toolbox.Definitions()
	tools := make([]rpcserver.Tool, 0, len(definitions))
	for _, definition := range definitions {
		name := definition.Name
		tools = append(tools, rpcserver.Tool{
			Definition:      definition,
			ConfinedRefusal: importToolRefusals[name],
			Call: func(ctx context.Context, args json.RawMessage) (any, error) {
				out, err := toolbox.Call(ctx, name, args)
				return out, importToolStatus(err)
			},
		})
	}
	return tools
}

// importToolStatus gives the errors that are the caller's to fix their code;
// the rest are mapped by the server as facade errors are.
func importToolStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, importflow.ErrInvalidRequest):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, cortexdb.ErrImportedTablesUnsupported):
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return err
}
