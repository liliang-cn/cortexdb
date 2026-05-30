// pkg/importflow/types_test.go
package importflow

import "testing"

func TestRecordGet(t *testing.T) {
	r := Record{
		Table:  "t",
		Values: map[string]string{"a": "1", "b": ""},
		Nulls:  map[string]bool{"b": true},
	}
	if v, ok := r.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = %q,%v; want 1,true", v, ok)
	}
	if _, ok := r.Get("b"); ok {
		t.Fatalf("Get(b) ok = true; want false (NULL)")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatalf("Get(missing) ok = true; want false")
	}
}

func TestReportAddError(t *testing.T) {
	var rep Report
	rep.addError(nil)
	rep.addError(errFor("boom"))
	if len(rep.Errors) != 1 {
		t.Fatalf("len(Errors) = %d; want 1", len(rep.Errors))
	}
}

func errFor(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
