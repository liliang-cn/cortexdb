package liveview

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The live view's server.
//
// It binds 127.0.0.1 and nothing else. The page it serves is the brain's
// contents — every entity anyone stored — and this process holds a working
// connection to that brain, so a listener on any other interface would be an
// unauthenticated read of the whole thing to whoever is on the network. There
// is deliberately no flag to widen it.

// DefaultPort is the preferred port, chosen high and unusual so it does not
// collide with whatever else is being developed on this machine. When it is
// taken the server asks the OS for a free one instead of failing: a view is not
// worth an error, and the caller is told the URL either way.
const DefaultPort = 37423

// DefaultInterval is how often the brain is re-read for structural change.
// Activity does not wait for it — that arrives the moment a tool call is
// handled — so this only bounds how late a write from *another* machine shows
// up, and polling a database faster than a person can read the result is spend
// with nothing bought.
const DefaultInterval = 2 * time.Second

// Source is where a live server reads the graph from.
type Source struct {
	Describe string
	Read     func(ctx context.Context) ([]Node, []Edge, error)
	// Contract answers the knowledge contract's two questions about this
	// store: how much of it stands on what, and what on it needs a person.
	//
	// Optional, and nil is a legitimate answer rather than an oversight: a
	// source can be a graph that keeps no contract metadata at all — a side
	// graph assembled in memory, say. The panel says so in words instead of
	// drawing an empty chart over it, so nil never reads as "nothing here is
	// graded", which is a different and much more common finding.
	Contract func(ctx context.Context) (ContractReport, error)
	// Ontology answers what this store is allowed to talk about — the object
	// types and link types it declares — and, when asked for, what its records
	// are actually typed as, so the two can be held against each other.
	//
	// Optional and nil for the same reason Contract is, and with a sharper
	// consequence: a nil hook must never render as "no ontology is saved". A
	// store nobody can ask and a store nobody has modelled are different
	// findings, and the second is far and away the more common one — so
	// [OntologyReport.State] names which, and the page reads that rather than
	// inferring it from an empty list of types.
	Ontology func(ctx context.Context, q OntologyQuery) (OntologyReport, error)
	Close    func() error
}

