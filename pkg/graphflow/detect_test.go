package graphflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemDetector(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte("Apollo plan"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	detector := FilesystemDetector{}
	docs, err := detector.Detect(context.Background(), root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 detected doc, got %+v", docs)
	}
	if docs[0].Type != "markdown" {
		t.Fatalf("unexpected detected type: %+v", docs[0])
	}
}
