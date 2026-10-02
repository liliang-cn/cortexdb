package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/liliang-cn/cortexdb/v2/pkg/eval"
)

// fetchBench downloads every file of a benchmark that is missing or does not
// match the manifest, from the manifest's pinned URL, and verifies it before
// moving it into place. A download that does not hash correctly is deleted and
// reported: keeping it would let the next run load a file the baselines were
// not measured on.
func fetchBench(ctx context.Context, dataDir, name string) error {
	m, err := eval.BenchManifest()
	if err != nil {
		return err
	}
	ds, ok := m[name]
	if !ok {
		return fmt.Errorf("unknown benchmark %q (have %v)", name, eval.BenchNames())
	}
	for _, f := range ds.Files {
		if eval.VerifyBenchFile(dataDir, f) == nil {
			continue
		}
		dst := filepath.Join(dataDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		logf("fetching %s (%d bytes) from %s", f.Path, f.Size, f.URL)
		tmp := dst + ".part"
		if err := download(ctx, f.URL, tmp); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("fetch %s: %w", f.Path, err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
		if err := eval.VerifyBenchFile(dataDir, f); err != nil {
			os.Remove(dst)
			return err
		}
	}
	logf("%s: license %s; source %s", name, ds.License, ds.Source)
	return nil
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
