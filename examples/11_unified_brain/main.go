// Unified support brain — a complex, multi-source CortexDB application.
//
// It unifies TWO live databases into one privacy-safe knowledge graph and keeps
// them continuously in sync, then reasons over the result:
//
//	Postgres (customers, orders)  ─┐  connector desensitize ┐
//	MySQL    (tickets, free-text)  ┘  (signed plan)          ├─▶ one CortexDB brain
//	                                                          │
//	  continuous CDC:  PG logical replication + MySQL binlog ─┘  (both stream concurrently)
//	                                                          │
//	  reasoning:  RDFS inference · SPARQL aggregates · SHACL validation
//
// What it demonstrates beyond a basic RAG demo:
//   - DUAL SOURCE: customers/orders from Postgres + support tickets from MySQL,
//     merged into a single knowledge graph (Customer ──placed──▶ Order,
//     Customer ──raised──▶ Ticket).
//   - FREE-TEXT PII: ticket bodies contain phones/emails in prose; the connector
//     redacts them in place before indexing.
//   - STREAMING CDC from BOTH at once: a Postgres logical-replication watcher and
//     a MySQL binlog watcher run concurrently; live INSERT/UPDATE/DELETE in either
//     database flow into the brain within milliseconds.
//   - REASONING: RDFS-lite inference materializes derived types; a SPARQL
//     aggregate finds "priority" customers (>=2 paid orders); SHACL validates a
//     data-quality constraint and catches a real gap.
//
// No LLM or embedder required — deterministic lexical retrieval + SPARQL.
//
// Prerequisites: a Postgres with wal_level=logical and a MySQL with ROW binlog
// (both are the defaults for `postgres:16 -c wal_level=logical` and `mysql:8`).
//
// Usage:
//
//	go run ./examples/11_unified_brain \
//	  -pg  'postgres://postgres:p@localhost:5432/postgres?sslmode=disable' \
//	  -my  'root:p@tcp(localhost:3306)/test'
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

const (
	classCustomer = "urn:cortexdb:class:Customer"
	classParty    = "urn:cortexdb:class:Party"
	propCity      = "urn:cortexdb:prop:city"
	relPlaced     = "urn:cortexdb:rel:placed"
	propStatus    = "urn:cortexdb:prop:status"
	rdfsSubClassOf = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
)

var key = connector.StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef"))

func main() {
	pgDSN := flag.String("pg", "", "Postgres DSN (wal_level=logical)")
	myDSN := flag.String("my", "", "MySQL DSN (ROW binlog)")
	flag.Parse()
	if *pgDSN == "" || *myDSN == "" {
		log.Fatal("set -pg and -my DSNs")
	}
	if err := run(*pgDSN, *myDSN); err != nil {
		log.Fatal(err)
	}
}

