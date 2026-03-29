package cortexdb

import "testing"

func TestStringChunksRespectsSQLiteVariableLimit(t *testing.T) {
	values := make([]string, 1200)
	for i := range values {
		values[i] = "value"
	}

	chunks := stringChunks(values, 2)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 499 || len(chunks[1]) != 499 || len(chunks[2]) != 202 {
		t.Fatalf("unexpected chunk sizes: %d, %d, %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}
