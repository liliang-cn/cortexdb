// Scale + analytics — a comprehensive, larger-volume CortexDB example.
//
// It bulk-loads tens of thousands of rows from TWO live databases through the
// privacy connector into one knowledge graph, reports ingest throughput, runs
// analytics at scale (SPARQL aggregates, RDFS inference, SHACL validation), and
// then keeps the brain in sync under a concurrent write load via Postgres
// logical-replication CDC.
//
// Everything is deterministic — no LLM, no embedder. Retrieval is lexical, graph
// analytics is SPARQL/RDFS/SHACL.
//
// Volume is configurable (defaults are "clearly large, ~1-2 min"):
//
//	go run ./examples/13_scale_analytics \
//	  -pg 'postgres://postgres:p@localhost:5432/postgres?sslmode=disable' \
//	  -my 'root:p@tcp(localhost:3306)/test' \
//	  -customers 4000 -orders 20000 -tickets 1500 -load 500
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

const (
	classCustomer  = "urn:cortexdb:class:Customer"
	classParty     = "urn:cortexdb:class:Party"
	propCity       = "urn:cortexdb:prop:city"
	propStatus     = "urn:cortexdb:prop:status"
	relPlaced      = "urn:cortexdb:rel:placed"
	rdfsSubClassOf = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
)

var (
	cities = []string{"Chengdu", "Shanghai", "Beijing", "Shenzhen", "Hangzhou", "Wuhan"}
	key    = connector.StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef"))
	rng    = rand.New(rand.NewSource(42)) // deterministic
)

func main() {
	pgDSN := flag.String("pg", "", "Postgres DSN (wal_level=logical)")
	myDSN := flag.String("my", "", "MySQL DSN (ROW binlog)")
	// Defaults are tuned to run in ~1-2 min; crank them up freely (ingest is
	// ~the throughput printed below, so e.g. 50k orders ≈ a few minutes).
	nCust := flag.Int("customers", 1500, "number of customers")
	nOrd := flag.Int("orders", 6000, "number of orders")
	nTick := flag.Int("tickets", 800, "number of support tickets")
	nLoad := flag.Int("load", 50, "number of live order updates for the CDC-under-load phase (each is applied as an idempotent upsert)")
	flag.Parse()
	if *pgDSN == "" || *myDSN == "" {
		log.Fatal("set -pg and -my DSNs")
	}
	if err := run(*pgDSN, *myDSN, *nCust, *nOrd, *nTick, *nLoad); err != nil {
		log.Fatal(err)
	}
}

