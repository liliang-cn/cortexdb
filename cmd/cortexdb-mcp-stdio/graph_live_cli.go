package main

import (
	"context"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/internal/liveview"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// runGraphLive is `--graph-3d`: serve the live view from the command line and
// stay up until interrupted.
//
// What it cannot do is worth stating, because the difference is invisible on
// the page until you notice the ticker never moves. A command-line view is its
// own process: it polls the brain and sees structure — nodes and relations
// appearing, from this machine or any other sharing the brain — but it is not
// the process the agent's tool calls go through, so it cannot see the queries.
// For those, ask the agent for the view instead (the serve_graph_3d tool), and
// it is served from inside the server handling those calls. The page says which
// of the two it is rather than leaving you to guess.
func runGraphLive(args []string) {
	port := liveview.PortFromEnv()
	interval := liveview.DefaultInterval
	openBrowserFlag := true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-open":
			openBrowserFlag = false
		case "--port":
			if i+1 < len(args) {
				i++
				if p, err := strconv.Atoi(args[i]); err == nil && p >= 0 && p <= 65535 {
					port = p
				} else {
					fmt.Fprintf(os.Stderr, "cortexdb: --port %q is not a port\n", args[i])
					os.Exit(2)
				}
			}
		case "--interval":
			if i+1 < len(args) {
				i++
				d, err := time.ParseDuration(args[i])
				if err != nil || d <= 0 {
					fmt.Fprintf(os.Stderr, "cortexdb: --interval %q is not a duration (try 2s)\n", args[i])
					os.Exit(2)
				}
				interval = d
			}
		default:
			fmt.Fprintf(os.Stderr, "cortexdb: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}

	// Interrupt is wired before anything is opened, so Ctrl-C closes the
	// database instead of leaving the process to be killed with it open.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	src, err := liveview.OpenSource(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	sv, err := liveview.Start(ctx, src, port, interval, false)
	if err != nil {
		_ = src.Close()
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = sv.Close() }()

	snap := sv.Snapshot()
	fmt.Fprintf(os.Stderr, "cortexdb: reading %s\n", src.Describe)
	fmt.Fprintf(os.Stderr, "cortexdb: %d nodes, %d edges; polling every %s\n", len(snap.Nodes), len(snap.Edges), interval)
	fmt.Fprintln(os.Stderr, "cortexdb: structure only — for live queries and saves, ask the agent for the view (serve_graph_3d)")
	// The URL goes to stdout alone, so a caller can capture it with $(...) the
	// way /cortexdb-graph already captures the static renderer's path.
	fmt.Println(sv.URL())

	if openBrowserFlag {
		if oerr := liveview.OpenInBrowser(sv.URL()); oerr != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: open a browser at %s (%v)\n", sv.URL(), oerr)
		}
	}
	fmt.Fprintln(os.Stderr, "cortexdb: serving; Ctrl-C to stop")
	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "cortexdb: stopped")
}
