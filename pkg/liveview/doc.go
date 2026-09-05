// Package liveview serves a knowledge graph as a live, rotatable 3D page.
//
// A rendered file is true for the instant it was taken. This serves a page
// instead — a WebGL scene on 127.0.0.1 that keeps up with the graph on its own,
// so a graph being written can be watched rather than re-rendered.
//
// Two things move on the page, and they arrive by different routes:
//
//   - Structure — nodes and relations appearing or disappearing. Found by
//     polling the graph and diffing, because it can be written by any process
//     sharing it and there is no change feed to subscribe to. Only the
//     difference is sent, so the layout keeps every node that did not change
//     exactly where it had settled.
//   - Activity — a query running, something being saved, a relation drawn.
//     A query changes nothing in the graph, so no amount of polling can show
//     one. These are reported by whoever handled the call, through
//     [Server.Observe], and light up the nodes they named.
//
// A third thing on the page does not move, and says so by arriving differently.
// The contract panel answers how much of this store stands on what, and what on
// it is waiting for a person — the knowledge contract, read back through
// [Source.Contract] as a [ContractReport]. It is two aggregate scans and a
// filtered read over a shelf that changes when somebody reviews a record, so
// the panel fetches it from /api/contract on its own slow timer
// ([ContractInterval]) rather than riding the structure poll. A source that
// keeps no contract leaves the hook nil, and the panel says that in words: a
// store nobody can ask and a store nobody has graded are different findings,
// and the second is the one a real machine is usually in.
//
// There is a second page, at /ontology, and it draws a different graph about
// the same store: not what is in this brain but what it is allowed to talk
// about — tens of declared object types with link types between them, read
// back through [Source.Ontology] as an [OntologyReport]. It is drawn as a
// deterministic 2D diagram rather than in the scene next door, for the reason
// spelled out over ontologyHTML: an ontology is tens of named nodes with
// declared structure — interfaces, link direction, a foreign key on one side —
// and a force layout can only express distance.
//
// A picture of the declarations alone describes intent. Held against what the
// store's records are actually typed as, it describes reality, and the gap is
// the finding: a declared type at zero instances is something nobody used, a
// node_type nothing declares is the reverse, and on a real brain it is most of
// them. Which of the four things this page has to say — the source cannot be
// asked, nothing is saved, a schema is saved and unused, a schema is in use —
// is [OntologyReport.State], decided in Go so the page never reads an empty
// list and guesses.
//
// A caller supplies a [Source], which is anything that can read nodes and
// edges. [OpenSource] builds one from the ambient CortexDB configuration
// (CORTEXDB_REMOTE for a shared brain, CORTEXDB_PATH otherwise), and
// [LoadLocal] and [LoadRemote] are exported for callers that know which graph
// they want and would rather say so than set environment variables.
//
// The listener binds 127.0.0.1 and there is deliberately no option to widen it.
// The page is the whole graph with no authentication in front of it, so a
// listener on any other interface would be an unauthenticated read of
// everything it contains. An embedder that needs to expose it further should
// put its own authenticated proxy in front rather than ask for a wider bind.
//
// Typical use:
//
//	src, err := liveview.OpenSource(ctx)
//	if err != nil {
//		return err
//	}
//	sv, err := liveview.Start(ctx, src, liveview.DefaultPort, liveview.DefaultInterval, false)
//	if err != nil {
//		return err
//	}
//	defer sv.Close()
//	fmt.Println(sv.URL())
package liveview