func run(pgDSN, myDSN string, nCust, nOrd, nTick, nLoad int) error {
	ctx := context.Background()
	pg, err := sql.Open("pgx", pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	my, err := sql.Open("mysql", myDSN)
	if err != nil {
		return err
	}
	defer my.Close()

	// --- bulk seed ---------------------------------------------------------------
	t0 := time.Now()
	if err := seedPG(ctx, pg, nCust, nOrd); err != nil {
		return fmt.Errorf("seed pg: %w", err)
	}
	if err := seedMy(ctx, my, nTick, nCust); err != nil {
		return fmt.Errorf("seed my: %w", err)
	}
	fmt.Printf("seeded %d customers + %d orders (Postgres) + %d tickets (MySQL) in %s\n",
		nCust, nOrd, nTick, time.Since(t0).Round(time.Millisecond))

	dir, err := os.MkdirTemp("", "scale")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		return err
	}
	defer db.Close()
	vault, err := connector.OpenSQLiteVault(filepath.Join(dir, "v.db"))
	if err != nil {
		return err
	}
	defer vault.Close()

	// --- timed desensitized import at scale -------------------------------------
	imp := importflow.New(db, importflow.WithBatchSize(1000))
	tImp := time.Now()

	pgD, err := desensitizer(ctx, "postgres", pgDSN, vault, pgPlan)
	if err != nil {
		return err
	}
	pgSrc, _ := connector.NewPostgresSource(pgDSN, connector.SourceOptions{Tables: []string{"customers", "orders"}})
	pgRep, err := imp.Run(ctx, connector.Desensitized(pgSrc, pgD), pgMapping())
	pgSrc.Close()
	if err != nil {
		return err
	}
	myD, err := desensitizer(ctx, "mysql", myDSN, vault, myPlan)
	if err != nil {
		return err
	}
	mySrc, _ := connector.NewMySQLSource(myDSN, connector.SourceOptions{Tables: []string{"tickets"}})
	myRep, err := imp.Run(ctx, connector.Desensitized(mySrc, myD), myMapping())
	mySrc.Close()
	if err != nil {
		return err
	}
	impDur := time.Since(tImp)
	rows := pgRep.RowsRead + myRep.RowsRead
	chunks := pgRep.ChunksIndexed + myRep.ChunksIndexed
	triples := pgRep.TriplesCreated + myRep.TriplesCreated
	fmt.Printf("\n=== ingest throughput (desensitized) ===\n")
	fmt.Printf("  %d rows → %d RAG chunks + %d KG triples in %s\n", rows, chunks, triples, impDur.Round(time.Millisecond))
	fmt.Printf("  %.0f rows/s · %.0f chunks/s · %.0f triples/s\n",
		rate(rows, impDur), rate(chunks, impDur), rate(triples, impDur))

	// --- analytics at scale (SPARQL aggregates) ---------------------------------
	fmt.Printf("\n=== analytics over the graph (%d orders) ===\n", nOrd)
	timed("orders by status (GROUP BY)", func() {
		q := `SELECT ?s (COUNT(?o) AS ?n) WHERE { ?o <` + propStatus + `> ?s . } GROUP BY ?s`
		res, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: q})
		if err != nil {
			fmt.Printf("    (query error: %v)\n", err)
			return
		}
		for _, r := range res.Result.Bindings {
			fmt.Printf("    %s: %s orders\n", r["s"].Value, r["n"].Value)
		}
	})
	timed("priority customers (>=5 paid orders)", func() {
		q := `SELECT (COUNT(?c) AS ?n) WHERE {
			{ SELECT ?c WHERE { ?c <` + relPlaced + `> ?o . ?o <` + propStatus + `> "paid" . }
			  GROUP BY ?c HAVING (COUNT(?o) >= 5) }
		}`
		res, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: q})
		if err != nil {
			fmt.Printf("    (query error: %v)\n", err)
			return
		}
		fmt.Printf("    %s customers have >=5 paid orders\n", firstVal(res, "n"))
	})

	// --- RDFS inference at scale ------------------------------------------------
	timed("RDFS inference (Customer subClassOf Party)", func() {
		_, _ = db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: []cortexdb.KnowledgeGraphTriple{
			{Subject: graph.NewIRI(classCustomer), Predicate: graph.NewIRI(rdfsSubClassOf), Object: graph.NewIRI(classParty)},
		}})
		inf, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{})
		if err != nil {
			fmt.Printf("    (inference error: %v)\n", err)
			return
		}
		fmt.Printf("    materialized %d inferred triples\n", inf.Result.InferredCount)
	})

	// --- SHACL validation at scale ----------------------------------------------
	timed("SHACL: every Customer must have a city", func() {
		rep, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{Shapes: cityShape()})
		if err != nil {
			fmt.Printf("    (shacl error: %v)\n", err)
			return
		}
		fmt.Printf("    conforms=%v, violations=%d (customers seeded without a city)\n", rep.Report.Conforms, len(rep.Report.Results))
	})

	// --- lexical RAG at scale ---------------------------------------------------
	timed(fmt.Sprintf("lexical search over %d chunks", chunks), func() {
		res, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: "refund late delivery", TopK: 3})
		if err != nil {
			fmt.Printf("    (search error: %v)\n", err)
			return
		}
		fmt.Printf("    top hit: %s\n", short(firstContent(ctx, db, res)))
	})

	// --- CDC under concurrent write load ----------------------------------------
	fmt.Printf("\n=== CDC under load: %d live order updates via Postgres logical replication ===\n", nLoad)
	if err := cdcUnderLoad(ctx, db, pg, pgDSN, vault, nLoad, nOrd); err != nil {
		return err
	}

	fmt.Println("\n✓ comprehensive scale run complete")
	return nil
}

