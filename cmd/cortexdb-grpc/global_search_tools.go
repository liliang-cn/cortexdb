package main

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

// globalSearchToolRefusals is why a confined key may not call each tool. The
// hierarchy is detected over the whole entity graph and its reports quote
// entities and relations of every collection and bucket, so neither reading
// nor building it can be held to a scope.
var globalSearchToolRefusals = map[string]string{
	"global_search":             "answers from community reports built over the whole graph, which no scope confines",
	"build_community_hierarchy": "rewrites the community reports of the whole graph, which no scope confines",
}

// globalSearchTools are graphflow's global_search and
// build_community_hierarchy as this server offers them beside the facade's
// toolbox, which cannot import graphflow: the same tools the local MCP server
// registers, so a whole-corpus question can be asked of a shared brain.
//
// Two things differ from the local server. There is no model here, so both
// run in their no-model mode, as importflow's planners do. And global_search
// never builds: it is a read, which a read-only key may call, and a build
// rewrites every report, so on this server the build is
// build_community_hierarchy's alone and needs a read-write key.
func globalSearchTools(db *cortexdb.DB) []rpcserver.Tool {
	tools := make([]rpcserver.Tool, 0, 2)
	for _, definition := range graphflow.GlobalSearchToolDefinitions() {
		name := definition.Name
		if name == "global_search" {
			definition.Description += " On a shared brain it never builds the hierarchy (build_if_empty is ignored): build_community_hierarchy, with a read-write key, does."
		}
		tools = append(tools, rpcserver.Tool{
			Definition:      definition,
			ConfinedRefusal: globalSearchToolRefusals[name],
			Call: func(ctx context.Context, args json.RawMessage) (any, error) {
				if name == "global_search" {
					var err error
					if args, err = withoutBuild(args); err != nil {
						return nil, status.Error(codes.InvalidArgument, err.Error())
					}
				}
				out, _, err := graphflow.CallGlobalSearchTool(ctx, db, nil, name, args)
				return out, globalSearchToolStatus(err)
			},
		})
	}
	return tools
}

// withoutBuild sets build_if_empty to false in a global_search call.
func withoutBuild(args json.RawMessage) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &fields); err != nil {
			return nil, err
		}
	}
	fields["build_if_empty"] = json.RawMessage("false")
	return json.Marshal(fields)
}

// globalSearchToolStatus says that a search before any build is the caller's
// to fix, by building; the rest are mapped as facade errors are.
func globalSearchToolStatus(err error) error {
	if err == nil {
		return nil
	}
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.Is(err, graphflow.ErrNoCommunityReports):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.As(err, &syntax), errors.As(err, &typ):
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return err
}
