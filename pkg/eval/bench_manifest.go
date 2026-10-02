package eval

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// BenchFile is one file a benchmark is read from: where it lives under the
// data directory, the official URL it came from, pinned to a revision, and the
// digest it must have.
type BenchFile struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// BenchDataset describes where a benchmark comes from and under what terms.
type BenchDataset struct {
	Source  string      `json:"source"`
	License string      `json:"license"`
	Files   []BenchFile `json:"files"`
}

//go:embed bench_manifest.json
var embeddedBenchManifest []byte

// BenchManifest returns the pinned file list of every standard benchmark the
// harness can load. Dataset content is never in the repository — licenses
// forbid redistributing some of it and LongMemEval alone is 277 MB — so the
// manifest is what makes "the same dataset" checkable: a file that does not
// hash to its entry is not the file the recorded baselines were measured on.
func BenchManifest() (map[string]BenchDataset, error) {
	var m map[string]BenchDataset
	if err := json.Unmarshal(embeddedBenchManifest, &m); err != nil {
		return nil, fmt.Errorf("eval: bench manifest: %w", err)
	}
	return m, nil
}

// BenchNames lists the benchmarks in the manifest, sorted.
func BenchNames() []string {
	m, err := BenchManifest()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// VerifyBenchFile checks that the file under dataDir has the manifest's size
// and SHA-256.
func VerifyBenchFile(dataDir string, f BenchFile) error {
	path := filepath.Join(dataDir, filepath.FromSlash(f.Path))
	fh, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("eval: %s: %w (fetch it from %s)", f.Path, err, f.URL)
	}
	defer fh.Close()
	h := sha256.New()
	n, err := io.Copy(h, fh)
	if err != nil {
		return fmt.Errorf("eval: %s: %w", f.Path, err)
	}
	if f.Size > 0 && n != f.Size {
		return fmt.Errorf("eval: %s: size %d, manifest says %d", f.Path, n, f.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("eval: %s: sha256 %s, manifest says %s", f.Path, got, f.SHA256)
	}
	return nil
}

// LoadBench verifies a benchmark's files under dataDir against the manifest
// and loads it. It refuses a file whose digest differs rather than scoring it:
// a number measured on a different file is not comparable to the baselines,
// and would not say so.
func LoadBench(dataDir, name string) (*Suite, error) {
	m, err := BenchManifest()
	if err != nil {
		return nil, err
	}
	ds, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("eval: unknown benchmark %q (have %v)", name, BenchNames())
	}
	for _, f := range ds.Files {
		if err := VerifyBenchFile(dataDir, f); err != nil {
			return nil, err
		}
	}
	open := func(i int) (*os.File, error) {
		return os.Open(filepath.Join(dataDir, filepath.FromSlash(ds.Files[i].Path)))
	}
	switch name {
	case "longmemeval":
		f, err := open(0)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return LoadLongMemEval(f)
	case "locomo":
		f, err := open(0)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return LoadLoCoMo(f)
	case "musique", "2wikimultihopqa":
		q, err := open(0)
		if err != nil {
			return nil, err
		}
		defer q.Close()
		c, err := open(1)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		if name == "musique" {
			return LoadMuSiQue(q, c)
		}
		return Load2WikiMultiHopQA(q, c)
	}
	return nil, fmt.Errorf("eval: benchmark %q is in the manifest but has no loader", name)
}
