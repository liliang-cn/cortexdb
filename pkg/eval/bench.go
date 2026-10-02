package eval

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
)

// Corpus is a set of documents indexed together and the queries asked of
// exactly that set. A benchmark whose questions share one corpus (MuSiQue,
// 2WikiMultiHopQA) has one; a benchmark whose every question brings its own
// haystack (LongMemEval) has one per question, and the harness builds a fresh
// index for each so no question can be answered from another's haystack.
type Corpus struct {
	ID        string     `json:"id"`
	Documents []Document `json:"documents"`
	Queries   []Query    `json:"queries"`
}

// Suite is a standard benchmark loaded into the harness's shape.
type Suite struct {
	Name string `json:"name"`
	// Protocol states, in one sentence, what is indexed, what is asked and what
	// counts as relevant, so a number can be read without the loader's source.
	Protocol string   `json:"protocol"`
	Corpora  []Corpus `json:"corpora"`
	// Skipped counts questions the protocol leaves out, by reason. A benchmark
	// number that silently dropped questions cannot be compared with one that
	// did not.
	Skipped map[string]int `json:"skipped,omitempty"`
}

// NumQueries is the number of questions across every corpus.
func (s *Suite) NumQueries() int {
	n := 0
	for _, c := range s.Corpora {
		n += len(c.Queries)
	}
	return n
}

// NumDocuments is the number of documents the harness will index, counting a
// document once per corpus it appears in.
func (s *Suite) NumDocuments() int {
	n := 0
	for _, c := range s.Corpora {
		n += len(c.Documents)
	}
	return n
}

// skip records one question left out for the given reason.
func (s *Suite) skip(reason string) {
	if s.Skipped == nil {
		s.Skipped = map[string]int{}
	}
	s.Skipped[reason]++
}

// Sample keeps at most limit questions, chosen by a seeded hash of each
// question's id, and drops corpora left with no question. A limit of zero or
// less keeps everything.
//
// Hashing the id instead of shuffling the list makes the choice independent of
// file order and of how many questions the file holds: the same seed picks the
// same questions after a dataset is re-serialised, and raising the limit only
// adds questions, so a run at 200 contains the run at 100.
func (s *Suite) Sample(limit int, seed uint64) {
	if limit <= 0 || limit >= s.NumQueries() {
		return
	}
	type ref struct {
		key  uint64
		id   string
		c, q int
	}
	var refs []ref
	for ci, c := range s.Corpora {
		for qi, q := range c.Queries {
			refs = append(refs, ref{key: sampleKey(seed, c.ID+"\x00"+q.ID), id: c.ID + "\x00" + q.ID, c: ci, q: qi})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].key != refs[j].key {
			return refs[i].key < refs[j].key
		}
		return refs[i].id < refs[j].id
	})
	keep := make(map[[2]int]struct{}, limit)
	for _, r := range refs[:limit] {
		keep[[2]int{r.c, r.q}] = struct{}{}
	}
	corpora := s.Corpora[:0]
	for ci, c := range s.Corpora {
		queries := c.Queries[:0]
		for qi, q := range c.Queries {
			if _, ok := keep[[2]int{ci, qi}]; ok {
				queries = append(queries, q)
			}
		}
		if len(queries) == 0 {
			continue
		}
		c.Queries = queries
		corpora = append(corpora, c)
	}
	s.Corpora = corpora
}

// sampleKey is a stable 64-bit hash of seed and id.
func sampleKey(seed uint64, id string) uint64 {
	sum := sha256.Sum256([]byte(strconv.FormatUint(seed, 10) + "\x00" + id))
	return binary.BigEndian.Uint64(sum[:8])
}

// passageID names a passage by its content, so the same passage gets the same
// id on every run and in every corpus file that repeats it.
func passageID(title, text string) string {
	sum := sha256.Sum256([]byte(title + "\n" + text))
	return fmt.Sprintf("p%x", sum[:8])
}
