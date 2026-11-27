package cortexdb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A simulated company for the vector-then-graph recall (#272): 40 people,
// five products that depend on each other, team pages, product docs, on-call
// rotas and a leave calendar as knowledge, and a month of change events —
// an owner handing over, an on-call swap for a leave, a schema migration and
// the incident it caused — as memories, each saved with its entities and
// relations the way an extractor would record them.
//
// The questions are the ones such a company asks: some answered by one
// document, some only by following a relation from the passage that matches
// the words to the one that holds the answer ("who is on call for the service
// that sends Ember's notifications" — Ember's doc names Beacon, the rota
// names Beacon's on-call, the swap names who actually holds it).
//
// It runs against a real embedding model, and reports for every question
// what the vector stage found, what the graph walk reached and what the
// fused ranking returned:
//
//	CORTEXDB_SCENARIO_EMBED_URL=http://localhost:11434/v1 \
//	CORTEXDB_SCENARIO_EMBED_MODEL=embeddinggemma \
//	go test ./pkg/cortexdb -run TestCompanyScenario -v

type scenarioProduct struct {
	name, about string
	dependsOn   map[string]string // product -> what for
}

var scenarioProducts = []scenarioProduct{
	{name: "Atlas", about: "the billing and invoicing service: subscriptions, invoices, refunds and in-app purchase receipts", dependsOn: map[string]string{"Delta": "usage events for metered billing"}},
	{name: "Beacon", about: "the notification service: push notifications, email and SMS delivery", dependsOn: map[string]string{"Delta": "the user event stream that triggers notifications"}},
	{name: "Cobalt", about: "the search service: product search, autocomplete and the search index", dependsOn: map[string]string{"Delta": "the nightly catalogue export it indexes"}},
	{name: "Delta", about: "the data platform: the event pipeline, the warehouse and the schema registry"},
	{name: "Ember", about: "the mobile app for iOS and Android", dependsOn: map[string]string{"Beacon": "push notifications", "Atlas": "in-app purchases and subscription status"}},
}

var scenarioNames = []string{
	"Lena Hartmann", "Zhou Wei", "Marco Bellini", "Aiko Tanaka", "Samuel Okafor", "Ines Duarte", "Pavel Novak",
	"Chen Jing", "Oliver Brandt", "Fatima Rahman", "Lucas Moreau", "Hana Kim", "Diego Ramos", "Sofia Lindqvist",
	"Wang Fang", "Tomasz Wrobel", "Amara Nwosu", "Julien Petit", "Mei Lin", "Erik Johansson", "Priya Raman",
	"Liu Yang", "Nora Fischer", "Kofi Mensah", "Elena Popescu", "Takumi Sato", "Clara Weiss", "Rafael Costa",
	"Zhang Min", "Anton Petrov", "Grace Mwangi", "Felix Wagner", "Yuki Mori", "Laura Conti", "Omar Haddad",
	"Sun Li", "Hugo Laurent", "Ingrid Berg", "Ravi Iyer", "Maja Kowalska",
}

type scenarioDoc struct {
	id, title, content string
	entities           []ToolEntityInput
	relations          []ToolRelationInput
}

type scenarioMemory struct {
	id, content string
	entities    []ToolEntityInput
	relations   []ToolRelationInput
}

type scenarioQuestion struct {
	id, kind, text string
	gold           []string // knowledge ids ("kn:") or memory ids ("mem:")
}

type scenarioCompany struct {
	docs      []scenarioDoc
	memories  []scenarioMemory
	questions []scenarioQuestion
}

func person(name string) ToolEntityInput  { return ToolEntityInput{Name: name, Type: "Person"} }
func product(name string) ToolEntityInput { return ToolEntityInput{Name: name, Type: "Product"} }
func rel(from, typ, to string) ToolRelationInput {
	return ToolRelationInput{From: from, Type: typ, To: to}
}

// buildScenarioCompany lays the company out from a fixed seed, so every run
// asks the same questions of the same documents.
func buildScenarioCompany() scenarioCompany {
	r := rand.New(rand.NewSource(272))
	names := append([]string(nil), scenarioNames...)
	r.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })

	teams := map[string][]string{} // product -> members, lead first
	for i, p := range scenarioProducts {
		teams[p.name] = names[i*7 : i*7+7]
	}
	cto, sre1, sre2, pmMobile, pmPlatform := names[35], names[36], names[37], names[38], names[39]
	cities := []string{"Berlin", "Lisbon", "Shanghai", "Toronto", "Warsaw", "Nairobi", "Osaka", "Madrid"}
	skills := map[string]string{
		"Atlas":  "subscriptions, invoicing and payment providers",
		"Beacon": "push delivery, APNs and FCM, email templates",
		"Cobalt": "ranking, the search index and autocomplete",
		"Delta":  "the event pipeline, Kafka and the warehouse",
		"Ember":  "the iOS and Android clients and app store releases",
	}

	var c scenarioCompany
	add := func(d scenarioDoc) { c.docs = append(c.docs, d) }
	mem := func(m scenarioMemory) { c.memories = append(c.memories, m) }
	slug := func(name string) string { return strings.ToLower(strings.ReplaceAll(name, " ", "-")) }

	// People.
	teamOf := map[string]string{}
	for _, p := range scenarioProducts {
		for i, m := range teams[p.name] {
			teamOf[m] = p.name
			role, reports := "Software engineer", teams[p.name][0]
			if i == 0 {
				role, reports = "Tech lead", cto
			}
			city := cities[r.Intn(len(cities))]
			add(scenarioDoc{
				id: "profile-" + slug(m), title: m,
				content:   fmt.Sprintf("%s — %s on the %s team. Based in %s. Works on %s. Reports to %s.", m, role, p.name, city, skills[p.name], reports),
				entities:  []ToolEntityInput{person(m), person(reports), {Name: p.name + " team", Type: "Team"}, {Name: city, Type: "City"}},
				relations: []ToolRelationInput{rel(m, "member_of", p.name+" team"), rel(m, "reports_to", reports), rel(m, "based_in", city)},
			})
		}
	}
	for _, x := range []struct{ who, role string }{{cto, "CTO"}, {sre1, "Site reliability engineer"}, {sre2, "Site reliability engineer"}, {pmMobile, "Product manager for Ember"}, {pmPlatform, "Product manager for Delta and Cobalt"}} {
		city := cities[r.Intn(len(cities))]
		add(scenarioDoc{id: "profile-" + slug(x.who), title: x.who,
			content:   fmt.Sprintf("%s — %s. Based in %s. Reports to %s.", x.who, x.role, city, cto),
			entities:  []ToolEntityInput{person(x.who), person(cto), {Name: city, Type: "City"}},
			relations: []ToolRelationInput{rel(x.who, "reports_to", cto), rel(x.who, "based_in", city)}})
	}

	// Product docs and runbooks.
	for _, p := range scenarioProducts {
		lead := teams[p.name][0]
		var b strings.Builder
		fmt.Fprintf(&b, "%s is %s. Owner and tech lead: %s. ", p.name, p.about, lead)
		ents := []ToolEntityInput{product(p.name), person(lead)}
		rels := []ToolRelationInput{rel(lead, "owns", p.name)}
		deps := make([]string, 0, len(p.dependsOn))
		for d := range p.dependsOn {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		for _, d := range deps {
			fmt.Fprintf(&b, "%s depends on %s for %s. ", p.name, d, p.dependsOn[d])
			ents = append(ents, product(d))
			rels = append(rels, rel(p.name, "depends_on", d))
		}
		fmt.Fprintf(&b, "Escalation: page the %s on-call first, then the owner.", p.name)
		add(scenarioDoc{id: "product-" + strings.ToLower(p.name), title: p.name + " product overview", content: b.String(), entities: ents, relations: rels})
		add(scenarioDoc{id: "runbook-" + strings.ToLower(p.name), title: p.name + " runbook",
			content:  fmt.Sprintf("%s runbook. Dashboards: latency, error rate and saturation. Rollback with the deploy tool to the previous release; a rollback needs no approval. Page the %s on-call for alerts.", p.name, p.name),
			entities: []ToolEntityInput{product(p.name)}})
	}

	// Team pages and the org chart.
	for _, p := range scenarioProducts {
		members := teams[p.name]
		ents := []ToolEntityInput{product(p.name), {Name: p.name + " team", Type: "Team"}}
		rels := []ToolRelationInput{rel(p.name+" team", "builds", p.name)}
		for _, m := range members {
			ents = append(ents, person(m))
			rels = append(rels, rel(m, "member_of", p.name+" team"))
		}
		add(scenarioDoc{id: "team-" + strings.ToLower(p.name), title: p.name + " team",
			content:  fmt.Sprintf("The %s team builds %s. Lead: %s. Engineers: %s.", p.name, p.name, members[0], strings.Join(members[1:], ", ")),
			entities: ents, relations: rels})
	}
	add(scenarioDoc{id: "org-chart", title: "Engineering organisation",
		content:   fmt.Sprintf("CTO: %s. Site reliability: %s and %s. Product managers: %s (mobile, Ember) and %s (platform: Delta, Cobalt). Atlas and Beacon have no product manager; their tech leads decide.", cto, sre1, sre2, pmMobile, pmPlatform),
		entities:  []ToolEntityInput{person(cto), person(sre1), person(sre2), person(pmMobile), person(pmPlatform), product("Ember"), product("Delta"), product("Cobalt")},
		relations: []ToolRelationInput{rel(pmMobile, "product_manager_of", "Ember"), rel(pmPlatform, "product_manager_of", "Delta"), rel(pmPlatform, "product_manager_of", "Cobalt")}})

	// On-call rotas, weeks 44 to 49.
	primary := func(p string, week int) string { return teams[p][1+week%5] }
	secondary := func(p string, week int) string { return teams[p][1+(week+1)%5] }
	for week := 44; week <= 49; week++ {
		var b strings.Builder
		fmt.Fprintf(&b, "On-call rota for week %d, 2026. ", week)
		var ents []ToolEntityInput
		var rels []ToolRelationInput
		for _, p := range scenarioProducts {
			fmt.Fprintf(&b, "%s: primary %s, secondary %s. ", p.name, primary(p.name, week), secondary(p.name, week))
			ents = append(ents, product(p.name), person(primary(p.name, week)), person(secondary(p.name, week)))
			rels = append(rels, rel(primary(p.name, week), "on_call_for", p.name), rel(secondary(p.name, week), "secondary_on_call_for", p.name))
		}
		add(scenarioDoc{id: fmt.Sprintf("oncall-w%d", week), title: fmt.Sprintf("On-call rota week %d", week), content: b.String(), entities: ents, relations: rels})
	}

	// Leave.
	beaconPrimary46, beaconCover46 := primary("Beacon", 46), teams["Beacon"][6]
	cobaltPrimary48, cobaltCover48 := primary("Cobalt", 48), teams["Cobalt"][6]
	canceller := teams["Ember"][6]
	onLeave := []struct{ who, when string }{
		{beaconPrimary46, "10-14 November"}, {teams["Delta"][6], "10-12 November"}, {sre2, "3-7 November"},
		{canceller, "24-28 November"}, {teams["Atlas"][6], "17-19 November"}, {pmMobile, "from 20 November (parental leave)"},
		{teams["Cobalt"][5], "17-21 November"}, {cto, "1-5 December"},
	}
	var lb strings.Builder
	lb.WriteString("Approved leave, November and December 2026. ")
	lents := []ToolEntityInput{{Name: "Leave calendar", Type: "Calendar"}}
	var lrels []ToolRelationInput
	for _, l := range onLeave {
		fmt.Fprintf(&lb, "%s: %s. ", l.who, l.when)
		lents = append(lents, person(l.who))
		lrels = append(lrels, rel(l.who, "on_leave", "Leave calendar"))
	}
	add(scenarioDoc{id: "leave-calendar", title: "Leave calendar", content: lb.String(), entities: lents, relations: lrels})
	add(scenarioDoc{id: "handbook-oncall", title: "On-call handbook",
		content: "On-call engineers acknowledge a page within 10 minutes. Swaps are agreed between the two engineers and announced in #oncall; the rota document is not edited after Friday."})
	add(scenarioDoc{id: "runbook-delta-migrations", title: "Delta schema migration runbook",
		content:  "Schema migrations in the Delta schema registry are announced two days ahead in #data-platform. Consumers pin a schema version; after a breaking migration every consumer must redeploy against the new version.",
		entities: []ToolEntityInput{product("Delta")}})

	// Events.
	atlasLead, atlasNewOwner := teams["Atlas"][0], teams["Atlas"][3]
	deltaEng, beaconFixer, atlasEng, atlasFixer := teams["Delta"][2], teams["Beacon"][4], teams["Atlas"][2], teams["Atlas"][5]
	mover := teams["Ember"][4]
	mem(scenarioMemory{id: "e-atlas-owner",
		content:   fmt.Sprintf("2026-11-05: %s handed ownership of Atlas to %s, who is now the Atlas owner and tech lead; %s moved to the CTO office.", atlasLead, atlasNewOwner, atlasLead),
		entities:  []ToolEntityInput{person(atlasLead), person(atlasNewOwner), product("Atlas")},
		relations: []ToolRelationInput{rel(atlasNewOwner, "owns", "Atlas")}})
	mem(scenarioMemory{id: "e-beacon-swap",
		content:   fmt.Sprintf("2026-11-07: on-call swap for week 46. %s is on leave 10-14 November, so %s takes the Beacon primary shift for the whole week.", beaconPrimary46, beaconCover46),
		entities:  []ToolEntityInput{person(beaconPrimary46), person(beaconCover46), product("Beacon")},
		relations: []ToolRelationInput{rel(beaconCover46, "on_call_for", "Beacon")}})
	mem(scenarioMemory{id: "e-cobalt-swap",
		content:   fmt.Sprintf("2026-11-20: on-call swap for week 48. %s is speaking at a conference, so %s takes the Cobalt primary shift.", cobaltPrimary48, cobaltCover48),
		entities:  []ToolEntityInput{person(cobaltPrimary48), person(cobaltCover48), product("Cobalt")},
		relations: []ToolRelationInput{rel(cobaltCover48, "on_call_for", "Cobalt")}})
	mem(scenarioMemory{id: "e-chg-1042",
		content:  fmt.Sprintf("2026-11-12: change CHG-1042, the Delta schema registry migration to v3, run by %s. The user_event payload renamed device_token to push_token. Downstream consumers Atlas, Beacon and Cobalt had to redeploy.", deltaEng),
		entities: []ToolEntityInput{{Name: "CHG-1042", Type: "Change"}, person(deltaEng), product("Delta"), product("Atlas"), product("Beacon"), product("Cobalt")},
		relations: []ToolRelationInput{rel("CHG-1042", "changes", "Delta"), rel(deltaEng, "executed", "CHG-1042"),
			rel("CHG-1042", "affects", "Atlas"), rel("CHG-1042", "affects", "Beacon"), rel("CHG-1042", "affects", "Cobalt")}})
	mem(scenarioMemory{id: "e-inc-77",
		content:   fmt.Sprintf("2026-11-13: incident INC-77, Beacon push delivery delayed by about 40 minutes. Root cause: CHG-1042 renamed the device token field and Beacon was still reading the old name. %s shipped the fix at 15:20.", beaconFixer),
		entities:  []ToolEntityInput{{Name: "INC-77", Type: "Incident"}, {Name: "CHG-1042", Type: "Change"}, product("Beacon"), person(beaconFixer)},
		relations: []ToolRelationInput{rel("INC-77", "affects", "Beacon"), rel("INC-77", "caused_by", "CHG-1042"), rel(beaconFixer, "fixed", "INC-77")}})
	mem(scenarioMemory{id: "e-chg-1050",
		content:   fmt.Sprintf("2026-11-17: change CHG-1050, new EU VAT rules in Atlas invoicing, deployed by %s.", atlasEng),
		entities:  []ToolEntityInput{{Name: "CHG-1050", Type: "Change"}, product("Atlas"), person(atlasEng)},
		relations: []ToolRelationInput{rel("CHG-1050", "changes", "Atlas"), rel(atlasEng, "executed", "CHG-1050")}})
	mem(scenarioMemory{id: "e-inc-81",
		content:   fmt.Sprintf("2026-11-18: incident INC-81, some customers received duplicate invoices for two hours. Caused by CHG-1050; %s rolled it back and re-sent corrected invoices.", atlasFixer),
		entities:  []ToolEntityInput{{Name: "INC-81", Type: "Incident"}, {Name: "CHG-1050", Type: "Change"}, product("Atlas"), person(atlasFixer)},
		relations: []ToolRelationInput{rel("INC-81", "affects", "Atlas"), rel("INC-81", "caused_by", "CHG-1050"), rel(atlasFixer, "fixed", "INC-81")}})
	mem(scenarioMemory{id: "e-team-move",
		content:   fmt.Sprintf("2026-11-16: %s moved from the Ember team to the Delta team to work on the event pipeline.", mover),
		entities:  []ToolEntityInput{person(mover), {Name: "Ember team", Type: "Team"}, {Name: "Delta team", Type: "Team"}},
		relations: []ToolRelationInput{rel(mover, "member_of", "Delta team")}})
	mem(scenarioMemory{id: "e-pm-change",
		content:   fmt.Sprintf("2026-11-19: while %s is on parental leave, %s is the product manager for Ember as well.", pmMobile, pmPlatform),
		entities:  []ToolEntityInput{person(pmMobile), person(pmPlatform), product("Ember")},
		relations: []ToolRelationInput{rel(pmPlatform, "product_manager_of", "Ember")}})
	mem(scenarioMemory{id: "e-leave-cancel",
		content:  fmt.Sprintf("2026-11-15: %s cancelled the 24-28 November leave and is working that week.", canceller),
		entities: []ToolEntityInput{person(canceller)}})
	mem(scenarioMemory{id: "e-ember-release",
		content:   fmt.Sprintf("2026-11-19: Ember 5.2 was released to the App Store and Google Play; %s signed off.", pmMobile),
		entities:  []ToolEntityInput{product("Ember"), person(pmMobile), {Name: "Ember 5.2", Type: "Release"}},
		relations: []ToolRelationInput{rel(pmMobile, "signed_off", "Ember 5.2")}})
	mem(scenarioMemory{id: "e-cobalt-reindex",
		content:  fmt.Sprintf("2026-11-18: the Cobalt search index rebuild is scheduled for 20 November, run by %s.", teams["Cobalt"][1]),
		entities: []ToolEntityInput{product("Cobalt"), person(teams["Cobalt"][1])}})
	for i, p := range scenarioProducts {
		who := teams[p.name][1+i%5]
		mem(scenarioMemory{id: "e-deploy-" + strings.ToLower(p.name),
			content:  fmt.Sprintf("2026-11-%02d: %s release %d.%d deployed to production by %s; no customer impact.", 3+i, p.name, 3+i, 10+i, who),
			entities: []ToolEntityInput{product(p.name), person(who)}})
	}
	mem(scenarioMemory{id: "e-sre-back", content: fmt.Sprintf("2026-11-10: %s is back from leave and joins the SRE on-call again.", sre2), entities: []ToolEntityInput{person(sre2)}})
	mem(scenarioMemory{id: "e-offsite", content: "2026-11-21: the engineering offsite moved to Lisbon, 4-5 December; the agenda is in the handbook."})
	mem(scenarioMemory{id: "e-laptops", content: "2026-11-11: laptop refresh — new machines arrive in the first week of December; hand old ones to IT."})
	mem(scenarioMemory{id: "e-hiring", content: fmt.Sprintf("2026-11-14: two open roles on the Delta team; %s runs the interviews.", teams["Delta"][0]), entities: []ToolEntityInput{person(teams["Delta"][0])}})

	any := func(ids ...string) string { return strings.Join(ids, "|") }
	cover := beaconCover46
	c.questions = []scenarioQuestion{
		{id: "q-search-owner", kind: "one-hop", text: "Who owns the search product?", gold: []string{"kn:product-cobalt"}},
		{id: "q-beacon-deps", kind: "one-hop", text: "What does the notification service depend on?", gold: []string{"kn:product-beacon"}},
		{id: "q-fix-77", kind: "one-hop", text: "Who fixed INC-77?", gold: []string{"mem:e-inc-77"}},
		{id: "q-city", kind: "one-hop", text: fmt.Sprintf("Where is %s based?", deltaEng), gold: []string{"kn:profile-" + slug(deltaEng)}},
		{id: "q-ember-push-oncall", kind: "two-hop", text: "Who is on call in week 46 for the service that sends Ember's push notifications?", gold: []string{"mem:e-beacon-swap"}},
		{id: "q-mobile-billing", kind: "two-hop", text: "Who should I ask about a billing problem with in-app purchases in the mobile app?", gold: []string{"mem:e-atlas-owner"}},
		{id: "q-late-push", kind: "two-hop", text: "Why were Ember push notifications late on 13 November?", gold: []string{"mem:e-inc-77", "mem:e-chg-1042"}},
		{id: "q-chg-team", kind: "two-hop", text: "Which team is the engineer who ran CHG-1042 on?", gold: []string{"mem:e-chg-1042", any("kn:team-delta", "kn:profile-"+slug(deltaEng))}},
		{id: "q-billing-secondary", kind: "two-hop", text: "Who is the secondary on-call in week 47 for the service Ember uses for subscription status?", gold: []string{"kn:oncall-w47"}},
		{id: "q-invoice-fixer-city", kind: "two-hop", text: "Where is the engineer who fixed the duplicate invoices based?", gold: []string{"mem:e-inc-81", "kn:profile-" + slug(atlasFixer)}},
		{id: "q-cover-manager", kind: "two-hop", text: "Who does the engineer covering Beacon in week 46 report to?", gold: []string{"mem:e-beacon-swap", "kn:profile-" + slug(cover)}},
		{id: "q-atlas-owner", kind: "change", text: "Who is the Atlas owner now?", gold: []string{"mem:e-atlas-owner"}},
		{id: "q-migration-impact", kind: "change", text: "Which products had to redeploy after the Delta schema migration?", gold: []string{"mem:e-chg-1042"}},
		{id: "q-mover-team", kind: "change", text: fmt.Sprintf("Which team is %s on now?", mover), gold: []string{"mem:e-team-move"}},
		{id: "q-ember-pm", kind: "change", text: "Who is the product manager for Ember now?", gold: []string{"mem:e-pm-change"}},
		{id: "q-leave-cancel", kind: "change", text: fmt.Sprintf("Is %s on leave 24-28 November?", canceller), gold: []string{"mem:e-leave-cancel"}},
		{id: "q-cobalt-w48", kind: "change", text: "Who covers Cobalt on call in week 48?", gold: []string{"mem:e-cobalt-swap"}},
	}
	return c
}

