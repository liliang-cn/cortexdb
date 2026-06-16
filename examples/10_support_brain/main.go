// Customer-support "agent brain" — an end-to-end CortexDB demo over a REAL
// database (Postgres or MySQL).
//
// It shows the full real-world pipeline:
//
//	live DB (with PII) ──connector──▶ desensitize (signed plan) ──importflow──▶ CortexDB
//	                                                                              │
//	                                                          RAG + knowledge graph
//	                                                                              │
//	                                          masked Q&A · entity relationships · audited un-mask · live CDC sync
//
// The agent answers support questions ("which customers are VIP?", "what did
// customer 1 order?") using only DESENSITIZED data — real phone numbers and
// customer names never enter the knowledge base. A name is recoverable only
// through the tenant vault (connector.Unmask), simulating an authorized
// operational action (e.g. sending a notification). Finally it mutates the
// source DB and runs one CDC poll cycle to show the brain staying in sync.
//
// No LLM or embedder is required — retrieval runs in CortexDB's lexical mode and
// relationships are answered with SPARQL, so the demo is fully deterministic.
//
// Usage:
//
//	# Postgres
//	go run ./examples/10_support_brain -driver postgres \
//	  -dsn 'postgres://postgres:p@localhost:5432/postgres?sslmode=disable'
//
//	# MySQL
//	go run ./examples/10_support_brain -driver mysql \
//	  -dsn 'root:p@tcp(localhost:3306)/test'
//
// Pass -seed=false if the customers/orders tables already exist.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func main() {
	driver := flag.String("driver", "postgres", "postgres | mysql")
	dsn := flag.String("dsn", "", "database DSN (postgres URL or go-sql-driver mysql DSN)")
	seed := flag.Bool("seed", true, "create + populate demo customers/orders tables")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("set -dsn (e.g. postgres://postgres:p@localhost:5432/postgres?sslmode=disable)")
	}
	if err := run(*driver, *dsn, *seed); err != nil {
		log.Fatal(err)
	}
}

