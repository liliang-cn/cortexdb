package index

import (
	"fmt"
	"sort"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/quantization"
)

// BinaryIndex is a binary-quantized index with full-precision rescoring.
//
// Every vector is kept as a 1-bit-per-dimension sign code (pkg/quantization,
// PackSigns), 1/32 of its float32 size. A search scans all codes by Hamming
// distance — a few popcounts per vector, so a full scan of 100k 768-d codes
// costs about what scoring a few thousand float vectors does — takes the
// k × Oversample nearest codes as a shortlist, and ranks the shortlist by
// exact cosine distance on the full vectors. The scan decides who is
// considered; the rescoring decides the order and which k survive. Without
// rescoring, Hamming order alone is a coarse proxy (hundreds of vectors share
// each distance value), and recall@10 on real embeddings sits far below what
// the shortlist actually contains.
//
// The full vectors need not live here. With KeepVectors (the default for
// direct use of this package) the index rescores them itself. A store that
// already has the vectors on disk — pkg/core keeps them in its embeddings
// table — builds the index without them, asks Candidates for the shortlist
// and rescores what it loads, so only the codes are resident.
type BinaryIndex struct {
	mu sync.RWMutex

	dim, words int
	// center is subtracted before taking signs; nil means plain sign codes.
	center   []float32
	centerOn bool
	// trainedAt is how many vectors the center was computed from.
	trainedAt int

	ids   []string
	pos   map[string]int
	codes []uint64 // words per vector, contiguous, in ids order
	// vectors holds the full-precision vectors when keepVectors is set.
	vectors     [][]float32
	keepVectors bool

	oversample int
	scratch    sync.Pool
}

// BinaryIndexOptions configures a BinaryIndex.
type BinaryIndexOptions struct {
	// Oversample is how many Hamming candidates are rescored per result
	// wanted: a search for k rescores k × Oversample. Zero means
	// DefaultBinaryOversample.
	Oversample int
	// Center subtracts the per-dimension mean (learned by Train) before taking
	// signs. It matters for embeddings that are not zero-centered, which is
	// most of them; see pkg/quantization.
	Center bool
	// KeepVectors stores the full vectors so Search can rescore by itself.
	// Off, the index holds only codes and Search ranks by Hamming distance;
	// rescoring is the caller's (see Candidates).
	KeepVectors bool
}

// DefaultBinaryOversample is the shortlist multiplier used when none is given:
// the smallest that kept recall@10 against exact search at or above 0.95 on
// every set of text embeddings measured (768-d, mean-centered) — 0.996 on
// the brain snapshot's dense vectors, 0.979 on 20k embeddinggemma vectors of
// Go doc comments, 0.999 on 100k synthetic vectors with the same spectrum.
// 4 falls just short on the last two (0.947, 0.948). The extra rescoring is
// 80 exact dot products per 10 results, small next to the Hamming scan.
// Clustered data with isotropic within-cluster noise — every cluster member
// nearly equidistant — needs 16; such sets should raise it.
const DefaultBinaryOversample = 8

// NewBinaryIndex creates an empty index for dim-dimensional vectors.
func NewBinaryIndex(dim int, opts BinaryIndexOptions) *BinaryIndex {
	over := opts.Oversample
	if over <= 0 {
		over = DefaultBinaryOversample
	}
	return &BinaryIndex{
		dim:         dim,
		words:       quantization.BinaryWords(dim),
		centerOn:    opts.Center,
		pos:         make(map[string]int),
		keepVectors: opts.KeepVectors,
		oversample:  over,
	}
}

// Train learns the center from a sample of vectors and re-encodes every code
// already stored (which needs KeepVectors; without it, use Rebuild). A no-op
// when centering is off.
func (b *BinaryIndex) Train(sample [][]float32) error {
	if !b.centerOn || len(sample) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ids) > 0 && !b.keepVectors {
		return fmt.Errorf("binary index: retraining %d stored codes needs their vectors; use Rebuild", len(b.ids))
	}
	b.center = quantization.MeanVector(sample, b.dim)
	b.trainedAt = len(sample)
	for i, v := range b.vectors {
		quantization.PackSigns(b.codes[i*b.words:(i+1)*b.words], v, b.center)
	}
	return nil
}

// Rebuild replaces the whole content with these vectors, retraining the
// center from them when centering is on. It is how a store that keeps no
// vectors here recenters once the data has drifted from what the center was
// learned on (see NeedsRetrain).
func (b *BinaryIndex) Rebuild(ids []string, vectors [][]float32) error {
	return b.RebuildFrom(func() ([]string, [][]float32, error) { return ids, vectors, nil })
}

