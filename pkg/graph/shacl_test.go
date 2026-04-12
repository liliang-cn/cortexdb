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
