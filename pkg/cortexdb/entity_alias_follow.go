package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Entity resolution (--resolve-entities) folds an entity into
// another and records the folded names on the one it keeps, under "aliases".
// A writer that still knows the entity by a folded name derived the old id,
// and so recreated the folded entity or wrote an edge to nothing; find_nodes
// told it the name was unknown. The writes and find_nodes now follow the
// alias, as recall already does (resolveEntityNameToNode).

// entityAliasScan reads the entities that carry aliases. Its condition is the
// one idx_graph_nodes_aliased is built on, so it reads that index, not every
// node of the graph.
const entityAliasScan = `SELECT id, properties FROM graph_nodes WHERE id LIKE 'entity:%' AND properties LIKE '%"aliases"%' ORDER BY id`

// entityAliasTable maps each alias, keyed as entity resolution compares
// names, to the entity that carries it. Two entities that carry the same
// alias: the first id in order keeps it. A row that cannot be read is
// skipped.
func (db *DB) entityAliasTable(ctx context.Context) (map[string]string, error) {
	return db.entityAliasTableIn(ctx, db.SQL())
}

// entityAliasTableIn is entityAliasTable read through q, the store's handle
// or a write transaction (knowledge_alias_follow.go).
func (db *DB) entityAliasTableIn(ctx context.Context, q queryContexter) (map[string]string, error) {
	rows, err := db.querierQuery(ctx, q, entityAliasScan)
	if err != nil {
		return nil, fmt.Errorf("read the entity aliases: %w", err)
	}
	defer rows.Close()
	table := map[string]string{}
	for rows.Next() {
		var id, properties string
		var props struct {
			Aliases []string `json:"aliases"`
		}
		if rows.Scan(&id, &properties) != nil || json.Unmarshal([]byte(properties), &props) != nil {
			continue
		}
		for _, alias := range props.Aliases {
			if key := entityAliasKey(alias); key != "" {
				if _, taken := table[key]; !taken {
					table[key] = id
				}
			}
		}
	}
	return table, rows.Err()
}

// followEntityAliases takes names with the ids derived from them and
// returns, for each name that names no node and that an entity carries as an
// alias, that entity. A node that stands under the name wins over an alias.
// Under a caller scope a name is followed only to an entity one of the
// caller's collections lists, the entities find_nodes serves it
// (scopedNode): the id a write answers would otherwise tell a confined
// caller that an entity it cannot read exists, and its name. The aliases
// are read first, through idx_graph_nodes_aliased; the nodes only when one
// of the names is an alias.
func (db *DB) followEntityAliases(ctx context.Context, names map[string]string) (map[string]string, error) {
	table, err := db.entityAliasTable(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for name, id := range names {
		if canonical, ok := table[entityAliasKey(name)]; ok {
			ids = append(ids, id, canonical)
		}
	}
	followed := map[string]string{}
	if len(ids) == 0 {
		return followed, nil
	}
	nodes, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read the entities named: %w", err)
	}
	scope, _ := callerScopeOf(ctx)
	present := make(map[string]bool, len(nodes))
	reachable := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		present[node.ID] = true
		reachable[node.ID] = scope.Knowledge == nil || slices.ContainsFunc(toStringSlice(node.Properties[entityCollectionsKey]), scope.Knowledge)
	}
	for name, id := range names {
		if canonical, ok := table[entityAliasKey(name)]; ok && !present[id] && reachable[canonical] {
			followed[name] = canonical
		}
	}
	return followed, nil
}

// followAlias is the id to write for name: the entity it was folded into,
// when the writer's id is the one the name derives and the name was
// followed, else the writer's id.
func followAlias(followed map[string]string, name, id string) string {
	if canonical := followed[name]; canonical != "" && id == graphEntityNodeID(name) {
		return canonical
	}
	return id
}

// keepCanonicalIdentity gives each node written under an alias back the
// content, type, vector and properties of the entity it lands on, the alias
// list among them: the writer adds its document, mentions and collections,
// which the caller merges after, and nothing else. A name, a description
// or a metadata key sent under an alias describes the alias, not the
// entity.
func keepCanonicalIdentity(nodes []*graph.GraphNode, prior map[string]*graph.GraphNode, aliased map[string]bool) {
	for _, node := range nodes {
		was := prior[node.ID]
		if !aliased[node.ID] || was == nil {
			continue
		}
		node.Content = was.Content
		node.NodeType = was.NodeType
		if len(was.Vector) > 0 {
			node.Vector = was.Vector
		}
		// Never nil: the entity carries its aliases there.
		node.Properties = maps.Clone(was.Properties)
	}
}

type followedAliasesKey struct{}

// withFollowedAliases returns a context under which an ontology resolves a
// relation endpoint that names no node through followed, the aliases the
// call already read: validation and the write then agree with a call under
// no ontology, and the table is read once.
func withFollowedAliases(ctx context.Context, followed map[string]string) context.Context {
	return context.WithValue(ctx, followedAliasesKey{}, followed)
}

func followedAliasesFrom(ctx context.Context) map[string]string {
	followed, _ := ctx.Value(followedAliasesKey{}).(map[string]string)
	return followed
}