func run(pgDSN, myDSN string) error {
	ctx := context.Background()

	pgAdmin, err := sql.Open("pgx", pgDSN)
	if err != nil {
		return err
	}
	defer pgAdmin.Close()
	myAdmin, err := sql.Open("mysql", myDSN)
	if err != nil {
		return err
	}
	defer myAdmin.Close()

	if err := seedPostgres(ctx, pgAdmin); err != nil {
		return fmt.Errorf("seed pg: %w", err)
	}
	if err := seedMySQL(ctx, myAdmin); err != nil {
		return fmt.Errorf("seed my: %w", err)
	}
	fmt.Println("seeded Postgres(customers,orders) + MySQL(tickets with free-text PII)")

	dir, err := os.MkdirTemp("", "unified-brain")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		return err
	}
	defer db.Close()

	vault, err := connector.OpenSQLiteVault(filepath.Join(dir, "tenant.vault.db"))
	if err != nil {
		return err
	}
	defer vault.Close()

	// --- 1. initial desensitized import from BOTH databases ---------------------
	pgMapping := pgImportMapping()
	myMapping := myImportMapping()

	pgDesens, err := desensitizerFor(ctx, "postgres", pgDSN, vault, pgPlan)
	if err != nil {
		return err
	}
	myDesens, err := desensitizerFor(ctx, "mysql", myDSN, vault, myPlan)
	if err != nil {
		return err
	}

	pgSrc, err := connector.NewPostgresSource(pgDSN, connector.SourceOptions{Tables: []string{"customers", "orders"}})
	if err != nil {
		return err
	}
	if _, err := importflow.New(db).Run(ctx, connector.Desensitized(pgSrc, pgDesens), pgMapping); err != nil {
		pgSrc.Close()
		return err
	}
	pgSrc.Close()

	mySrc, err := connector.NewMySQLSource(myDSN, connector.SourceOptions{Tables: []string{"tickets"}})
	if err != nil {
		return err
	}
	if _, err := importflow.New(db).Run(ctx, connector.Desensitized(mySrc, myDesens), myMapping); err != nil {
		mySrc.Close()
		return err
	}
	mySrc.Close()
	fmt.Println("initial import done — both sources merged into one knowledge graph")

	// Prove free-text PII in ticket bodies was redacted in place.
	fmt.Println("\n=== ticket bodies (free-text PII redacted) ===")
	tk, _ := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: "ticket order issue", TopK: 5})
	for _, h := range tk.Results {
		full, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: h.KnowledgeID})
		fmt.Printf("  • %s\n", full.Knowledge.Content)
	}

	// --- 2. continuous streaming CDC from BOTH databases concurrently -----------
	cdcCtx, stopCDC := context.WithCancel(ctx)
	defer stopCDC()
	pgErr := startPostgresCDC(cdcCtx, db, pgDSN, vault, pgMapping)
	myErr := startMySQLCDC(cdcCtx, db, myDSN, vault, myMapping)
	time.Sleep(1500 * time.Millisecond) // let both replication streams attach
	fmt.Println("\nstreaming CDC attached: Postgres logical replication + MySQL binlog")

	// Live change in Postgres: a new paid order for customer 2.
	mustExec(pgAdmin, `INSERT INTO orders(id,customer_id,product,amount,status) VALUES (104,2,'Pro Plan',299.00,'paid')`)
	// Live change in MySQL: a new ticket for customer 1 with PII in the body.
	mustExec(myAdmin, `INSERT INTO tickets(id,customer_id,subject,body,status,updated_at) VALUES (203,1,'refund','Please call me on 13955557777 or email me at urgent@x.com about order 104','open',NOW(3))`)

	waitFor(8*time.Second, func() bool { return exists(ctx, db, "104") && exists(ctx, db, "203") })
	fmt.Println("\n=== live CDC landed (both streams) ===")
	o104, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "104"})
	t203, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "203"})
	fmt.Printf("  PG  → %s\n", o104.Knowledge.Content)
	fmt.Printf("  MySQL→ %s\n", t203.Knowledge.Content)
	if contains(t203.Knowledge.Content, "13955557777") || contains(t203.Knowledge.Content, "urgent@x.com") {
		return fmt.Errorf("PII leaked through CDC free-text path: %q", t203.Knowledge.Content)
	}

	// --- 3. reasoning + constraints over the unified graph ----------------------
	// 3a. RDFS-lite inference: Customer rdfs:subClassOf Party → every customer is
	//     inferred to be a Party.
	if _, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: []cortexdb.KnowledgeGraphTriple{
			{Subject: graph.NewIRI(classCustomer), Predicate: graph.NewIRI(rdfsSubClassOf), Object: graph.NewIRI(classParty)},
		},
	}); err != nil {
		return err
	}
	inf, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{})
	if err != nil {
		return err
	}
	parties, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `SELECT (COUNT(?c) AS ?n) WHERE { ?c <` + graph.RDFType + `> <` + classParty + `> . }`,
	})
	fmt.Printf("\n=== RDFS inference ===\n  materialized %d inferred triples; Party members (inferred): %s\n",
		inf.Result.InferredCount, firstBinding(parties, "n"))

	// 3b. SPARQL aggregate: priority customers = >=2 paid orders.
	fmt.Println("\n=== priority customers (>=2 paid orders, SPARQL aggregate) ===")
	pri, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `SELECT ?c (COUNT(?o) AS ?paid) WHERE {
			?c <` + relPlaced + `> ?o .
			?o <` + propStatus + `> "paid" .
		} GROUP BY ?c HAVING (COUNT(?o) >= 2)`,
	})
	if err != nil {
		return err
	}
	for _, row := range pri.Result.Bindings {
		fmt.Printf("  • %s  (%s paid orders)\n", row["c"].Value, row["paid"].Value)
	}

	// 3c. SHACL: every Customer must have a city. Customer 4 was seeded without one.
	fmt.Println("\n=== SHACL validation: Customer must have a city ===")
	report, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{
		Shapes: customerCityShape(),
	})
	if err != nil {
		return err
	}
	fmt.Printf("  conforms=%v\n", report.Report.Conforms)
	for _, r := range report.Report.Results {
		fmt.Printf("  ✗ violation: %s — %s\n", r.FocusNode.Value, r.Message)
	}

	stopCDC()
	if e := drain(pgErr); e != nil {
		fmt.Printf("(pg cdc ended: %v)\n", e)
	}
	if e := drain(myErr); e != nil {
		fmt.Printf("(my cdc ended: %v)\n", e)
	}
	fmt.Println("\n✓ unified brain: 2 live DBs → desensitized → merged KG → dual streaming CDC → RDFS + SPARQL + SHACL reasoning")
	return nil
}

