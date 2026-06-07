// Command cortexdb-grpc serves the pkg/cortexdb facade over gRPC.
//
// Configuration (flags override env; .env is loaded when present):
//
//	CORTEXDB_PATH        SQLite file path (default cortexdb.db)
//	CORTEXDB_GRPC_ADDR   listen address (default 127.0.0.1:47821)
//	CORTEXDB_GRPC_TOKEN  bearer token; empty disables auth
//	OPENAI_BASE_URL      OpenAI-compatible base URL enabling embeddings
//	OPENAI_API_KEY       API key for the embeddings endpoint
//	CORTEXDB_EMBED_MODEL embedding model name (default text-embedding-3-small)
//	CORTEXDB_EMBED_DIM   embedding dimension (default 1536)
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
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
		dbPath = flag.String("db", envOr("CORTEXDB_PATH", "cortexdb.db"), "SQLite database path")
		addr   = flag.String("addr", envOr("CORTEXDB_GRPC_ADDR", "127.0.0.1:47821"), "listen address")
		token  = flag.String("token", os.Getenv("CORTEXDB_GRPC_TOKEN"), "bearer token (empty disables auth)")
	)
	flag.Parse()

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

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	srv := rpcserver.New(db, rpcserver.Options{Token: *token, DBPath: *dbPath})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()

	authState := "disabled"
	if *token != "" {
		authState = "bearer token"
	}
	log.Printf("cortexdb-grpc listening on %s (db=%s, auth=%s)", lis.Addr(), *dbPath, authState)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
