package authz

import (
	"errors"
	"strings"
	"testing"
)

func TestAnExtensionToolIsHeldToItsClassAndRefusedToConfinedKeys(t *testing.T) {
	writer := Key{ID: "writer", Clearance: ReadWrite}
	reader := Key{ID: "reader", Clearance: ReadOnly}
	confined := Key{ID: "tenant", Clearance: ReadWrite, Scope: Scope{Collection: "acme"}}

	write := ExtensionTool{Name: "ext_import", Access: Write, Refusal: "writes rows no scope field confines"}
	read := ExtensionTool{Name: "ext_plan", Access: Read}

	if err := writer.AuthorizeExtensionTool(write); err != nil {
		t.Fatalf("a read-write key was refused a writing extension: %v", err)
	}
	if err := reader.AuthorizeExtensionTool(read); err != nil {
		t.Fatalf("a read-only key was refused a reading extension: %v", err)
	}
	err := reader.AuthorizeExtensionTool(write)
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "ext_import writes") {
		t.Fatalf("a read-only key and a writing extension: %v", err)
	}

	err = confined.AuthorizeExtensionTool(write)
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "writes rows no scope field confines") {
		t.Fatalf("a confined key and an extension with a reason: %v", err)
	}
	err = confined.AuthorizeExtensionTool(read)
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), defaultRefusal) {
		t.Fatalf("a confined key and an extension with no reason: %v", err)
	}
}

func TestAnExtensionToolThatIsNotClassifiedIsRefusedToEveryKey(t *testing.T) {
	writer := Key{ID: "writer", Clearance: ReadWrite}
	for _, tool := range []ExtensionTool{
		{Name: "ext_unclassified"},
		{Access: Read},
		// A toolbox tool keeps the toolbox's line: an extension line cannot
		// reclassify ingest_document as a read.
		{Name: "ingest_document", Access: Read},
	} {
		if err := writer.AuthorizeExtensionTool(tool); !errors.Is(err, ErrDenied) {
			t.Errorf("%+v was allowed: %v", tool, err)
		}
	}
}