// RebuildFrom is Rebuild with the vectors loaded while the index is locked.
// A store reloading from its own table needs that: an insert that lands
// between the load and the swap would otherwise be dropped by the swap, and
// with the lock held it waits and is applied after.
func (b *BinaryIndex) RebuildFrom(load func() ([]string, [][]float32, error)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids, vectors, err := load()
	if err != nil {
		return err
	}
	for i, v := range vectors {
		if len(v) != b.dim {
			return fmt.Errorf("binary index: %s has %d dimensions, index has %d", ids[i], len(v), b.dim)
		}
	}
	b.center = nil
	b.trainedAt = 0
	if b.centerOn && len(vectors) > 0 {
		b.center = quantization.MeanVector(vectors, b.dim)
		b.trainedAt = len(vectors)
	}
	b.ids = b.ids[:0]
	b.pos = make(map[string]int, len(ids))
	b.codes = b.codes[:0]
	b.vectors = b.vectors[:0]
	for i, id := range ids {
		b.insertLocked(id, vectors[i])
	}
	return nil
}

// RebuildStreaming is RebuildFrom without the vectors in memory: scan yields
// every vector once per pass, and only the codes are kept. With centering on
// it is called twice — once to learn the mean, once to encode against it —
// which costs a second read of the store and saves holding a float32 copy of
// all of it, the whole peak of an index that otherwise keeps a 32nd of that.
func (b *BinaryIndex) RebuildStreaming(scan func(yield func(id string, vector []float32) error) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.center = nil
	b.trainedAt = 0
	if b.centerOn {
		sum := make([]float64, b.dim)
		n := 0
		err := scan(func(id string, v []float32) error {
			if len(v) != b.dim {
				return fmt.Errorf("binary index: %s has %d dimensions, index has %d", id, len(v), b.dim)
			}
			for d, x := range v {
				sum[d] += float64(x)
			}
			n++
			return nil
		})
		if err != nil {
			return err
		}
		if n > 0 {
			b.center = make([]float32, b.dim)
			for d := range sum {
				b.center[d] = float32(sum[d] / float64(n))
			}
			b.trainedAt = n
		}
	}
	b.ids = b.ids[:0]
	b.pos = make(map[string]int)
	b.codes = b.codes[:0]
	b.vectors = b.vectors[:0]
	return scan(func(id string, v []float32) error {
		if len(v) != b.dim {
			return fmt.Errorf("binary index: %s has %d dimensions, index has %d", id, len(v), b.dim)
		}
		b.insertLocked(id, v)
		return nil
	})
}

// Dim is the dimensionality the index was created for.
func (b *BinaryIndex) Dim() int { return b.dim }

// NeedsRetrain reports whether the center is stale: centering is on and the
// index now holds at least twice as many vectors as the center was learned
// from (or it was never learned and there are enough to learn from). Doubling
// keeps the total re-encoding work linear in the number of inserts.
func (b *BinaryIndex) NeedsRetrain() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.centerOn {
		return false
	}
	const minTrain = 64
	return len(b.ids) >= minTrain && len(b.ids) >= 2*b.trainedAt
}

// Insert adds or replaces a vector.
func (b *BinaryIndex) Insert(id string, vector []float32) error {
	if len(vector) != b.dim {
		return fmt.Errorf("binary index: %s has %d dimensions, index has %d", id, len(vector), b.dim)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.insertLocked(id, vector)
	return nil
}

func (b *BinaryIndex) insertLocked(id string, vector []float32) {
	i, ok := b.pos[id]
	if !ok {
		i = len(b.ids)
		b.pos[id] = i
		b.ids = append(b.ids, id)
		b.codes = append(b.codes, make([]uint64, b.words)...)
		if b.keepVectors {
			b.vectors = append(b.vectors, nil)
		}
	}
	quantization.PackSigns(b.codes[i*b.words:(i+1)*b.words], vector, b.center)
	if b.keepVectors {
		b.vectors[i] = append([]float32(nil), vector...)
	}
}

// Delete removes a vector; it reports whether it was there. The last vector
// moves into the hole, so deletion is O(words) and the codes stay dense.
func (b *BinaryIndex) Delete(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	i, ok := b.pos[id]
	if !ok {
		return false
	}
	last := len(b.ids) - 1
	if i != last {
		b.ids[i] = b.ids[last]
		b.pos[b.ids[i]] = i
		copy(b.codes[i*b.words:(i+1)*b.words], b.codes[last*b.words:])
		if b.keepVectors {
			b.vectors[i] = b.vectors[last]
		}
	}
	b.ids = b.ids[:last]
	b.codes = b.codes[:last*b.words]
	if b.keepVectors {
		b.vectors[last] = nil
		b.vectors = b.vectors[:last]
	}
	delete(b.pos, id)
	return true
}

// Size is the number of vectors indexed.
func (b *BinaryIndex) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.ids)
}