// OpenSource opens whichever brain this process is configured for. The
// local database is opened once and held: the poller reads it every couple of
// seconds, and reopening the file on that cadence would be pointless churn.
func OpenSource(ctx context.Context) (*Source, error) {
	if addr, token, ok := RemoteConfigured(); ok {
		return &Source{
			Describe: "shared brain " + addr,
			Read: func(ctx context.Context) ([]Node, []Edge, error) {
				return LoadRemote(ctx, addr, token, 0, true)
			},
			Contract: remoteContract(addr, token),
			Ontology: remoteOntology(addr, token),
			Close:    func() error { return nil },
		}, nil
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	// Opened plainly, with no embedder and no reranker. The view reads the
	// graph tables directly and never embeds anything, so wiring up a model it
	// will not call would only add a way for a read-only picture to fail.
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	return &Source{
		Describe: dbPath,
		Read: func(ctx context.Context) ([]Node, []Edge, error) {
			return LoadLocal(ctx, db.SQL())
		},
		Contract: localContract(db),
		Ontology: localOntology(db),
		Close:    db.Close,
	}, nil
}

// Server serves one live view.
type Server struct {
	hub      *Hub
	src      *Source
	url      string
	interval time.Duration

	ln   net.Listener
	http *http.Server
	// mux is the routes, kept so a view built by New can hand them to a mux it
	// does not own. Start wraps the same mux in its own http.Server.
	mux *http.ServeMux
	// activity reports whether tool calls are being observed. A view started
	// from the command line polls a database and sees structure only; one
	// started from inside the MCP server sees the calls too. The page says
	// which, because a still ticker otherwise reads as a broken feature.
	activity bool
}

// New reads the graph once, starts polling, and returns a view that serves
// on a mux somebody else owns — see Handler.
//
// It exists because Start binds its own loopback listener, which is right for
// the MCP server (the view is a thing you open, and a port is how you open it)
// and wrong for a process that already has an HTTP server and wants the graph
// as one page of it. That process should not have to run a second listener
// on a second port for one route. Everything Start did apart from listening is
// here, and Start is now New plus a listener.
//
// The pages fetch their APIs by relative path ("api/graph", href="ontology"),
// so the handler can be mounted under a prefix — with http.StripPrefix and a
// trailing slash on the mount — and the browser resolves the rest.
func New(ctx context.Context, src *Source, interval time.Duration, activity bool) (*Server, error) {
	if src == nil {
		return nil, fmt.Errorf("live view: a source is required")
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	s := &Server{
		hub:      NewHub(),
		src:      src,
		interval: interval,
		activity: activity,
	}

	// One read before serving, so the first page load draws a graph instead of
	// an empty scene that fills in a poll later.
	if nodes, edges, rerr := src.Read(ctx); rerr == nil {
		s.hub.publishSnapshot(Snapshot{Nodes: nodes, Edges: edges})
	} else {
		fmt.Fprintf(os.Stderr, "cortexdb: live view: first read failed: %v\n", rerr)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	// The ontology is a second page rather than a mode of the first. It draws
	// a different graph — tens of declared types, not thousands of instances —
	// with a different layout, a different cadence and different honesty
	// states, and a switch on one page would have had to carry all of that
	// while pretending the two were views of one thing.
	mux.HandleFunc("/ontology", s.handleOntologyPage)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/contract", s.handleContract)
	mux.HandleFunc("/api/ontology", s.handleOntology)
	mux.HandleFunc("/api/stream", s.handleStream)
	s.mux = mux

	go s.poll(ctx)
	return s, nil
}

// Handler is the view's routes, rooted at "/". Mount it wherever the page
// should live; the pages' own links and fetches are relative, so a prefix
// works as long as it ends in a slash and is stripped before this handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Start reads the graph once, starts serving on its own loopback listener,
// and starts polling. It returns as soon as the URL is usable.
func Start(ctx context.Context, src *Source, port int, interval time.Duration, activity bool) (*Server, error) {
	s, err := New(ctx, src, interval, activity)
	if err != nil {
		return nil, err
	}
	ln, err := listenLocal(port)
	if err != nil {
		return nil, err
	}
	s.ln = ln
	s.url = "http://" + ln.Addr().String()
	s.http = &http.Server{Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		if serr := s.http.Serve(ln); serr != nil && serr != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "cortexdb: live view: %v\n", serr)
		}
	}()
	return s, nil
}

// listenLocal binds the preferred port, falling back to any free one.
func listenLocal(port int) (net.Listener, error) {
	if port < 0 {
		port = DefaultPort
	}
	if port > 0 {
		if ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			return ln, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1: %w", err)
	}
	return ln, nil
}

// URL is where the view is.
func (s *Server) URL() string { return s.url }

// Snapshot is the graph as last read.
func (s *Server) Snapshot() Snapshot { return s.hub.snapshot() }

// SourceName names the brain this view reads, for a caller that wants to say so.
func (s *Server) SourceName() string { return s.src.Describe }

// WatchesCalls reports whether tool calls reach this view. A view polling a
// database sees structure only; one fed by an MCP server sees the queries too,
// and the page says which rather than leaving a still ticker to be read as a
// fault.
func (s *Server) WatchesCalls() bool { return s.activity }

// Close stops serving and releases the brain.
func (s *Server) Close() error {
	// Shutdown stops the listener at once and then waits for handlers to
	// finish. Every open page is holding an SSE stream that only ends when its
	// request context does, so that wait always runs to the deadline — two
	// seconds of nothing, on every settings change, with the caller's lock
	// held. Close the connections instead: the listener is already shut, and a
	// stream to a view that is going away has nothing left to say.
	// A view built by New has no server of its own to stop: the process that
	// mounted it owns the listener, and shutting that down is its business.
	if s.http != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		if err := s.http.Shutdown(shutCtx); err != nil {
			// Deadline reached with streams still attached, which is the normal
			// case rather than a fault. Close forces them down.
			_ = s.http.Close()
		}
	}
	if s.src.Close != nil {
		return s.src.Close()
	}
	return nil
}

