package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A trace file that is never cut grows until the disk is full: a busy brain
// writes a record per call, and an explain record per recall is a few
// kilobytes. RotatingFile cuts the file when it passes a size, keeps a fixed
// number of old files beside it (name.1 the newest), and drops old files
// past an age. Each Write is one record and is never split across files.

// RotatingFile is an append-only file that rotates by size and age.
type RotatingFile struct {
	path    string
	maxSize int64
	keep    int
	maxAge  time.Duration

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenRotatingFile opens path for appending. maxSize <= 0 never rotates;
// keep is how many rotated files to keep (0 keeps none: the file is
// truncated when it rotates); maxAge > 0 also removes rotated files older
// than that.
func OpenRotatingFile(path string, maxSize int64, keep int, maxAge time.Duration) (*RotatingFile, error) {
	r := &RotatingFile{path: path, maxSize: maxSize, keep: keep, maxAge: maxAge}
	if err := r.open(); err != nil {
		return nil, err
	}
	r.prune()
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

// Write appends p, rotating first when p would take the file past its size.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.maxSize > 0 && r.size > 0 && r.size+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts name.i to name.i+1, the live file to name.1, and opens a new
// live file. Called with mu held.
func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	if r.keep <= 0 {
		if err := os.Truncate(r.path, 0); err != nil && !os.IsNotExist(err) {
			return err
		}
		return r.open()
	}
	_ = os.Remove(r.rotated(r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		_ = os.Rename(r.rotated(i), r.rotated(i+1))
	}
	if err := os.Rename(r.path, r.rotated(1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := r.open(); err != nil {
		return err
	}
	r.prune()
	return nil
}

func (r *RotatingFile) rotated(i int) string { return r.path + "." + strconv.Itoa(i) }

// prune removes rotated files past keep or older than maxAge.
func (r *RotatingFile) prune() {
	matches, _ := filepath.Glob(r.path + ".*")
	now := time.Now()
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimPrefix(m, r.path+"."))
		if err != nil || n < 1 {
			continue
		}
		if n > r.keep {
			_ = os.Remove(m)
			continue
		}
		if r.maxAge > 0 {
			if st, err := os.Stat(m); err == nil && now.Sub(st.ModTime()) > r.maxAge {
				_ = os.Remove(m)
			}
		}
	}
}

// Close closes the live file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// String names the file, for a log line saying where records go.
func (r *RotatingFile) String() string {
	return fmt.Sprintf("%s (rotate at %d bytes, keep %d)", r.path, r.maxSize, r.keep)
}