func run(driver, dsn string, seed bool) error {
	ctx := context.Background()
	sqlDriver, connDriver := "pgx", "postgres"
	if driver == "mysql" {
		sqlDriver, connDriver = "mysql", "mysql"
	}

	// --- 0. (optional) seed a realistic business schema with PII ----------------
	admin, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return err
	}
	defer admin.Close()
	if seed {
		if err := seedSchema(ctx, admin, driver); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		fmt.Println("seeded customers + orders (with PII) into", driver)
	}

	// --- 1. open the agent's brain (a single CortexDB file) ---------------------
	dir, err := os.MkdirTemp("", "support-brain")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		return err
	}
	defer db.Close()

	// --- 2. connect the live DB through the connector privacy gate --------------
	src, err := openSource(connDriver, dsn)
	if err != nil {
		return err
	}
	defer src.Close()

	// 2a. introspect + auto-classify PII into a proposed MaskingPlan.
	plan, err := connector.BuildMaskingPlan(ctx, src, connector.NewRuleClassifier(), connector.PlanOptions{})
	if err != nil {
		return err
	}
	// 2b. HUMAN REVIEW (simulated): keep the non-PII business columns, confirm the
	// PII handling. This is the "schema-first, data-second" sign-off step.
	setAction(&plan, "customers", "id", connector.ActionKeep)
	setAction(&plan, "customers", "name", connector.ActionPseudonymize) // reversible via vault
	setAction(&plan, "customers", "phone", connector.ActionMask)        // 138****1234
	setAction(&plan, "customers", "email", connector.ActionMask)
	setAction(&plan, "customers", "city", connector.ActionKeep)
	setAction(&plan, "customers", "vip", connector.ActionKeep)
	setAction(&plan, "orders", "id", connector.ActionKeep)
	setAction(&plan, "orders", "customer_id", connector.ActionKeep)
	setAction(&plan, "orders", "product", connector.ActionKeep)
	setAction(&plan, "orders", "amount", connector.ActionKeep)
	setAction(&plan, "orders", "status", connector.ActionKeep)
	setAction(&plan, "orders", "updated_at", connector.ActionKeep)

	fmt.Println("\n=== proposed MaskingPlan (review before signing) ===")
	for _, r := range plan.Columns {
		fmt.Printf("  %-10s %-12s kind=%-12s action=%s\n", r.Table, r.Column, r.PiiKind, r.Action)
	}
	plan.Sign("data-owner", time.Now()) // Run refuses an unsigned plan

	// 2c. tenant vault holds reversible originals (separate file, separate trust).
	vault, err := connector.OpenSQLiteVault(filepath.Join(dir, "tenant.vault.db"))
	if err != nil {
		return err
	}
	defer vault.Close()
	key := connector.StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef"))
	d, err := connector.NewDesensitizer(plan, connector.DesensitizerOptions{
		Tenant: "acme", KeyProvider: key, Vault: vault,
	})
	if err != nil {
		return err
	}

	// --- 3. import the DESENSITIZED data into RAG + knowledge graph --------------
	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"customers": {
			RAG: &importflow.RAGPlan{ContentTmpl: "Customer {name} in {city}, VIP={vip}", IDColumn: "id"},
			KG: &importflow.KGPlan{Entities: []importflow.EntityMap{
				{Ref: "c", Type: "Customer", IDTmpl: "{id}", Props: []string{"city", "vip"}},
			}},
		},
		"orders": {
			RAG: &importflow.RAGPlan{ContentTmpl: "Order {id}: {product}, amount {amount}, status {status}", IDColumn: "id"},
			KG: &importflow.KGPlan{
				Entities: []importflow.EntityMap{
					{Ref: "cust", Type: "Customer", IDTmpl: "{customer_id}"},
					{Ref: "ord", Type: "Order", IDTmpl: "{id}", Props: []string{"product", "status"}},
				},
				Relations: []importflow.RelationMap{{Subject: "cust", Predicate: "placed", Object: "ord"}},
			},
		},
	}}
	rep, err := importflow.New(db).Run(ctx, connector.Desensitized(src, d), mapping)
	if err != nil {
		return err
	}
	fmt.Printf("\nimported %d rows → %d RAG chunks, %d graph triples (PII desensitized before indexing)\n",
		rep.RowsRead, rep.ChunksIndexed, rep.TriplesCreated)

	// --- 4. the agent answers support questions over MASKED data ----------------
	fmt.Println("\n=== Q: which customers are VIP? (lexical RAG) ===")
	res, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: "VIP customer", TopK: 5})
	if err != nil {
		return err
	}
	for _, h := range res.Results {
		full, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: h.KnowledgeID})
		fmt.Printf("  • %s\n", full.Knowledge.Content)
	}

	fmt.Println("\n=== Q: what did customer 1 order? (SPARQL over the knowledge graph) ===")
	q := `SELECT ?order WHERE { <urn:cortexdb:Customer:1> <urn:cortexdb:rel:placed> ?order . }`
	qr, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: q})
	if err != nil {
		return err
	}
	for _, row := range qr.Result.Bindings {
		fmt.Printf("  • %s\n", row["order"].Value)
	}

	// --- 5. authorized operational action: un-mask one name via the vault -------
	// Customer content carries a pseudonym token for the name; the RAG/LLM path
	// only ever sees the token. To actually contact the customer we resolve it.
	tok := firstToken(res.Results, db, ctx)
	if tok != "" {
		got, err := connector.Unmask(ctx, vault, "acme", []string{tok}, key)
		if err != nil {
			return err
		}
		fmt.Printf("\n=== authorized un-mask (audited): %s → %q ===\n", tok, got[tok])
	}

	// --- 6. live CDC sync: change the source DB, poll once, see the brain update -
	fmt.Println("\n=== live update: shipping order 101 in the source DB ===")
	if err := updateOrderStatus(ctx, admin, driver, 101, "shipped"); err != nil {
		return err
	}
	cp, _ := connector.NewSQLiteCheckpointStore(db)
	poll, err := connector.NewPollingChangeSource(connDriver, dsn, connector.PollingOptions{
		Tables: []connector.TableCursor{{Table: "orders", CursorColumn: "updated_at", KeyColumns: []string{"id"}}},
	})
	if err != nil {
		return err
	}
	defer poll.Close()
	w, err := connector.NewWatcher(db, poll, connector.WatcherOptions{
		SourceKey: "orders-sync", Desensitizer: d, Checkpoint: cp,
		Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{"orders": mapping.Tables["orders"]}},
	})
	if err != nil {
		return err
	}
	if err := w.Run(ctx); err != nil { // one poll cycle (polling source drains once)
		return err
	}
	after, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "101"})
	fmt.Printf("  order 101 now: %s\n", after.Knowledge.Content)

	fmt.Println("\n✓ end-to-end: real DB → desensitized → RAG+KG → masked Q&A → audited un-mask → live CDC sync")
	return nil
}

