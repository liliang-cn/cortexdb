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
