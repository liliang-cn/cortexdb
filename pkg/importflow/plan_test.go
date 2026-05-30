package importflow

import "testing"

func TestRenderTemplate(t *testing.T) {
	r := Record{Values: map[string]string{"title": "Go", "body": "fast"}}
	got := renderTemplate("{title}\n\n{body}", r)
	if got != "Go\n\nfast" {
		t.Fatalf("render = %q; want %q", got, "Go\n\nfast")
	}
	// missing/NULL columns render as empty string
	got2 := renderTemplate("{title}-{missing}", r)
	if got2 != "Go-" {
		t.Fatalf("render = %q; want %q", got2, "Go-")
	}
}
