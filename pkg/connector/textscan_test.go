package connector

import (
	"strings"
	"testing"
)

func TestTextScannerRedactsInPlace(t *testing.T) {
	s := NewTextScanner()
	in := "Call me at 13812341234 or alice@example.com; id 110101199003078888."
	out, hits := s.Scan(in)
	if strings.Contains(out, "13812341234") || strings.Contains(out, "alice@example.com") || strings.Contains(out, "110101199003078888") {
		t.Fatalf("PII leaked: %q", out)
	}
	if hits < 3 {
		t.Fatalf("expected >=3 hits, got %d", hits)
	}
	if !strings.Contains(out, "[REDACTED:phone]") {
		t.Fatalf("missing typed marker: %q", out)
	}
}

func TestTextScannerCleanTextUnchanged(t *testing.T) {
	s := NewTextScanner()
	in := "The customer was happy with the service."
	out, hits := s.Scan(in)
	if out != in || hits != 0 {
		t.Fatalf("clean text changed: %q hits=%d", out, hits)
	}
}