// openSource opens the live connector source for the chosen driver.
func openSource(driver, dsn string) (importflow.Source, error) {
	opts := connector.SourceOptions{Tables: []string{"customers", "orders"}}
	if driver == "mysql" {
		return connector.NewMySQLSource(dsn, opts)
	}
	return connector.NewPostgresSource(dsn, opts)
}

// setAction overrides the action for one column in the plan (the human review).
func setAction(p *connector.MaskingPlan, table, col string, a connector.MaskAction) {
	for i := range p.Columns {
		if p.Columns[i].Table == table && p.Columns[i].Column == col {
			p.Columns[i].Action = a
			return
		}
	}
}

// firstToken pulls a pseudonym token (tok_...) out of the first customer chunk.
func firstToken(hits []cortexdb.KnowledgeSearchHit, db *cortexdb.DB, ctx context.Context) string {
	for _, h := range hits {
		full, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: h.KnowledgeID})
		for _, w := range strings.Fields(full.Knowledge.Content) {
			if strings.HasPrefix(w, "tok_") {
				return w
			}
		}
	}
	return ""
}

// --- demo schema seeding (driver-specific DDL) ------------------------------

func seedSchema(ctx context.Context, db *sql.DB, driver string) error {
	var stmts []string
	if driver == "mysql" {
		stmts = []string{
			`DROP TABLE IF EXISTS orders`,
			`DROP TABLE IF EXISTS customers`,
			`CREATE TABLE customers(id INT PRIMARY KEY, name VARCHAR(64), phone VARCHAR(32), email VARCHAR(128), city VARCHAR(64), vip TINYINT)`,
			`CREATE TABLE orders(id INT PRIMARY KEY, customer_id INT, product VARCHAR(64), amount DECIMAL(10,2), status VARCHAR(32), updated_at DATETIME(3))`,
			`INSERT INTO customers VALUES (1,'Alice Chen','13812341234','alice@example.com','Chengdu',1),(2,'Bob Li','13900005678','bob@example.com','Shanghai',0),(3,'Carol Wu','13700009999','carol@example.com','Beijing',1)`,
			`INSERT INTO orders VALUES (101,1,'Pro Plan',299.00,'paid',NOW(3)),(102,2,'Starter',49.00,'pending',NOW(3)),(103,1,'Add-on',19.00,'paid',NOW(3))`,
		}
	} else {
		stmts = []string{
			`DROP TABLE IF EXISTS orders`,
			`DROP TABLE IF EXISTS customers`,
			`CREATE TABLE customers(id INT PRIMARY KEY, name TEXT, phone TEXT, email TEXT, city TEXT, vip BOOLEAN)`,
			`CREATE TABLE orders(id INT PRIMARY KEY, customer_id INT, product TEXT, amount NUMERIC, status TEXT, updated_at TIMESTAMPTZ DEFAULT now())`,
			`INSERT INTO customers VALUES (1,'Alice Chen','13812341234','alice@example.com','Chengdu',true),(2,'Bob Li','13900005678','bob@example.com','Shanghai',false),(3,'Carol Wu','13700009999','carol@example.com','Beijing',true)`,
			`INSERT INTO orders(id,customer_id,product,amount,status) VALUES (101,1,'Pro Plan',299.00,'paid'),(102,2,'Starter',49.00,'pending'),(103,1,'Add-on',19.00,'paid')`,
		}
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

func updateOrderStatus(ctx context.Context, db *sql.DB, driver string, id int, status string) error {
	var q string
	if driver == "mysql" {
		q = "UPDATE orders SET status=?, updated_at=NOW(3) WHERE id=?"
	} else {
		q = "UPDATE orders SET status=$1, updated_at=now() WHERE id=$2"
	}
	_, err := db.ExecContext(ctx, q, status, id)
	return err
}