// --- masking plans (human-reviewed) -----------------------------------------

func pgPlan(p *connector.MaskingPlan) {
	set(p, "customers", "name", connector.ActionPseudonymize)
	set(p, "customers", "phone", connector.ActionMask)
	set(p, "customers", "email", connector.ActionMask)
	for _, c := range []string{"id", "city", "vip"} {
		set(p, "customers", c, connector.ActionKeep)
	}
	for _, c := range []string{"id", "customer_id", "product", "amount", "status", "updated_at"} {
		set(p, "orders", c, connector.ActionKeep)
	}
}

func myPlan(p *connector.MaskingPlan) {
	for _, c := range []string{"id", "customer_id", "subject", "status", "updated_at"} {
		set(p, "tickets", c, connector.ActionKeep)
	}
	set(p, "tickets", "body", connector.ActionKeep) // kept but free-text scanned
	p.TextScan = append(p.TextScan, connector.TextScanRule{Table: "tickets", Column: "body"})
}

func desensitizerFor(ctx context.Context, driver, dsn string, vault connector.Vault, edit func(*connector.MaskingPlan)) (*connector.Desensitizer, error) {
	var src importflow.Source
	var err error
	if driver == "mysql" {
		src, err = connector.NewMySQLSource(dsn, connector.SourceOptions{})
	} else {
		src, err = connector.NewPostgresSource(dsn, connector.SourceOptions{})
	}
	if err != nil {
		return nil, err
	}
	defer src.Close()
	plan, err := connector.BuildMaskingPlan(ctx, src, connector.NewRuleClassifier(), connector.PlanOptions{})
	if err != nil {
		return nil, err
	}
	edit(&plan)
	plan.Sign("data-owner", time.Now())
	return connector.NewDesensitizer(plan, connector.DesensitizerOptions{Tenant: "acme", KeyProvider: key, Vault: vault})
}

// --- import mappings ---------------------------------------------------------

func pgImportMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
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
}

func myImportMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"tickets": {
			RAG: &importflow.RAGPlan{ContentTmpl: "Ticket {id} ({status}): {subject} — {body}", IDColumn: "id"},
			KG: &importflow.KGPlan{
				Entities: []importflow.EntityMap{
					{Ref: "cust", Type: "Customer", IDTmpl: "{customer_id}"},
					{Ref: "tk", Type: "Ticket", IDTmpl: "{id}", Props: []string{"status"}},
				},
				Relations: []importflow.RelationMap{{Subject: "cust", Predicate: "raised", Object: "tk"}},
			},
		},
	}}
}

// --- streaming CDC watchers --------------------------------------------------

func startPostgresCDC(ctx context.Context, db *cortexdb.DB, dsn string, vault connector.Vault, mapping importflow.MappingPlan) <-chan error {
	out := make(chan error, 1)
	d, err := desensitizerFor(ctx, "postgres", dsn, vault, pgPlan)
	if err != nil {
		out <- err
		return out
	}
	src, err := connector.NewPostgresCDCSource(dsn, connector.PostgresCDCOptions{
		Publication: "cx_pub", Slot: "cx_slot",
		Tables: map[string][]string{"customers": {"id"}, "orders": {"id"}},
	})
	if err != nil {
		out <- err
		return out
	}
	cp, _ := connector.NewSQLiteCheckpointStore(db)
	w, err := connector.NewWatcher(db, src, connector.WatcherOptions{
		SourceKey: "pg-cdc", Desensitizer: d, Checkpoint: cp, Mapping: mapping,
	})
	if err != nil {
		out <- err
		return out
	}
	go func() { out <- w.Run(ctx) }()
	return out
}