// poll re-reads the brain on a timer and publishes what changed.
//
// A failed read is reported once and then retried silently: the brain being
// briefly unavailable — a shared one restarting, a database locked by a writer
// — is a normal thing to live through, and a page that logs a line per second
// about it is worse than one that waits.
func (s *Server) poll(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		nodes, edges, err := s.src.Read(ctx)
		if err != nil {
			if !failing {
				fmt.Fprintf(os.Stderr, "cortexdb: live view: read graph: %v\n", err)
				failing = true
			}
			continue
		}
		failing = false
		s.hub.publishSnapshot(Snapshot{Nodes: nodes, Edges: edges})
	}
}

// observe is the hook the MCP middleware calls for every handled tool call.
func (s *Server) Observe(ev Event) { s.hub.publishEvent(ev) }

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is regenerated on every load rather than cached, so a rebuilt
	// binary is never shadowed by a stale copy in the browser.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(pageHTML))
}

// Payload is the page's opening state: everything needed to draw,
// plus what the header reports about where it came from.
type Payload struct {
	Version  int64   `json:"version"`
	Nodes    []Node  `json:"nodes"`
	Edges    []Edge  `json:"edges"`
	Events   []Event `json:"events"`
	Source   string  `json:"source"`
	Activity bool    `json:"activity"`
	Interval int64   `json:"interval_ms"`
}

func (s *Server) payload() Payload {
	snap := s.hub.snapshot()
	backlog := s.hub.recentEvents()
	return Payload{
		Version:  snap.Version,
		Nodes:    snap.Nodes,
		Edges:    snap.Edges,
		Events:   backlog,
		Source:   s.src.Describe,
		Activity: s.activity,
		Interval: s.interval.Milliseconds(),
	}
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.payload())
}

// handleContract answers the contract panel.
//
// Read on demand rather than served from the hub: unlike structure it is not
// diffed and never pushed, so there is nothing to hold between requests, and
// the panel asks about once every fifteen seconds. It adds no listener and no
// reach — it is the same store this process is already holding open, and the
// page showing it is the same page already showing every node in it.
//
// A store this view cannot ask always returns 200 with a report that says why.
// A failed fetch would leave the panel showing what it drew last, which after
// an error is the previous answer presented as the current one.
func (s *Server) handleContract(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	rep := ContractReport{}
	switch {
	case s.src.Contract == nil:
		rep = unavailableContract("this view's source keeps no knowledge contract")
	default:
		got, err := s.src.Contract(r.Context())
		if err != nil {
			rep = unavailableContract(err.Error())
		} else {
			rep = got
		}
	}
	_ = json.NewEncoder(w).Encode(rep)
}

// handleOntologyPage serves the second page: what this brain is allowed to
// talk about, rather than what is in it.
func (s *Server) handleOntologyPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(ontologyHTML))
}

// handleOntology answers the ontology page.
//
// One endpoint for both halves — the declarations and the store's own
// vocabulary — because they are one finding. Split across two fetches they
// would arrive at two different times against a store that can be written
// between them, and the page would be left reconciling a schema read at one
// instant with counts read at another, which is precisely the sort of quiet
// disagreement this page exists to surface rather than create.
//
// gap=0 is the one thing that changes the work: the declarations alone, with
// nothing read for the overlay. Same bargain the contract panel makes when it
// is folded — the expensive read is the one nobody is looking at.
//
// Like the contract endpoint, a store this view cannot ask still answers 200
// with a report saying why. A failed fetch would leave the page showing what
// it drew last, which after an error is the previous answer presented as the
// current one.
func (s *Server) handleOntology(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	q := OntologyQuery{
		SchemaID: r.URL.Query().Get("schema"),
		Usage:    r.URL.Query().Get("gap") != "0",
	}
	var rep OntologyReport
	switch {
	case s.src.Ontology == nil:
		rep = unavailableOntology("this view's source cannot be asked for an ontology")
	default:
		got, err := s.src.Ontology(r.Context(), q)
		if err != nil {
			rep = unavailableOntology(err.Error())
		} else {
			rep = got
		}
	}
	_ = json.NewEncoder(w).Encode(rep)
}