// Oversample is the shortlist multiplier this index uses.
func (b *BinaryIndex) Oversample() int { return b.oversample }

// CodeBytes is the memory the codes occupy: dim/8 bytes per vector, rounded
// up to whole 64-bit words.
func (b *BinaryIndex) CodeBytes() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.codes) * 8
}

// Candidates returns the n stored ids whose codes are nearest the query's by
// Hamming distance, nearest first, with those distances. Ties are broken by
// insertion position, so the result is deterministic.
//
// Selection is a counting pass, not a sort: distances are integers in
// [0, dim], so a histogram finds the cutoff distance in one scan and a second
// collects everything below it. Linear in the index size whatever n is.
func (b *BinaryIndex) Candidates(query []float32, n int) ([]string, []int) {
	if len(query) != b.dim || n <= 0 {
		return nil, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := len(b.ids)
	if count == 0 {
		return nil, nil
	}
	if n > count {
		n = count
	}
	q := make([]uint64, b.words)
	quantization.PackSigns(q, query, b.center)

	distp, _ := b.scratch.Get().(*[]uint32)
	if distp == nil {
		distp = new([]uint32)
	}
	dist := *distp
	if cap(dist) < count {
		dist = make([]uint32, count)
	}
	dist = dist[:count]
	defer func() { *distp = dist; b.scratch.Put(distp) }()

	hist := make([]int, b.dim+2)
	w := b.words
	codes := b.codes
	for i := 0; i < count; i++ {
		d := quantization.HammingWords(q, codes[i*w:(i+1)*w])
		dist[i] = uint32(d)
		hist[d]++
	}
	cutoff, below := 0, 0
	for cutoff = 0; cutoff <= b.dim; cutoff++ {
		if below+hist[cutoff] >= n {
			break
		}
		below += hist[cutoff]
	}
	atCutoff := n - below // how many of the vectors at the cutoff distance fit

	type cand struct {
		i int
		d uint32
	}
	picked := make([]cand, 0, n)
	for i := 0; i < count; i++ {
		d := dist[i]
		if int(d) < cutoff {
			picked = append(picked, cand{i, d})
		} else if int(d) == cutoff && atCutoff > 0 {
			picked = append(picked, cand{i, d})
			atCutoff--
		}
	}
	sort.SliceStable(picked, func(a, c int) bool { return picked[a].d < picked[c].d })
	ids := make([]string, len(picked))
	ds := make([]int, len(picked))
	for j, p := range picked {
		ids[j] = b.ids[p.i]
		ds[j] = int(p.d)
	}
	return ids, ds
}

// Search returns the k nearest vectors by cosine distance: the
// k × Oversample Hamming candidates, rescored exactly with the stored full
// vectors. Without KeepVectors there is nothing to rescore with, and the
// candidates come back in Hamming order with the Hamming distance as a
// fraction of the dimensions.
func (b *BinaryIndex) Search(query []float32, k int) ([]string, []float32) {
	return b.SearchOversampled(query, k, b.oversample)
}

// SearchOversampled is Search with an explicit shortlist multiplier.
// Oversample 1 with rescoring still reorders the k Hamming candidates, but
// cannot recover a true neighbour that missed the shortlist.
func (b *BinaryIndex) SearchOversampled(query []float32, k, oversample int) ([]string, []float32) {
	if k <= 0 {
		return nil, nil
	}
	if oversample < 1 {
		oversample = 1
	}
	ids, hd := b.Candidates(query, k*oversample)
	if !b.keepVectors {
		if len(ids) > k {
			ids, hd = ids[:k], hd[:k]
		}
		out := make([]float32, len(hd))
		for i, d := range hd {
			out[i] = float32(d) / float32(b.dim)
		}
		return ids, out
	}
	b.mu.RLock()
	type scored struct {
		id string
		d  float32
	}
	res := make([]scored, 0, len(ids))
	for _, id := range ids {
		i, ok := b.pos[id]
		if !ok {
			continue // deleted between the scan and the rescoring
		}
		res = append(res, scored{id, CosineDistance(query, b.vectors[i])})
	}
	b.mu.RUnlock()
	sort.SliceStable(res, func(a, c int) bool { return res[a].d < res[c].d })
	if len(res) > k {
		res = res[:k]
	}
	outIDs := make([]string, len(res))
	outD := make([]float32, len(res))
	for i, r := range res {
		outIDs[i], outD[i] = r.id, r.d
	}
	return outIDs, outD
}
