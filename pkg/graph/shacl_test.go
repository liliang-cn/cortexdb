package graph

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func TestSHACLValidation(t *testing.T) {
	dbPath := fmt.Sprintf("test_shacl_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 16)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	g := NewGraphStore(store)

	// Add test data: Alice is 25 (valid), Bob is 200 (invalid), Charlie has no age (invalid if minCount=1)
	triples := []*RDFTriple{
		{Subject: NewIRI("alice"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("age"), Object: NewTypedLiteral("25", XSDNamespace+"integer")},

		{Subject: NewIRI("bob"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("age"), Object: NewTypedLiteral("200", XSDNamespace+"integer")},

		{Subject: NewIRI("charlie"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},

		{Subject: NewIRI("dan"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},
		{Subject: NewIRI("dan"), Predicate: NewIRI("age"), Object: NewLiteral("not-a-number")},
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	// Define SHACL shape
	shapeTriples := []RDFTriple{
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(RDFType), Object: NewIRI(SHACLNodeShape)},
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(SHACLTargetClass), Object: NewIRI("Person")},
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(SHACLProperty), Object: NewIRI("AgePropertyShape")},

		{Subject: NewIRI("AgePropertyShape"), Predicate: NewIRI(SHACLPath), Object: NewIRI("age")},
		{Subject: NewIRI("AgePropertyShape"), Predicate: NewIRI(SHACLDatatype), Object: NewIRI(XSDNamespace + "integer")},
		{Subject: NewIRI("AgePropertyShape"), Predicate: NewIRI(SHACLMinCount), Object: NewLiteral("1")},
		{Subject: NewIRI("AgePropertyShape"), Predicate: NewIRI(SHACLMinInclusive), Object: NewLiteral("0")},
		{Subject: NewIRI("AgePropertyShape"), Predicate: NewIRI(SHACLMaxInclusive), Object: NewLiteral("150")},
	}

	report, err := g.ValidateSHACL(ctx, shapeTriples)
	if err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	if report.Conforms {
		t.Error("Expected validation failure, but it conformed")
	}

	// Expected errors:
	// 1. Bob's age 200 > 150
	// 2. Charlie has no age (minCount 1)
	// 3. Dan's age is not a number
	// 4. Dan's age datatype is missing

	if len(report.Results) < 3 {
		t.Errorf("Expected at least 3 violations, got %d", len(report.Results))
	}

	foundBobViolation := false
	foundCharlieViolation := false
	foundDanViolation := false
	for _, res := range report.Results {
		if res.FocusNode.Value == "bob" && res.Path.Value == "age" {
			foundBobViolation = true
		}
		if res.FocusNode.Value == "charlie" && res.Path.Value == "age" {
			foundCharlieViolation = true
		}
		if res.FocusNode.Value == "dan" && res.Path.Value == "age" {
			foundDanViolation = true
		}
	}

	if !foundBobViolation {
		t.Error("Missing violation for Bob's age")
	}
	if !foundCharlieViolation {
		t.Error("Missing violation for Charlie's missing age")
	}
	if !foundDanViolation {
		t.Error("Missing violation for Dan's invalid age")
	}
}

func TestSHACLAdvancedPropertyConstraints(t *testing.T) {
	dbPath := fmt.Sprintf("test_shacl_advanced_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 16)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	g := NewGraphStore(store)

	triples := []*RDFTriple{
		{Subject: NewIRI("boss1"), Predicate: NewIRI(RDFType), Object: NewIRI("Employee")},

		{Subject: NewIRI("alice"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("manager"), Object: NewIRI("boss1")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("homepage"), Object: NewIRI("https://example.com/alice")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("status"), Object: NewLiteral("active")},

		{Subject: NewIRI("bob"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("manager"), Object: NewIRI("outsider")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("homepage"), Object: NewIRI("https://example.com/bob")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("status"), Object: NewLiteral("blocked")},

		{Subject: NewIRI("carol"), Predicate: NewIRI(RDFType), Object: NewIRI("Person")},
		{Subject: NewIRI("carol"), Predicate: NewIRI("manager"), Object: NewIRI("boss1")},
		{Subject: NewIRI("carol"), Predicate: NewIRI("homepage"), Object: NewLiteral("https://example.com/carol")},
		{Subject: NewIRI("carol"), Predicate: NewIRI("status"), Object: NewLiteral("pending")},
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	shapeTriples := []RDFTriple{
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(RDFType), Object: NewIRI(SHACLNodeShape)},
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(SHACLTargetClass), Object: NewIRI("Person")},
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(SHACLProperty), Object: NewIRI("ManagerShape")},
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(SHACLProperty), Object: NewIRI("HomepageShape")},
		{Subject: NewIRI("PersonShape"), Predicate: NewIRI(SHACLProperty), Object: NewIRI("StatusShape")},

		{Subject: NewIRI("ManagerShape"), Predicate: NewIRI(SHACLPath), Object: NewIRI("manager")},
		{Subject: NewIRI("ManagerShape"), Predicate: NewIRI(SHACLClass), Object: NewIRI("Employee")},

		{Subject: NewIRI("HomepageShape"), Predicate: NewIRI(SHACLPath), Object: NewIRI("homepage")},
		{Subject: NewIRI("HomepageShape"), Predicate: NewIRI(SHACLNodeKind), Object: NewIRI(SHACLIRI)},

		{Subject: NewIRI("StatusShape"), Predicate: NewIRI(SHACLPath), Object: NewIRI("status")},
		{Subject: NewIRI("StatusShape"), Predicate: NewIRI(SHACLIn), Object: NewBlankNode("status-list-1")},

		{Subject: NewBlankNode("status-list-1"), Predicate: NewIRI(rdfFirstIRI), Object: NewLiteral("active")},
		{Subject: NewBlankNode("status-list-1"), Predicate: NewIRI(rdfRestIRI), Object: NewBlankNode("status-list-2")},
		{Subject: NewBlankNode("status-list-2"), Predicate: NewIRI(rdfFirstIRI), Object: NewLiteral("pending")},
		{Subject: NewBlankNode("status-list-2"), Predicate: NewIRI(rdfRestIRI), Object: NewIRI(rdfNilIRI)},
	}

	report, err := g.ValidateSHACL(ctx, shapeTriples)
	if err != nil {
		t.Fatalf("validate advanced shacl: %v", err)
	}
	if report.Conforms {
		t.Fatal("expected validation failure for advanced constraints")
	}
	if len(report.Results) != 3 {
		t.Fatalf("expected exactly 3 violations, got %+v", report.Results)
	}

	foundBobClass := false
	foundBobStatus := false
	foundCarolNodeKind := false
	for _, res := range report.Results {
		switch {
		case res.FocusNode.Value == "bob" && res.Path.Value == "manager":
			foundBobClass = true
		case res.FocusNode.Value == "bob" && res.Path.Value == "status":
			foundBobStatus = true
		case res.FocusNode.Value == "carol" && res.Path.Value == "homepage":
			foundCarolNodeKind = true
		}
	}

	if !foundBobClass {
		t.Error("missing sh:class violation for Bob manager")
	}
	if !foundBobStatus {
		t.Error("missing sh:in violation for Bob status")
	}
	if !foundCarolNodeKind {
		t.Error("missing sh:nodeKind violation for Carol homepage")
	}
}
