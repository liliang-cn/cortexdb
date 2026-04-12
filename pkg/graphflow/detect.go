package graphflow

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// FilesystemDetector is a deterministic detector for local text/code corpora.
type FilesystemDetector struct {
	IncludeExtensions []string
	ExcludeDirs       []string
	MaxFileBytes      int64
	ReadContent       bool
}

// Detect enumerates source documents under a root directory.
func (d FilesystemDetector) Detect(ctx context.Context, root string) ([]SourceDocument, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("root is required")
	}
	if d.MaxFileBytes <= 0 {
		d.MaxFileBytes = 512 * 1024
	}
	if len(d.IncludeExtensions) == 0 {
		d.IncludeExtensions = defaultGraphflowExtensions()
	}
	if len(d.ExcludeDirs) == 0 {
		d.ExcludeDirs = []string{".git", "node_modules", "dist", "build", "vendor", ".venv", "graphify-out"}
	}
	if !d.ReadContent {
		d.ReadContent = true
	}

	allowed := make(map[string]struct{}, len(d.IncludeExtensions))
	for _, ext := range d.IncludeExtensions {
		if ext = strings.ToLower(strings.TrimSpace(ext)); ext != "" {
			allowed[ext] = struct{}{}
		}
	}
	excluded := make(map[string]struct{}, len(d.ExcludeDirs))
	for _, dir := range d.ExcludeDirs {
		if dir = strings.TrimSpace(dir); dir != "" {
			excluded[dir] = struct{}{}
		}
	}

	docs := make([]SourceDocument, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if entry.IsDir() {
			if _, skip := excluded[entry.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := allowed[ext]; !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > d.MaxFileBytes {
			return nil
		}
		doc := SourceDocument{
			ID:       stableSourceID(path),
			Path:     path,
			Type:     detectSourceType(ext),
			Title:    strings.TrimSuffix(entry.Name(), ext),
			Metadata: map[string]string{"extension": ext},
		}
		if d.ReadContent {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytesLookBinary(data) || !utf8.Valid(data) {
				return nil
			}
			doc.Content = string(data)
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

func defaultGraphflowExtensions() []string {
	return []string{
		".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".rs", ".rb", ".php",
		".md", ".txt", ".rst", ".json", ".yaml", ".yml", ".toml", ".sql", ".sh",
	}
}

func detectSourceType(ext string) string {
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".rs", ".rb", ".php":
		return "code"
	case ".md", ".rst":
		return "markdown"
	case ".json", ".yaml", ".yml", ".toml":
		return "config"
	default:
		return "text"
	}
}

func bytesLookBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func stableSourceID(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.ReplaceAll(path, " ", "_")
	path = strings.ReplaceAll(path, ":", "_")
	return path
}