func startMySQLCDC(ctx context.Context, db *cortexdb.DB, dsn string, vault connector.Vault, mapping importflow.MappingPlan) <-chan error {
	out := make(chan error, 1)
	d, err := desensitizerFor(ctx, "mysql", dsn, vault, myPlan)
	if err != nil {
		out <- err
		return out
	}
	src, err := connector.NewMySQLBinlogSource(dsn, connector.MySQLBinlogOptions{
		ServerID: 4101, Tables: map[string][]string{"tickets": {"id"}},
	})
	if err != nil {
		out <- err
		return out
	}
	cp, _ := connector.NewSQLiteCheckpointStore(db)
	w, err := connector.NewWatcher(db, src, connector.WatcherOptions{
		SourceKey: "my-cdc", Desensitizer: d, Checkpoint: cp, Mapping: mapping,
	})
	if err != nil {
		out <- err
		return out
	}
	go func() { out <- w.Run(ctx) }()
	return out
}

// --- SHACL shape -------------------------------------------------------------

func customerCityShape() []cortexdb.KnowledgeGraphTriple {
	const shape = "urn:cortexdb:shape:CustomerCity"
	const prop = "urn:cortexdb:shape:CustomerCityProp"
	return []cortexdb.KnowledgeGraphTriple{
		{Subject: graph.NewIRI(shape), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
		{Subject: graph.NewIRI(shape), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI(classCustomer)},
		{Subject: graph.NewIRI(shape), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI(prop)},
		{Subject: graph.NewIRI(prop), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI(propCity)},
		{Subject: graph.NewIRI(prop), Predicate: graph.NewIRI(graph.SHACLMinCount), Object: graph.NewLiteral("1")},
	}
}

// --- small helpers -----------------------------------------------------------

func set(p *connector.MaskingPlan, table, col string, a connector.MaskAction) {
	for i := range p.Columns {
		if p.Columns[i].Table == table && p.Columns[i].Column == col {
			p.Columns[i].Action = a
			return
		}
	}
}

func exists(ctx context.Context, db *cortexdb.DB, id string) bool {
	_, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: id})
	return err == nil
}

func firstBinding(r *cortexdb.KnowledgeGraphQueryResponse, v string) string {
	if r == nil || len(r.Result.Bindings) == 0 {
		return "0"
	}
	return r.Result.Bindings[0][v].Value
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func waitFor(d time.Duration, cond func() bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func drain(ch <-chan error) error {
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		return nil
	}
}

func mustExec(db *sql.DB, q string, args ...any) {
	if _, err := db.Exec(q, args...); err != nil {
		log.Fatalf("exec %q: %v", q, err)
	}
}

// --- seeding -----------------------------------------------------------------

func seedPostgres(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS customers`,
		`DROP PUBLICATION IF EXISTS cx_pub`,
		`SELECT pg_drop_replication_slot('cx_slot') WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name='cx_slot')`,
		`CREATE TABLE customers(id INT PRIMARY KEY, name TEXT, phone TEXT, email TEXT, city TEXT, vip BOOLEAN)`,
		`CREATE TABLE orders(id INT PRIMARY KEY, customer_id INT, product TEXT, amount NUMERIC, status TEXT, updated_at TIMESTAMPTZ DEFAULT now())`,
		// customer 4 has NO city → SHACL will catch it.
		`INSERT INTO customers VALUES (1,'Alice Chen','13812341234','alice@example.com','Chengdu',true),(2,'Bob Li','13900005678','bob@example.com','Shanghai',false),(3,'Carol Wu','13700009999','carol@example.com','Beijing',true),(4,'Dan Ho','13611112222','dan@example.com',NULL,false)`,
		`INSERT INTO orders(id,customer_id,product,amount,status) VALUES (101,1,'Pro Plan',299.00,'paid'),(102,2,'Starter',49.00,'pending'),(103,1,'Add-on',19.00,'paid')`,
		`CREATE PUBLICATION cx_pub FOR TABLE customers, orders`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

func seedMySQL(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS tickets`,
		`CREATE TABLE tickets(id INT PRIMARY KEY, customer_id INT, subject VARCHAR(128), body TEXT, status VARCHAR(32), updated_at DATETIME(3))`,
		`INSERT INTO tickets VALUES (201,1,'late delivery','Hi, my order is late. Reach me at 13812341234 or alice@example.com.','open',NOW(3)),(202,3,'refund request','Card charged twice, call 13700009999 to confirm.','open',NOW(3))`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}
