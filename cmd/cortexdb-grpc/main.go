// Command cortexdb-grpc serves the pkg/cortexdb facade over gRPC.
//
// Configuration (flags override env; .env is loaded when present):
//
//	CORTEXDB_PATH        SQLite file path (default ~/.cortexdb/cortexdb.db)
//	CORTEXDB_GRPC_ADDR   listen address (default 127.0.0.1:47821)
//	CORTEXDB_GRPC_TOKEN  bearer token; empty disables auth
//	OPENAI_BASE_URL      OpenAI-compatible base URL enabling embeddings
//	OPENAI_API_KEY       API key for the embeddings endpoint
//	CORTEXDB_EMBED_MODEL embedding model name (default text-embedding-3-small)
//	CORTEXDB_EMBED_DIM   embedding dimension (default 1536)
//	CORTEXDB_KEY_FILE    JSON file of scoped API keys (replaces the single token)
//	CORTEXDB_BACKUP_DIR  where AdminService.Backup may write
//	CORTEXDB_HTTP_ADDR   also serve REST + /metrics + /debug/vars here
//
// `cortexdb-grpc -health` probes a server already running at -addr instead of
// starting one, so the same binary is its own liveness check. See deploy/ for
// systemd units and container images that use it.
package main

import (
	"context"
	"expvar"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/httpapi"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	_ = godotenv.Load()

	var (
		dbPath = flag.String("db", envOr("CORTEXDB_PATH", cortexdb.DefaultDBPath()), "SQLite database path")
		addr   = flag.String("addr", envOr("CORTEXDB_GRPC_ADDR", "127.0.0.1:47821"), "listen address")
		token  = flag.String("token", os.Getenv("CORTEXDB_GRPC_TOKEN"), "bearer token (empty disables auth)")
		health = flag.Bool("health", false, "probe a server already running at -addr, print its status and exit")
		keys   = flag.String("keys", envOr("CORTEXDB_KEY_FILE", ""), "JSON file of scoped API keys; when set it is the whole policy and -token is ignored")
		backup = flag.String("backup-dir", envOr("CORTEXDB_BACKUP_DIR", ""), "directory AdminService.Backup may write into (default: the directory holding the database)")
		httpAd = flag.String("http-addr", envOr("CORTEXDB_HTTP_ADDR", ""), "also serve REST + /metrics + /debug/vars here; empty disables it")
	)
	flag.Parse()

	// The probe opens no database: it is meant to run beside a live server as a
	// container HEALTHCHECK or a systemd health command, where a second process
	// touching the same SQLite file would be the wrong thing to do.
	if *health {
		line, err := probeHealth(context.Background(), *addr, *token)
		if err != nil {
			log.Fatalf("unhealthy: %v", err)
		}
		log.Print(line)
		return
	}

	var opts []cortexdb.Option
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		model := envOr("CORTEXDB_EMBED_MODEL", "text-embedding-3-small")
		dim, err := strconv.Atoi(envOr("CORTEXDB_EMBED_DIM", "1536"))
		if err != nil {
			log.Fatalf("invalid CORTEXDB_EMBED_DIM: %v", err)
		}
		opts = append(opts, cortexdb.WithEmbedder(newOpenAIEmbedder(baseURL, os.Getenv("OPENAI_API_KEY"), model, dim)))
		log.Printf("embedder: %s (dim=%d) via %s", model, dim, baseURL)
	} else {
		log.Printf("embedder: none (lexical mode)")
	}

	db, err := cortexdb.Open(cortexdb.DefaultConfig(*dbPath), opts...)
	if err != nil {
		log.Fatalf("open cortexdb: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close cortexdb: %v", err)
		}
	}()

	// Metrics wrap authorization rather than sitting inside it, so a denied
	// call is counted too — on a server whose point is scoped keys, denials
	// are the signal an operator most needs.
	metrics := observability.NewRegistry()
	if err := metrics.PublishExpvar("cortexdb"); err != nil {
		log.Fatalf("publish expvar: %v", err)
	}

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	srv, err := rpcserver.NewWithPolicy(db, rpcserver.Options{
		Token:        *token,
		KeyFile:      *keys,
		DBPath:       *dbPath,
		BackupDir:    *backup,
		Interceptors: []grpc.UnaryServerInterceptor{rpcserver.MetricsInterceptor(metrics)},
	})
	if err != nil {
		// A policy that will not load must stop the process here. Starting
		// anyway would either serve nothing (confusing) or serve everything
		// (worse).
		log.Fatalf("api key policy: %v", err)
	}

	if *httpAd != "" {
		startHTTP(*httpAd, db, metrics, *token, *keys, *dbPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()

	// Say which policy is actually in force. "bearer token" printed while a
	// key file was doing the work would be a log that lies about the security
	// posture, which is the one thing a startup line must not do.
	authState := "disabled"
	switch {
	case *keys != "":
		authState = "scoped keys (" + *keys + ")"
	case *token != "":
		authState = "bearer token"
	}
	log.Printf("cortexdb-grpc listening on %s (db=%s, auth=%s)", lis.Addr(), *dbPath, authState)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// startHTTP serves the REST API, the metrics endpoint and expvar on one
// listener.
//
// It used to refuse to start alongside a key file, because pkg/httpapi knew
// only a single bearer token and serving it beside a confined gRPC port would
// have handed out unscoped — or with no token set, unauthenticated — exactly
// what the other port confines. httpapi now reads the same policy, so the two
// doors take the same key and the refusal is gone. Both ports are handed the
// identical Options, which is the property worth keeping: a deployment must not
// be securable to two different degrees depending on which one you knock on.
func startHTTP(addr string, db *cortexdb.DB, metrics *observability.Registry, token, keyFile, dbPath string) {
	handler, err := httpapi.NewWithPolicy(db, httpapi.Options{Token: token, KeyFile: keyFile, DBPath: dbPath})
	if err != nil {
		log.Fatalf("http api key policy: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", handler)
	mux.Handle("/metrics", metrics.Handler())
	mux.Handle("/debug/vars", expvar.Handler())
	go func() {
		log.Printf("http listening on %s (REST /v1, metrics /metrics, expvar /debug/vars)", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("http serve: %v", err)
		}
	}()
}