// handleStream is the live channel: one SSE connection per open page.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, snap, backlog := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	// The opening frame is the whole graph, taken with the subscription under
	// one lock, so the page starts from a state no update can have slipped past.
	open := Payload{
		Version:  snap.Version,
		Nodes:    snap.Nodes,
		Edges:    snap.Edges,
		Events:   backlog,
		Source:   s.src.Describe,
		Activity: s.activity,
		Interval: s.interval.Milliseconds(),
	}
	if !writeSSE(w, flusher, "snapshot", open) {
		return
	}

	// Without traffic a stream looks identical to a dead one, to the browser
	// and to anything between. The heartbeat is what keeps a quiet brain from
	// being mistaken for a broken connection.
	beat := time.NewTicker(20 * time.Second)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if !writeSSE(w, flusher, "ping", map[string]int64{"at": time.Now().UnixMilli()}) {
				return
			}
		case msg, alive := <-ch:
			if !alive {
				return
			}
			switch {
			case msg.Delta != nil:
				if !writeSSE(w, flusher, "delta", msg.Delta) {
					return
				}
			case msg.Event != nil:
				if !writeSSE(w, flusher, "activity", msg.Event) {
					return
				}
			}
		}
	}
}

// writeSSE emits one frame, reporting whether the connection is still good.
func writeSSE(w http.ResponseWriter, f http.Flusher, event string, v any) bool {
	body, err := json.Marshal(v)
	if err != nil {
		return true
	}
	// A payload with a newline in it would end the frame early and desynchronise
	// the stream. JSON encoding already escapes them; this is the guard that
	// says so out loud.
	body = []byte(strings.ReplaceAll(string(body), "\n", " "))
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return false
	}
	f.Flush()
	return true
}

// liveOnce holds the one server an MCP process serves. A second request for a
// view returns the first one's URL rather than binding another port: the agent
// asking twice means "show me", not "make me another".
var (
	onceMu sync.Mutex
	views  = map[string]*Server{}
)

// Shared returns the process's view of the brain, starting it on first use.
func Shared(ctx context.Context, activity bool) (*Server, error) {
	return SharedFor(ctx, "", activity, OpenSource)
}

// SharedFor returns the process's view of one graph, keyed by name, starting it
// on first use. The empty key is the brain.
//
// Keyed rather than single because a process can now be asked for more than one
// graph, and a second request for the same one means "show me", not "make me
// another": a view is a port and a poller, and handing out a new pair every time
// an agent asked twice would leave a trail of them behind.
//
// Each view binds its own port. Only the first gets the preferred one; the rest
// take whatever the OS gives, which is why the URL is always returned rather
// than assumed.
func SharedFor(ctx context.Context, key string, activity bool, open func(context.Context) (*Source, error)) (*Server, error) {
	onceMu.Lock()
	defer onceMu.Unlock()
	if sv, ok := views[key]; ok {
		return sv, nil
	}
	src, err := open(ctx)
	if err != nil {
		return nil, err
	}
	port := PortFromEnv()
	if key != "" {
		// The preferred port belongs to the brain. A side graph asks for any
		// free one rather than losing a race with it.
		port = 0
	}
	sv, err := Start(context.WithoutCancel(ctx), src, port, DefaultInterval, activity)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	views[key] = sv
	return sv, nil
}

// Current returns the running view of the brain, or nil. Used by the
// middleware, which must not start a server just because a tool was called —
// the view exists only once someone asks to see it.
func Current() *Server { return CurrentFor("") }

// CurrentFor returns the running view of one graph, or nil.
func CurrentFor(key string) *Server {
	onceMu.Lock()
	defer onceMu.Unlock()
	return views[key]
}

func PortFromEnv() int {
	if raw := strings.TrimSpace(os.Getenv("CORTEXDB_LIVE_PORT")); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p >= 0 && p <= 65535 {
			return p
		}
	}
	return DefaultPort
}

// OpenInBrowser hands the URL to the desktop.
//
// Errors are returned rather than ignored: a caller that claims to have opened
// something is worse than one that says it could not and gives you the URL.
func OpenInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