func TestCompanyScenario(t *testing.T) {
	url := os.Getenv("CORTEXDB_SCENARIO_EMBED_URL")
	if url == "" {
		t.Skip("set CORTEXDB_SCENARIO_EMBED_URL (an OpenAI-compatible embeddings endpoint) to run the company scenario")
	}
	ctx := context.Background()
	embedder := newPPRBenchEmbedder(t, ctx, url, firstNonEmpty(os.Getenv("CORTEXDB_SCENARIO_EMBED_MODEL"), "embeddinggemma"))
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "company.db"))
	cfg.Dimensions = embedder.Dim()
	db, err := Open(cfg, WithEmbedder(embedder))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	company := buildScenarioCompany()
	for _, d := range company.docs {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: d.id, Title: d.title, Content: d.content, Entities: withEndpoints(d.entities, d.relations), Relations: d.relations}); err != nil {
			t.Fatalf("save %s: %v", d.id, err)
		}
	}
	for _, m := range company.memories {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: m.id, Scope: MemoryScopeGlobal, Content: m.content, Entities: withEndpoints(m.entities, m.relations), Relations: m.relations}); err != nil {
			t.Fatalf("save %s: %v", m.id, err)
		}
	}

	type tally struct{ found, total int }
	before, after := map[string]*tally{}, map[string]*tally{}
	for _, q := range company.questions {
		// Before: what auto returned until #272, the vector + keyword
		// ranking with no walk.
		vecMem, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q.text, Scope: MemoryScopeGlobal, TopK: 5, DisableGraph: true})
		if err != nil {
			t.Fatal(err)
		}
		opts := GraphRAGQueryOptions{TopK: 5, MaxContextChars: 1 << 30, RetrievalMode: RetrievalModeLexical}
		applyGraphRAGQueryDefaults(&opts)
		vecKn, err := db.searchKnowledgeHybrid(ctx, q.text, opts, ToolSearchGraphRAGLexicalRequest{Query: q.text, TopK: 5, MaxContextChars: 1 << 30, RetrievalMode: RetrievalModeLexical})
		if err != nil {
			t.Fatal(err)
		}
		// After: the recall agents call.
		rec, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: q.text, Scope: MemoryScopeGlobal, TopKMemories: 5, TopKKnowledge: 5})
		if err != nil {
			t.Fatal(err)
		}

		var vecIDs, recIDs []string
		for _, h := range vecMem.Results {
			vecIDs = append(vecIDs, "mem:"+h.Memory.ID)
		}
		seen := map[string]bool{}
		for _, c := range vecKn.Chunks {
			if !seen[c.DocumentID] {
				seen[c.DocumentID] = true
				vecIDs = append(vecIDs, "kn:"+c.DocumentID)
			}
		}
		for _, h := range rec.Memories {
			recIDs = append(recIDs, "mem:"+h.Memory.ID)
		}
		for _, k := range rec.Knowledge {
			recIDs = append(recIDs, "kn:"+k.KnowledgeID)
		}
		has := func(ids []string, gold string) bool {
			for _, x := range ids {
				for _, g := range strings.Split(gold, "|") {
					if x == g {
						return true
					}
				}
			}
			return false
		}
		mark := func(ids []string) string {
			out := make([]string, len(ids))
			for i, id := range ids {
				out[i] = id
				for _, g := range q.gold {
					for _, alt := range strings.Split(g, "|") {
						if alt == id {
							out[i] = "*" + id
						}
					}
				}
			}
			return strings.Join(out, " ")
		}
		for _, g := range q.gold {
			for kind, set := range map[*map[string]*tally][]string{&before: vecIDs, &after: recIDs} {
				m := *kind
				if m[q.kind] == nil {
					m[q.kind] = &tally{}
				}
				m[q.kind].total++
				if has(set, g) {
					m[q.kind].found++
				}
			}
		}

		t.Logf("\n[%s] %s  (gold: %s)", q.kind, q.text, strings.Join(q.gold, ", "))
		t.Logf("  vector stage   : %s", mark(vecIDs))
		t.Logf("  memory decision: %s", rec.MemoryDecision.Reason)
		t.Logf("  know.  decision: %s", rec.KnowledgeDecision.Reason)
		t.Logf("  walk entities  : %v", firstN(rec.GraphEntities, 10))
		t.Logf("  fused          : %s", mark(recIDs))
		for i, f := range rec.GraphFacts {
			if i == 6 {
				break
			}
			t.Logf("  graph fact     : %s —%s→ %s", f.Subject, f.Predicate, f.Object)
		}
	}
	kinds := []string{"one-hop", "two-hop", "change"}
	t.Logf("\n%-8s %-14s %-14s", "kind", "vector stage", "vector+graph")
	var bf, bt, af, at int
	for _, k := range kinds {
		b, a := before[k], after[k]
		if b == nil {
			continue
		}
		t.Logf("%-8s %2d/%-11d %2d/%-11d", k, b.found, b.total, a.found, a.total)
		bf, bt, af, at = bf+b.found, bt+b.total, af+a.found, at+a.total
	}
	t.Logf("%-8s %2d/%-11d %2d/%-11d", "all", bf, bt, af, at)
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// withEndpoints declares every relation endpoint the entities leave out, as
// an extractor naming both ends of a relation does.
func withEndpoints(entities []ToolEntityInput, relations []ToolRelationInput) []ToolEntityInput {
	out := append([]ToolEntityInput(nil), entities...)
	have := map[string]bool{}
	for _, e := range out {
		have[e.Name] = true
	}
	for _, r := range relations {
		for _, n := range []string{r.From, r.To} {
			if !have[n] {
				have[n] = true
				out = append(out, ToolEntityInput{Name: n})
			}
		}
	}
	return out
}