// cdcUnderLoad starts a logical-replication watcher, fires nLoad order updates at
// the source, and reports how quickly the brain catches up.
func cdcUnderLoad(ctx context.Context, db *cortexdb.DB, pg *sql.DB, pgDSN string, vault connector.Vault, nLoad, nOrd int) error {
	d, err := desensitizer(ctx, "postgres", pgDSN, vault, pgPlan)
	if err != nil {
		return err
	}
	src, err := connector.NewPostgresCDCSource(pgDSN, connector.PostgresCDCOptions{
		Publication: "sc_pub", Slot: "sc_slot", Tables: map[string][]string{"orders": {"id"}},
	})
	if err != nil {
		return err
	}
	cp, _ := connector.NewSQLiteCheckpointStore(db)
	w, err := connector.NewWatcher(db, src, connector.WatcherOptions{
		SourceKey: "scale-cdc", Desensitizer: d, Checkpoint: cp, Mapping: ordersOnlyMapping(),
	})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- w.Run(runCtx) }()
	time.Sleep(1500 * time.Millisecond) // attach replication
	select { // surface an early replication failure instead of hiding it
	case e := <-runErr:
		return fmt.Errorf("cdc watcher failed to start: %w", e)
	default:
	}

	// fire the write load: flip nLoad random orders to 'shipped'
	tLoad := time.Now()
	lastID := 0
	for i := 0; i < nLoad; i++ {
		id := 1 + rng.Intn(nOrd)
		if _, err := pg.ExecContext(ctx, `UPDATE orders SET status='shipped', updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		lastID = id
	}
	wroteIn := time.Since(tLoad)

	// wait until the last updated order reflects 'shipped' in the brain
	tSync := time.Now()
	want := "status shipped"
	ok := false
	for time.Now().Before(tSync.Add(30 * time.Second)) {
		full, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: strconv.Itoa(lastID)})
		if err == nil && strings.Contains(full.Knowledge.Content, want) {
			ok = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Diagnostic: count how many orders now show 'shipped' in the brain, so a slow
	// apply never looks like a broken stream.
	shipped := 0
	if res, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `SELECT (COUNT(?o) AS ?n) WHERE { ?o <` + propStatus + `> "shipped" . }`,
	}); err == nil {
		shipped, _ = strconv.Atoi(firstVal(res, "n"))
	}
	select { // surface any replication error that occurred during the load
	case e := <-runErr:
		fmt.Printf("  ⚠ cdc stream error: %v\n", e)
	default:
	}
	fmt.Printf("  wrote %d updates in %s; %d orders now 'shipped' in the brain; last update reflected %s after write (synced=%v)\n",
		nLoad, wroteIn.Round(time.Millisecond), shipped, time.Since(tSync).Round(time.Millisecond), ok)
	return nil
}

// --- masking plans, mappings -------------------------------------------------

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
	set(p, "tickets", "body", connector.ActionKeep)
	p.TextScan = append(p.TextScan, connector.TextScanRule{Table: "tickets", Column: "body"})
}

func pgMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"customers": {
			RAG: &importflow.RAGPlan{ContentTmpl: "Customer {name} in {city}, VIP={vip}", IDColumn: "id"},
			KG:  &importflow.KGPlan{Entities: []importflow.EntityMap{{Ref: "c", Type: "Customer", IDTmpl: "{id}", Props: []string{"city", "vip"}}}},
		},
		"orders": ordersTablePlan(),
	}}
}

func ordersOnlyMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{"orders": ordersTablePlan()}}
}

func ordersTablePlan() importflow.TablePlan {
	return importflow.TablePlan{
		RAG: &importflow.RAGPlan{ContentTmpl: "Order {id}: {product}, amount {amount}, status {status}", IDColumn: "id"},
		KG: &importflow.KGPlan{
			Entities: []importflow.EntityMap{
				{Ref: "cust", Type: "Customer", IDTmpl: "{customer_id}"},
				{Ref: "ord", Type: "Order", IDTmpl: "{id}", Props: []string{"status"}},
			},
			Relations: []importflow.RelationMap{{Subject: "cust", Predicate: "placed", Object: "ord"}},
		},
	}
}

func myMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"tickets": {RAG: &importflow.RAGPlan{ContentTmpl: "Ticket {id} ({status}): {subject} — {body}", IDColumn: "id"}},
	}}
}

func desensitizer(ctx context.Context, driver, dsn string, vault connector.Vault, edit func(*connector.MaskingPlan)) (*connector.Desensitizer, error) {
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

func cityShape() []cortexdb.KnowledgeGraphTriple {
	const s, p = "urn:cortexdb:shape:CityShape", "urn:cortexdb:shape:CityProp"
	return []cortexdb.KnowledgeGraphTriple{
		{Subject: graph.NewIRI(s), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
		{Subject: graph.NewIRI(s), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI(classCustomer)},
		{Subject: graph.NewIRI(s), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI(p)},
		{Subject: graph.NewIRI(p), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI(propCity)},
		{Subject: graph.NewIRI(p), Predicate: graph.NewIRI(graph.SHACLMinCount), Object: graph.NewLiteral("1")},
	}
}

// --- bulk seeding (batched multi-row INSERT) --------------------------------

func seedPG(ctx context.Context, db *sql.DB, nCust, nOrd int) error {
	stmts := []string{
		`DROP TABLE IF EXISTS orders`, `DROP TABLE IF EXISTS customers`,
		`DROP PUBLICATION IF EXISTS sc_pub`,
		`SELECT pg_drop_replication_slot('sc_slot') WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name='sc_slot')`,
		`CREATE TABLE customers(id INT PRIMARY KEY, name TEXT, phone TEXT, email TEXT, city TEXT, vip BOOLEAN)`,
		`CREATE TABLE orders(id INT PRIMARY KEY, customer_id INT, product TEXT, amount NUMERIC, status TEXT, updated_at TIMESTAMPTZ DEFAULT now())`,
		`CREATE PUBLICATION sc_pub FOR TABLE orders`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	// customers (every 17th has no city → SHACL violations)
	if err := batchInsert(ctx, db, nCust, 1000, func(i int) string {
		city := "'" + cities[i%len(cities)] + "'"
		if i%17 == 0 {
			city = "NULL"
		}
		vip := "false"
		if i%5 == 0 {
			vip = "true"
		}
		return fmt.Sprintf("(%d,'Cust %d','138%08d','c%d@example.com',%s,%s)", i, i, i, i, city, vip)
	}, "INSERT INTO customers(id,name,phone,email,city,vip) VALUES "); err != nil {
		return err
	}
	products := []string{"Pro Plan", "Starter", "Add-on", "Enterprise", "Trial"}
	return batchInsert(ctx, db, nOrd, 1000, func(i int) string {
		status := "paid"
		if i%3 == 0 {
			status = "pending"
		}
		return fmt.Sprintf("(%d,%d,'%s',%d.00,'%s')", i, 1+rng.Intn(nCust), products[i%len(products)], 10+rng.Intn(490), status)
	}, "INSERT INTO orders(id,customer_id,product,amount,status) VALUES ")
}

func seedMy(ctx context.Context, db *sql.DB, nTick, nCust int) error {
	for _, s := range []string{
		`DROP TABLE IF EXISTS tickets`,
		`CREATE TABLE tickets(id INT PRIMARY KEY, customer_id INT, subject VARCHAR(128), body TEXT, status VARCHAR(32), updated_at DATETIME(3))`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return batchInsert(ctx, db, nTick, 1000, func(i int) string {
		// free-text body with embedded PII (phone + email) to exercise redaction
		body := fmt.Sprintf("Order is late, reach me at 138%08d or user%d@example.com please.", i, i)
		return fmt.Sprintf("(%d,%d,'late delivery','%s','open',NOW(3))", i, 1+rng.Intn(nCust), body)
	}, "INSERT INTO tickets(id,customer_id,subject,body,status,updated_at) VALUES ")
}

// batchInsert builds multi-row INSERTs of `batch` rows. ids run 1..n.
func batchInsert(ctx context.Context, db *sql.DB, n, batch int, row func(i int) string, prefix string) error {
	for start := 1; start <= n; start += batch {
		end := start + batch
		if end > n+1 {
			end = n + 1
		}
		var b strings.Builder
		b.WriteString(prefix)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			b.WriteString(row(i))
		}
		if _, err := db.ExecContext(ctx, b.String()); err != nil {
			return err
		}
	}
	return nil
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

func timed(label string, fn func()) {
	t := time.Now()
	fmt.Printf("  • %s:\n", label)
	fn()
	fmt.Printf("    (%s)\n", time.Since(t).Round(time.Millisecond))
}

func rate(n int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / d.Seconds()
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

func firstVal(r *cortexdb.KnowledgeGraphQueryResponse, v string) string {
	if r == nil || len(r.Result.Bindings) == 0 {
		return "0"
	}
	return r.Result.Bindings[0][v].Value
}

func firstContent(ctx context.Context, db *cortexdb.DB, res *cortexdb.KnowledgeSearchResponse) string {
	if res == nil || len(res.Results) == 0 {
		return "(no hits)"
	}
	full, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: res.Results[0].KnowledgeID})
	return full.Knowledge.Content
}
